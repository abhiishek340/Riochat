/**
 * Voice dictation on top of the browser's speech recognition. A controller owns
 * the restart policy: recognition stops after each phrase, so it is started
 * again after a short delay, tolerating two empty rounds in a row.
 */

export type VoiceError =
  | "audioCapture"
  | "languageNotSupported"
  | "network"
  | "notAllowed"
  | "serviceNotAllowed"
  | "startFailed"
  | "unavailable"
  | "noSpeech"
  | "interrupted";

export interface RecognitionLike {
  lang: string;
  continuous: boolean;
  interimResults: boolean;
  onresult: ((event: { results: ArrayLike<ArrayLike<{ transcript: string }> & { isFinal?: boolean }>; resultIndex?: number }) => void) | null;
  onerror: ((event: { error: string }) => void) | null;
  onend: (() => void) | null;
  start(): void;
  stop(): void;
  abort(): void;
}

export type RecognitionCtor = new () => RecognitionLike;

export function getRecognitionCtor(win: { SpeechRecognition?: RecognitionCtor; webkitSpeechRecognition?: RecognitionCtor; isSecureContext?: boolean } | undefined): RecognitionCtor | null {
  if (!win || win.isSecureContext === false) return null;
  return win.SpeechRecognition ?? win.webkitSpeechRecognition ?? null;
}

export function errorFor(code: string): VoiceError {
  switch (code) {
    case "audio-capture":
      return "audioCapture";
    case "language-not-supported":
      return "languageNotSupported";
    case "network":
      return "network";
    case "not-allowed":
      return "notAllowed";
    case "service-not-allowed":
      return "serviceNotAllowed";
    default:
      return "unavailable";
  }
}

const RESTART_DELAY_MS = 250;
const MAX_EMPTY_RESTARTS = 2;

export interface VoiceHandlers {
  /** Called with the full text to show: the draft before dictation plus what was heard. */
  onText: (text: string) => void;
  onError: (error: VoiceError) => void;
  onState: (active: boolean) => void;
}

export interface VoiceDeps {
  ctor: RecognitionCtor;
  setTimeout: (fn: () => void, ms: number) => unknown;
  clearTimeout: (id: unknown) => void;
}

export class VoiceController {
  private rec: RecognitionLike | null = null;
  private active = false;
  private base = "";
  private heard = "";
  private emptyRestarts = 0;
  private heardThisRound = false;
  private lastRecoverable: "no-speech" | "aborted" | null = null;
  private timer: unknown = null;

  constructor(private deps: VoiceDeps, private lang: string, private handlers: VoiceHandlers) {}

  get isActive(): boolean {
    return this.active;
  }

  private join(): string {
    const spoken = this.heard.trim();
    if (!spoken) return this.base;
    return this.base && !/\s$/.test(this.base) ? `${this.base} ${spoken}` : `${this.base}${spoken}`;
  }

  start(currentText: string): void {
    if (this.active) return;
    this.base = currentText;
    this.heard = "";
    this.emptyRestarts = 0;
    this.lastRecoverable = null;
    this.active = true;
    this.handlers.onState(true);
    this.begin();
  }

  private begin(): void {
    const rec = new this.deps.ctor();
    this.rec = rec;
    rec.lang = this.lang;
    rec.continuous = false;
    rec.interimResults = true;
    this.heardThisRound = false;
    rec.onresult = (event) => {
      let finals = "";
      let interim = "";
      for (let i = 0; i < event.results.length; i++) {
        const r = event.results[i] as ArrayLike<{ transcript: string }> & { isFinal?: boolean };
        const text = r[0]?.transcript ?? "";
        if (r.isFinal) finals += text;
        else interim += text;
      }
      if (finals) {
        this.heard += (this.heard && !/\s$/.test(this.heard) ? " " : "") + finals;
      }
      if (finals || interim) this.heardThisRound = true;
      const preview = interim ? `${this.heard}${this.heard ? " " : ""}${interim}` : this.heard;
      const keep = this.heard;
      this.heard = preview;
      this.handlers.onText(this.join());
      this.heard = keep;
    };
    rec.onerror = (event) => {
      if (event.error === "no-speech" || event.error === "aborted") {
        this.lastRecoverable = event.error;
        return;
      }
      this.finish(errorFor(event.error));
    };
    rec.onend = () => {
      if (!this.active) return;
      if (this.heardThisRound) this.emptyRestarts = 0;
      else if (++this.emptyRestarts > MAX_EMPTY_RESTARTS) {
        this.finish(this.lastRecoverable === "aborted" ? "interrupted" : "noSpeech");
        return;
      }
      this.timer = this.deps.setTimeout(() => {
        if (!this.active) return;
        try {
          this.begin();
        } catch {
          this.finish("startFailed");
        }
      }, RESTART_DELAY_MS);
    };
    try {
      rec.start();
    } catch {
      this.finish("startFailed");
    }
  }

  private finish(error: VoiceError | null): void {
    const was = this.active;
    this.active = false;
    if (this.timer !== null) this.deps.clearTimeout(this.timer);
    this.timer = null;
    const rec = this.rec;
    this.rec = null;
    if (rec) {
      rec.onresult = rec.onerror = rec.onend = null;
      try {
        rec.abort();
      } catch {
        /* already stopped */
      }
    }
    if (was) this.handlers.onState(false);
    if (error) this.handlers.onError(error);
  }

  /** Stops dictation and keeps what was heard. */
  stop(): void {
    this.finish(null);
  }

  /** Stops dictation and restores the text from before it started. */
  cancel(): void {
    this.finish(null);
    this.handlers.onText(this.base);
  }
}
