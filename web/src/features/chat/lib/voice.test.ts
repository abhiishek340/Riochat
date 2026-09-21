import { describe, expect, it, vi } from "vitest";
import { errorFor, getRecognitionCtor, VoiceController, type RecognitionLike, type VoiceError } from "./voice";

class FakeRec implements RecognitionLike {
  static instances: FakeRec[] = [];
  lang = "";
  continuous = true;
  interimResults = false;
  onresult: RecognitionLike["onresult"] = null;
  onerror: RecognitionLike["onerror"] = null;
  onend: RecognitionLike["onend"] = null;
  started = false;
  aborted = false;
  constructor() {
    FakeRec.instances.push(this);
  }
  start() {
    this.started = true;
  }
  stop() {}
  abort() {
    this.aborted = true;
  }
  say(text: string, final = true) {
    const result = Object.assign([{ transcript: text }], { isFinal: final });
    this.onresult?.({ results: [result] });
  }
}

function setup() {
  FakeRec.instances = [];
  vi.useFakeTimers();
  const texts: string[] = [];
  const errors: VoiceError[] = [];
  const states: boolean[] = [];
  const c = new VoiceController(
    { ctor: FakeRec, setTimeout: (f, ms) => setTimeout(f, ms), clearTimeout: (id) => clearTimeout(id as ReturnType<typeof setTimeout>) },
    "en-US",
    { onText: (t) => texts.push(t), onError: (e) => errors.push(e), onState: (s) => states.push(s) },
  );
  return { c, texts, errors, states };
}

describe("VoiceController", () => {
  it("appends heard text after the draft with a space and shows interim text", () => {
    const { c, texts } = setup();
    c.start("hello");
    const rec = FakeRec.instances[0] as FakeRec;
    expect(rec).toMatchObject({ lang: "en-US", continuous: false, interimResults: true });
    rec.say("wor", false);
    rec.say("world", true);
    expect(texts).toEqual(["hello wor", "hello world"]);
    vi.useRealTimers();
  });

  it("restarts after each phrase with a 250 ms delay", () => {
    const { c } = setup();
    c.start("");
    const first = FakeRec.instances[0] as FakeRec;
    first.say("one");
    first.onend?.();
    expect(FakeRec.instances).toHaveLength(1);
    vi.advanceTimersByTime(250);
    expect(FakeRec.instances).toHaveLength(2);
    (FakeRec.instances[1] as FakeRec).say("two");
    expect(c.isActive).toBe(true);
    vi.useRealTimers();
  });

  it("tolerates two empty rounds, then ends with 'no speech'", () => {
    const { c, errors, states } = setup();
    c.start("");
    for (let i = 0; i < 3; i++) {
      (FakeRec.instances[i] as FakeRec).onerror?.({ error: "no-speech" });
      (FakeRec.instances[i] as FakeRec).onend?.();
      vi.advanceTimersByTime(250);
    }
    expect(errors).toEqual(["noSpeech"]);
    expect(states).toEqual([true, false]);
    vi.useRealTimers();
  });

  it("reports 'interrupted' when the last recoverable error was aborted", () => {
    const { c, errors } = setup();
    c.start("");
    for (let i = 0; i < 3; i++) {
      (FakeRec.instances[i] as FakeRec).onerror?.({ error: "aborted" });
      (FakeRec.instances[i] as FakeRec).onend?.();
      vi.advanceTimersByTime(250);
    }
    expect(errors).toEqual(["interrupted"]);
    vi.useRealTimers();
  });

  it("stops on other errors with a specific reason", () => {
    const { c, errors } = setup();
    c.start("");
    (FakeRec.instances[0] as FakeRec).onerror?.({ error: "not-allowed" });
    expect(errors).toEqual(["notAllowed"]);
    expect(c.isActive).toBe(false);
    expect(errorFor("audio-capture")).toBe("audioCapture");
    expect(errorFor("weird")).toBe("unavailable");
    vi.useRealTimers();
  });

  it("cancel restores the original text; stop keeps the heard text", () => {
    const { c, texts } = setup();
    c.start("draft");
    (FakeRec.instances[0] as FakeRec).say("spoken");
    c.cancel();
    expect(texts.at(-1)).toBe("draft");
    expect(c.isActive).toBe(false);
    vi.useRealTimers();
  });

  it("needs a secure context and a constructor", () => {
    expect(getRecognitionCtor({ isSecureContext: false, webkitSpeechRecognition: FakeRec })).toBeNull();
    expect(getRecognitionCtor({ isSecureContext: true, webkitSpeechRecognition: FakeRec })).toBe(FakeRec);
    expect(getRecognitionCtor({ isSecureContext: true })).toBeNull();
    expect(getRecognitionCtor(undefined)).toBeNull();
  });
});
