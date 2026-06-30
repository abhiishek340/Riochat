"use client";

import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type ClipboardEvent, type KeyboardEvent } from "react";
import { ArrowUp, Eye, EyeOff, Image as ImageIcon, Mic, MicOff, Monitor, Paperclip, Plus, Settings2, Square, X } from "lucide-react";
import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, Tooltip, toast } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { attachmentFromFile } from "../../lib/attachments";
import { pastedName } from "../../lib/file-policy";
import { detectTrigger, removeTrigger, replaceTrigger, type Trigger } from "../../lib/mention";
import { editorPasteToMarkdown, htmlToMarkdown, insertBlock, isLongPaste } from "../../lib/paste";
import { browserIO, recordRecent, type RecentKind } from "../../lib/persist";
import { INPUT_MAX_HEIGHT_REM, isApple, isComposingKey, isSendKey } from "../../lib/settings";
import { getRecognitionCtor, VoiceController, type RecognitionCtor, type VoiceError } from "../../lib/voice";
import { CODE_CLIPBOARD_TYPE } from "../../lib/clipboard";
import { isMediaTask, type TaskKind } from "../../lib/task-decision";
import { useBilling } from "../../state/resources";
import type { FileDTO, McpToolDTO, PromptPresetDTO, SkillSummaryDTO } from "../../types";
import { Markdown } from "../markdown/markdown";
import { AdvancedOptionsDialog } from "./advanced-dialog";
import { AttachmentChips } from "./attachment-chips";
import { ModelControls } from "./controls-bar";
import { MentionMenu, type MentionHandle, type MentionKind } from "./mention-menu";
import { ModelPicker } from "./model-picker";
import { KnowledgePicker, ToolsPicker, UiComponentsPicker } from "./pickers";
import { QueuedList } from "./queued-list";
import type { ComposerApi, ComposerController } from "./types";

interface MenuState {
  trigger: "@" | "/";
  query: string;
  start: number;
  browse: boolean;
}

function mediaPlaceholderKey(task: TaskKind): string {
  switch (task) {
    case "image_generation":
      return "chatComposer.placeholderMedia.imageGeneration";
    case "image_edit":
      return "chatComposer.placeholderMedia.imageEdit";
    case "video_generation":
      return "chatComposer.placeholderMedia.videoGeneration";
    default:
      return "chatComposer.placeholderMedia.videoExtension";
  }
}

export function Composer({ c }: { c: ComposerController }) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const billing = useBilling();
  void billing;
  const { draft, model, prefs } = c;
  const ref = useRef<HTMLTextAreaElement>(null);
  const frame = useRef<HTMLDivElement>(null);
  const menuRef = useRef<MentionHandle>(null);
  const mentionListId = useId();
  const fileInput = useRef<HTMLInputElement>(null);
  const [menu, setMenu] = useState<MenuState | null>(null);
  const [preview, setPreview] = useState(false);
  const [advanced, setAdvanced] = useState(false);
  const [voiceOn, setVoiceOn] = useState(false);
  const voice = useRef<VoiceController | null>(null);
  const [pasted, setPasted] = useState<Record<string, string>>({});
  const apple = typeof navigator !== "undefined" && isApple(navigator.platform ?? "");

  const media = isMediaTask(c.decision.task);
  const blocked = c.decision.blocked;
  const hasContent = draft.text.trim() !== "" || draft.attachments.length > 0;
  const canSend = hasContent && !c.conversationLoading && !draft.busy && !(c.temporary && c.busy);
  const queueMode = c.busy && !c.temporary && hasContent;
  const pastedIds = useMemo(() => new Set(Object.keys(pasted)), [pasted]);

  // --- imperative api for the page --------------------------------------------------------
  const insert = useCallback(
    (text: string) => {
      const el = ref.current;
      const start = el?.selectionStart ?? draft.text.length;
      const end = el?.selectionEnd ?? draft.text.length;
      const next = insertBlock(draft.text, start, end, text);
      draft.setText(next.text);
      requestAnimationFrame(() => {
        ref.current?.focus();
        ref.current?.setSelectionRange(next.caret, next.caret);
      });
    },
    [draft],
  );
  const api = useMemo<ComposerApi>(() => ({ focus: () => ref.current?.focus(), insert }), [insert]);
  const { registerApi } = c;
  useEffect(() => {
    registerApi(api);
    return () => registerApi(null);
  }, [api, registerApi]);

  // --- sizing and focus ------------------------------------------------------------------------
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "auto";
    const max = INPUT_MAX_HEIGHT_REM[prefs.inputHeight] * 16;
    el.style.height = `${Math.min(el.scrollHeight, max)}px`;
    el.style.overflowY = el.scrollHeight > max ? "auto" : "hidden";
  }, [draft.text, prefs.inputHeight]);

  useEffect(() => {
    if (window.matchMedia("(pointer: coarse)").matches) return;
    ref.current?.focus();
  }, [c.chatKey]);

  // --- mention menu ---------------------------------------------------------------------------------
  const kinds = useMemo<MentionKind[]>(() => {
    if (!menu) return [];
    if (menu.trigger === "/") return ["skill", "prompt"];
    const list: MentionKind[] = ["model"];
    if (!c.temporary) list.push("file");
    if (c.kbEnabled && c.filePolicy?.ragAvailable !== false && !media) list.push("knowledge");
    if (!media && c.tools.length > 0) list.push("tool");
    return list;
  }, [menu, c.temporary, c.kbEnabled, c.filePolicy?.ragAvailable, media, c.tools.length]);

  const syncMenu = useCallback(
    (text: string, start: number, end: number) => {
      if (c.conversationLoading || draft.busy || c.modelsLoading) return setMenu(null);
      const trig: Trigger | null = detectTrigger(text, start, end);
      setMenu((cur) => {
        if (cur?.browse) return cur;
        return trig ? { trigger: trig.trigger, query: trig.query, start: trig.start, browse: false } : null;
      });
    },
    [c.conversationLoading, c.modelsLoading, draft.busy],
  );

  const pick = useCallback(
    (kind: MentionKind, payload: unknown) => {
      const el = ref.current;
      const caret = el?.selectionStart ?? draft.text.length;
      const closeTrigger = () => {
        if (!menu || menu.browse) return draft.text;
        const trig: Trigger = { trigger: menu.trigger, start: menu.start, query: menu.query };
        const res = removeTrigger(draft.text, trig, caret);
        draft.setText(res.text);
        requestAnimationFrame(() => ref.current?.setSelectionRange(res.caret, res.caret));
        return res.text;
      };
      recordRecent(browserIO, kind as RecentKind, kind === "model" ? (payload as { platformModelName: string }).platformModelName : kind === "file" ? (payload as FileDTO).fileID : kind === "knowledge" ? (payload as { publicID: string }).publicID : String((payload as { id: number }).id), Date.now());
      if (kind === "model") {
        model.select((payload as { platformModelName: string }).platformModelName);
        closeTrigger();
        setMenu(null);
      } else if (kind === "file") {
        closeTrigger();
        c.draft.addExisting([attachmentFromFile(payload as FileDTO)]);
        setMenu(null);
      } else if (kind === "prompt") {
        const preset = payload as PromptPresetDTO;
        if (menu && !menu.browse) {
          const trig: Trigger = { trigger: menu.trigger, start: menu.start, query: menu.query };
          const res = replaceTrigger(draft.text, trig, caret, preset.content);
          draft.setText(res.text);
          requestAnimationFrame(() => ref.current?.setSelectionRange(res.caret, res.caret));
        } else insert(preset.content);
        setMenu(null);
      } else {
        // Tools, skills and knowledge bases toggle and the menu stays open for more picks.
        if (kind === "tool") c.selections.toggleTool(payload as McpToolDTO);
        else if (kind === "skill") c.selections.toggleSkill(payload as SkillSummaryDTO);
        else c.selections.toggleKb((payload as { publicID: string }).publicID);
        closeTrigger();
        setMenu((cur) => (cur ? { ...cur, browse: true, query: "" } : cur));
      }
      ref.current?.focus();
    },
    [c.draft, c.selections, draft, insert, menu, model],
  );

  // --- keyboard ------------------------------------------------------------------------------------------
  const onKeyDown = (e: KeyboardEvent<HTMLTextAreaElement>) => {
    if (menu && menuRef.current?.handleKey(e)) return;
    if (isComposingKey(e)) return;
    if (e.key === "Escape" && menu) {
      setMenu(null);
      return;
    }
    if (isSendKey(e, prefs.sendOn, apple)) {
      e.preventDefault();
      if (canSend) c.onSubmit();
      return;
    }
    if (e.key === "Enter" && !e.shiftKey && !e.altKey && prefs.sendOn === "modifier") return; // plain Enter inserts a newline
  };

  // --- paste ---------------------------------------------------------------------------------------------------
  const onPaste = async (e: ClipboardEvent<HTMLTextAreaElement>) => {
    const data = e.clipboardData;
    if (!data) return;
    const el = e.currentTarget;
    const plain = data.getData("text/plain");
    const files: File[] = [];
    for (const item of Array.from(data.items ?? [])) {
      if (item.kind === "file") {
        const f = item.getAsFile();
        if (f) files.push(f);
      }
    }
    if (files.length === 0 && data.files?.length) files.push(...Array.from(data.files));
    if (files.length > 0) {
      if (!plain) e.preventDefault();
      let n = 0;
      const renamed = files.map((f) => {
        const ext = f.name.includes(".") ? f.name.slice(f.name.lastIndexOf(".")) : f.type.startsWith("image/") ? `.${f.type.slice(6).replace("jpeg", "jpg")}` : "";
        return new File([f], pastedName(f.type.startsWith("image/") ? "image" : "file", ++n, ext), { type: f.type });
      });
      void draft.addFiles(media ? renamed.filter((f) => f.type.startsWith("image/")) : renamed);
      if (!plain) return;
    }
    // The app's own code blocks paste as plain text.
    if (Array.from(data.types ?? []).includes(CODE_CLIPBOARD_TYPE)) return;
    const editor = editorPasteToMarkdown(data.getData("vscode-editor-data") || undefined, plain);
    if (editor) {
      e.preventDefault();
      const r = insertBlock(draft.text, el.selectionStart, el.selectionEnd, editor);
      draft.setText(r.text);
      requestAnimationFrame(() => el.setSelectionRange(r.caret, r.caret));
      return;
    }
    const html = data.getData("text/html");
    if (html && plain) {
      const md = htmlToMarkdown(html, plain);
      if (md) {
        e.preventDefault();
        const r = insertBlock(draft.text, el.selectionStart, el.selectionEnd, md);
        draft.setText(r.text);
        requestAnimationFrame(() => el.setSelectionRange(r.caret, r.caret));
        return;
      }
    }
    if (plain && isLongPaste(plain) && !media && !draft.busy && files.length === 0 && !html) {
      e.preventDefault();
      const file = new File([plain], `pasted-text-${Date.now()}.txt`, { type: "text/plain" });
      const added = await draft.addFiles([file]);
      // If the upload was rejected or failed, the text goes into the draft instead.
      if (added[0]) setPasted((cur) => ({ ...cur, [added[0]!.fileId]: plain }));
      else insert(plain);
    }
  };

  const insertBack = (a: { fileId: string }) => {
    const text = pasted[a.fileId];
    if (text === undefined) return;
    insert(text);
    draft.removeAttachment(a.fileId);
    setPasted((cur) => {
      const { [a.fileId]: _gone, ...rest } = cur;
      return rest;
    });
  };

  // --- screenshot ------------------------------------------------------------------------------------------
  const screenshot = async () => {
    if (!navigator.mediaDevices?.getDisplayMedia) return void toast.error(t("chatComposer.attach.screenshotUnsupported"));
    let stream: MediaStream | null = null;
    try {
      stream = await navigator.mediaDevices.getDisplayMedia({ video: true, audio: false });
      const video = document.createElement("video");
      video.srcObject = stream;
      video.muted = true;
      await video.play();
      await new Promise((r) => requestAnimationFrame(r));
      const canvas = document.createElement("canvas");
      canvas.width = video.videoWidth;
      canvas.height = video.videoHeight;
      canvas.getContext("2d")?.drawImage(video, 0, 0);
      const blob = await new Promise<Blob | null>((r) => canvas.toBlob(r, "image/png"));
      if (!blob) throw new Error("no image");
      const stamp = new Date().toISOString().replace(/[-:T.Z]/g, "").slice(0, 14);
      await draft.addFiles([new File([blob], `screenshot-${stamp}.png`, { type: "image/png" })]);
    } catch (error) {
      if (error instanceof DOMException && error.name === "NotAllowedError") return;
      toast.error(t("chatComposer.attach.screenshotFailed"), { description: errorMessage(error) });
    } finally {
      stream?.getTracks().forEach((track) => track.stop());
    }
  };

  // --- voice ----------------------------------------------------------------------------------------------------
  const ctor: RecognitionCtor | null = useMemo(() => (typeof window === "undefined" ? null : getRecognitionCtor(window as unknown as Parameters<typeof getRecognitionCtor>[0])), []);
  const toggleVoice = () => {
    if (!ctor) return;
    if (voice.current?.isActive) {
      voice.current.stop();
      return;
    }
    voice.current = new VoiceController({ ctor, setTimeout: (f, ms) => setTimeout(f, ms), clearTimeout: (id) => clearTimeout(id as ReturnType<typeof setTimeout>) }, c.locale, {
      onText: (text) => draft.setText(text),
      onState: setVoiceOn,
      onError: (err: VoiceError) => toast.error(t("chatComposer.voice.errorTitle"), { description: t(`chatComposer.voice.error.${err}` as "chatComposer.voice.error.unavailable") }),
    });
    voice.current.start(draft.text);
  };
  useEffect(() => () => voice.current?.stop(), []);

  const placeholder = voiceOn
    ? t("chatComposer.placeholderListening")
    : c.dragging
      ? t("chatComposer.placeholderDrop")
      : blocked && draft.attachments.length === 0
        ? t(`chatComposer.blocked.${blocked}` as "chatComposer.blocked.B1")
        : media
          ? t(mediaPlaceholderKey(c.decision.task) as "chatComposer.placeholderMedia.imageGeneration")
          : t("chatComposer.placeholder");

  const ragWarn = c.filePolicy?.fileMode === "rag" && draft.attachments.some((a) => a.kind !== "image") && draft.attachments.filter((a) => a.kind !== "image").every((a) => a.ragOptOut);
  const [ragDismissed, setRagDismissed] = useState(false);

  const primary = () => {
    if (c.busy && !hasContent) return "stop" as const;
    if (hasContent) return queueMode ? ("queue" as const) : ("send" as const);
    return "mic" as const;
  };
  const mode = primary();

  return (
    <div className={cn("relative w-full", c.landing ? "" : "")} data-composer>
      {!c.temporary ? <QueuedList items={c.queued} onEdit={c.onEditQueued} onRemove={c.onRemoveQueued} onGuide={c.onGuideQueued} /> : null}
      {c.temporary ? <p className="mb-2 rounded-lg bg-muted/60 px-3 py-1.5 text-center text-xs text-muted-foreground">{t("chatMisc.temp.notice")}</p> : null}
      {ragWarn && !ragDismissed ? (
        <div className="mb-2 flex items-start gap-2 rounded-lg border border-warning/40 bg-warning/10 px-3 py-1.5 text-xs" role="status">
          <span className="flex-1">{t("chatComposer.attach.ragWarning")}</span>
          <button type="button" onClick={() => setRagDismissed(true)} aria-label={t("common.actions.close")} className="text-muted-foreground hover:text-foreground">
            <X className="size-3.5" />
          </button>
        </div>
      ) : null}
      {blocked && draft.attachments.length > 0 ? (
        <div role="alert" className="mb-2 rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-1.5 text-xs text-destructive">
          {t(`chatComposer.blocked.${blocked}` as "chatComposer.blocked.B1")}
        </div>
      ) : null}

      <div
        ref={frame}
        className={cn(
          "relative rounded-3xl border bg-card shadow-sm transition-shadow focus-within:border-ring focus-within:shadow-md",
          c.dragging && "border-primary ring-2 ring-primary/30",
        )}
      >
        <AttachmentChips draft={draft} fileMode={c.filePolicy?.fileMode ?? "auto"} onInsertBack={insertBack} pastedTexts={pastedIds} />
        {preview && draft.text.trim() ? (
          <div className="mx-3 mt-3 max-h-60 overflow-auto rounded-xl border bg-background p-3" data-composer-preview>
            <Markdown content={draft.text} variant="user" scope="preview" readOnly />
          </div>
        ) : null}
        <textarea
          ref={ref}
          value={draft.text}
          rows={1}
          disabled={c.conversationLoading}
          readOnly={voiceOn}
          placeholder={placeholder}
          aria-label={t("chatComposer.inputLabel")}
          aria-haspopup="listbox"
          aria-controls={menu ? mentionListId : undefined}
          onChange={(e) => {
            draft.setText(e.target.value);
            syncMenu(e.target.value, e.target.selectionStart, e.target.selectionEnd);
          }}
          onKeyDown={onKeyDown}
          onKeyUp={(e) => {
            if (["ArrowLeft", "ArrowRight", "Home", "End"].includes(e.key)) syncMenu(e.currentTarget.value, e.currentTarget.selectionStart, e.currentTarget.selectionEnd);
          }}
          onClick={(e) => syncMenu(e.currentTarget.value, e.currentTarget.selectionStart, e.currentTarget.selectionEnd)}
          onBlur={() => setTimeout(() => setMenu(null), 120)}
          onPaste={onPaste}
          className="block max-h-none w-full resize-none bg-transparent px-4 pb-1 pt-3.5 text-[0.9375rem] leading-6 outline-none placeholder:text-muted-foreground/80 disabled:opacity-60"
        />
        <MentionMenu
          ref={menuRef}
          open={!!menu}
          trigger={menu?.trigger ?? "@"}
          query={menu?.query ?? ""}
          anchorRef={frame}
          kinds={kinds}
          models={c.models}
          selectedModel={model.model?.platformModelName ?? ""}
          tools={c.temporary ? c.tools.filter((x) => x.attachmentInputMode !== "image") : c.tools}
          selectedToolIds={c.selections.toolIds}
          selectedSkillIds={c.selections.skills.map((s) => s.id)}
          selectedKbIds={c.selections.kbIds}
          kbAvailable={c.filePolicy?.ragAvailable !== false}
          onPick={pick}
          onClose={() => setMenu(null)}
          listId={mentionListId}
        />

        <div className="flex flex-wrap items-center gap-x-1 gap-y-1 px-2 pb-2 pt-1">
          <input
            ref={fileInput}
            type="file"
            multiple
            hidden
            onChange={(e) => {
              const files = Array.from(e.target.files ?? []);
              e.target.value = "";
              void draft.addFiles(media && c.decision.task !== "video_extension" ? files : files);
            }}
          />
          <DropdownMenu>
            <Tooltip label={t("chatComposer.attach.add")}>
              <DropdownMenuTrigger asChild>
                <button type="button" aria-label={t("chatComposer.attach.add")} className="inline-flex size-8 items-center justify-center rounded-full text-muted-foreground outline-none hover:bg-accent hover:text-accent-foreground focus-visible:ring-2 focus-visible:ring-ring">
                  <Plus className="size-4.5" />
                </button>
              </DropdownMenuTrigger>
            </Tooltip>
            <DropdownMenuContent align="start" side="top">
              <DropdownMenuItem onSelect={() => fileInput.current?.click()}>
                <Paperclip /> {t("chatComposer.attach.upload")}
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => void screenshot()}>
                <Monitor /> {t("chatComposer.attach.screenshot")}
              </DropdownMenuItem>
              <DropdownMenuItem
                onSelect={() => {
                  ref.current?.focus();
                  setMenu({ trigger: "@", query: "", start: draft.text.length, browse: true });
                }}
              >
                <ImageIcon /> {t("chatComposer.attach.fromLibrary")}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>

          <ModelPicker models={c.models} selected={model.model} loading={c.modelsLoading} currency={c.currency} locale={c.locale} onSelect={model.select} placement={c.pickerPlacement} />
          {model.model ? <ModelControls model={model} prefs={{ placements: prefs.placements }} media={media} policy={c.policy} extension={c.decision.task === "video_extension"} /> : null}
          {!media ? (
            <>
              <ToolsPicker tools={c.temporary ? c.tools.filter((x) => x.attachmentInputMode !== "image") : c.tools} loaded={c.toolsLoaded} selectedIds={c.selections.toolIds} cap={c.selections.toolCap} defaultIds={prefs.defaultToolIds} onToggle={c.selections.toggleTool} onToggleServer={c.selections.toggleServerTools} currency={c.currency} />
              {c.kbEnabled ? <KnowledgePicker selectedIds={c.selections.kbIds} onToggle={c.selections.toggleKb} ragAvailable={c.filePolicy?.ragAvailable !== false} ragReason={c.filePolicy?.ragAvailabilityReason ?? ""} /> : null}
              <UiComponentsPicker components={c.uiComponents} effective={c.uiChoice.effective} explicit={c.uiChoice.explicit} onChange={c.uiChoice.set} onReset={c.uiChoice.reset} />
              <Tooltip label={c.visualPrompt[0] ? t("chatComposer.visual.enabled") : t("chatComposer.visual.disabled")}>
                <button
                  type="button"
                  aria-pressed={c.visualPrompt[0]}
                  aria-label={t("chatComposer.visual.label")}
                  onClick={() => c.visualPrompt[1](!c.visualPrompt[0])}
                  className={cn("inline-flex size-8 items-center justify-center rounded-lg outline-none focus-visible:ring-2 focus-visible:ring-ring", c.visualPrompt[0] ? "bg-primary/12 text-primary" : "text-muted-foreground hover:bg-accent hover:text-accent-foreground")}
                >
                  <ImageIcon className="size-4" />
                </button>
              </Tooltip>
            </>
          ) : null}
          {(c.isAdmin && !media) || (media && c.policy?.mode !== "disabled") ? (
            <Tooltip label={t("chatComposer.advanced.open")}>
              <button type="button" aria-label={t("chatComposer.advanced.open")} onClick={() => setAdvanced(true)} className="inline-flex size-8 items-center justify-center rounded-lg text-muted-foreground outline-none hover:bg-accent hover:text-accent-foreground focus-visible:ring-2 focus-visible:ring-ring">
                <Settings2 className="size-4" />
              </button>
            </Tooltip>
          ) : null}

          <div className="ml-auto flex items-center gap-1">
            {draft.text.trim() ? (
              <Tooltip label={preview ? t("chatComposer.preview.hide") : t("chatComposer.preview.show")}>
                <button type="button" aria-pressed={preview} aria-label={t("chatComposer.preview.toggle")} onClick={() => setPreview((v) => !v)} className="inline-flex size-8 items-center justify-center rounded-lg text-muted-foreground outline-none hover:bg-accent hover:text-accent-foreground focus-visible:ring-2 focus-visible:ring-ring">
                  {preview ? <EyeOff className="size-4" /> : <Eye className="size-4" />}
                </button>
              </Tooltip>
            ) : null}
            {mode === "mic" ? (
              <Tooltip label={!ctor ? t("chatComposer.voice.unsupported") : voiceOn ? t("chatComposer.voice.cancel") : t("chatComposer.voice.start")}>
                <Button
                  size="icon"
                  variant={voiceOn ? "default" : "ghost"}
                  className="rounded-full"
                  disabled={!ctor || c.conversationLoading}
                  aria-label={!ctor ? t("chatComposer.voice.unsupported") : voiceOn ? t("chatComposer.voice.cancel") : t("chatComposer.voice.start")}
                  onClick={toggleVoice}
                >
                  {voiceOn ? <MicOff /> : <Mic />}
                </Button>
              </Tooltip>
            ) : mode === "stop" ? (
              <Tooltip label={t("chatComposer.stop")}>
                <Button size="icon" className="rounded-full" aria-label={t("chatComposer.stop")} onClick={c.onStop}>
                  <Square className="fill-current" />
                </Button>
              </Tooltip>
            ) : (
              <Tooltip label={mode === "queue" ? t("chatComposer.queueMessage") : t("chatComposer.send")}>
                <Button
                  size="icon"
                  className="rounded-full"
                  aria-label={mode === "queue" ? t("chatComposer.queueMessage") : t("chatComposer.send")}
                  disabled={!canSend}
                  onClick={() => {
                    voice.current?.stop();
                    c.onSubmit();
                  }}
                >
                  <ArrowUp />
                </Button>
              </Tooltip>
            )}
          </div>
        </div>
      </div>
      <AdvancedOptionsDialog open={advanced} onOpenChange={setAdvanced} model={model} policy={c.policy} />
    </div>
  );
}
