"use client";

import { memo, useId, useLayoutEffect, useRef, useState } from "react";
import { Check, Copy, Pencil, RotateCcw, Trash2 } from "lucide-react";
import { Badge, Button, Textarea, Tooltip, toast } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { useCopy } from "../../hooks/use-copy";
import { detectTrigger, removeTrigger, replaceTrigger, type Trigger } from "../../lib/mention";
import { browserIO, recordRecent } from "../../lib/persist";
import { isComposingKey, isApple, isSendKey } from "../../lib/settings";
import type { Navigator } from "../../lib/tree";
import type { ChatMessage, PromptPresetDTO } from "../../types";
import { MentionMenu, type MentionHandle, type MentionKind } from "../composer/mention-menu";
import { Markdown } from "../markdown/markdown";
import { AttachmentCards } from "./attachment-cards";
import { useMessageEnv } from "./env";
import { BranchNav, IconAction, MessageTime } from "./parts";

const COLLAPSED_PX = 24 * 6;

export const UserMessage = memo(function UserMessage({ message, nav }: { message: ChatMessage; nav?: Navigator }) {
  const t = useT();
  const env = useMessageEnv();
  const { copied, copy } = useCopy();
  const [editing, setEditing] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const [overflows, setOverflows] = useState(false);
  const body = useRef<HTMLDivElement>(null);
  const pending = message.status === "pending" || !message.persisted;
  const canDelete = message.persisted && !!message.parentId && !env.readOnly && !env.temporary;

  useLayoutEffect(() => {
    const el = body.current;
    if (el) setOverflows(el.scrollHeight > COLLAPSED_PX + 4);
  }, [message.content, editing]);

  if (editing) {
    return (
      <EditBox
        initial={message.content}
        onCancel={() => setEditing(false)}
        onSave={async (text) => {
          const ok = await env.actions.saveEdit(message, text);
          if (ok) setEditing(false);
        }}
        onRegenerate={(text) => {
          setEditing(false);
          env.actions.regenerate(message, text);
        }}
        mentions
      />
    );
  }

  return (
    <div className="flex flex-col items-end gap-1.5" data-role="user">
      <AttachmentCards attachments={message.attachments} className="justify-end" />
      {message.content ? (
        <div className="max-w-[min(100%,42rem)] rounded-2xl rounded-br-md bg-secondary px-4 py-2.5 text-secondary-foreground">
          <div ref={body} className={cn("overflow-hidden", !expanded && overflows && "rio-collapsed-fade")} style={!expanded ? { maxHeight: COLLAPSED_PX } : undefined} data-user-text>
            <Markdown content={message.content} variant="user" scope={`u-${message.id}`} readOnly={env.readOnly} />
          </div>
          {overflows ? (
            <button type="button" onClick={() => setExpanded((v) => !v)} aria-expanded={expanded} className="mt-1 text-xs font-medium text-muted-foreground hover:text-foreground" data-capture-hide>
              {expanded ? t("chatMsg.collapse") : t("chatMsg.expand")}
            </button>
          ) : null}
        </div>
      ) : null}
      <div className="flex min-h-7 items-center justify-end gap-0.5 opacity-0 transition-opacity focus-within:opacity-100 group-hover/msg:opacity-100 [@media(hover:none)]:opacity-100" data-capture-hide data-actions>
        {message.editedAt ? (
          <Tooltip label={t("chatMsg.editedUserTip")}>
            <Badge tone="outline" className="mr-1">
              {t("chatMsg.edited")}
            </Badge>
          </Tooltip>
        ) : null}
        <MessageTime iso={message.createdAt} />
        {!env.readOnly ? (
          <>
            <IconAction label={t("chatMsg.retry")} disabled={pending} onClick={() => env.actions.retry(message)}>
              <RotateCcw />
            </IconAction>
            <IconAction label={t("chatMsg.edit")} disabled={pending} onClick={() => setEditing(true)}>
              <Pencil />
            </IconAction>
          </>
        ) : null}
        <IconAction
          label={copied ? t("chatMsg.copied") : t("chatMsg.copy")}
          onClick={async () => {
            if (!(await copy(message.content))) toast.error(t("chatRich.md.copyFailed"));
          }}
        >
          {copied ? <Check /> : <Copy />}
        </IconAction>
        {canDelete ? (
          <IconAction label={t("chatMsg.delete")} disabled={pending || env.busy} onClick={() => env.actions.remove(message)}>
            <Trash2 />
          </IconAction>
        ) : null}
        {nav && !env.temporary ? <BranchNav nav={nav} disabled={pending} onCycle={(d) => env.actions.cycle(message, d)} /> : null}
      </div>
    </div>
  );
});

/** Inline editor shared by user and assistant messages. */
export function EditBox({
  initial,
  onCancel,
  onSave,
  onRegenerate,
  mentions = false,
  busy = false,
}: {
  initial: string;
  onCancel: () => void;
  onSave: (text: string) => void | Promise<void>;
  onRegenerate?: (text: string) => void;
  /** Offer the `@` (models) and `/` (prompts) menus while typing. */
  mentions?: boolean;
  busy?: boolean;
}) {
  const t = useT();
  const env = useMessageEnv();
  const [text, setText] = useState(initial);
  const [saving, setSaving] = useState(false);
  const [menu, setMenu] = useState<{ trigger: "@" | "/"; query: string; start: number } | null>(null);
  const ref = useRef<HTMLTextAreaElement>(null);
  const frame = useRef<HTMLDivElement>(null);
  const menuRef = useRef<MentionHandle>(null);
  const listId = useId();
  const mention = mentions ? env.mention : undefined;
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.focus();
    el.setSelectionRange(el.value.length, el.value.length);
  }, []);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 360)}px`;
  }, [text]);
  const trimmed = text.trim();
  const unchanged = trimmed === initial.trim();
  const disabled = !trimmed || unchanged || busy || saving;
  const apple = typeof navigator !== "undefined" && isApple(navigator.platform ?? "");

  const save = async () => {
    if (disabled) return;
    setSaving(true);
    try {
      await onSave(trimmed);
    } finally {
      setSaving(false);
    }
  };

  const syncMenu = (value: string, start: number, end: number) => {
    if (!mention) return;
    const trig = detectTrigger(value, start, end);
    setMenu(trig ? { trigger: trig.trigger, query: trig.query, start: trig.start } : null);
  };

  const pick = (kind: MentionKind, payload: unknown) => {
    const el = ref.current;
    const caret = el?.selectionStart ?? text.length;
    if (!menu || !mention) return;
    const trig: Trigger = { trigger: menu.trigger, start: menu.start, query: menu.query };
    if (kind === "model") {
      const name = (payload as { platformModelName: string }).platformModelName;
      recordRecent(browserIO, "model", name, Date.now());
      mention.selectModel(name);
      const res = removeTrigger(text, trig, caret);
      setText(res.text);
      requestAnimationFrame(() => ref.current?.setSelectionRange(res.caret, res.caret));
    } else if (kind === "prompt") {
      const preset = payload as PromptPresetDTO;
      recordRecent(browserIO, "prompt", String(preset.id), Date.now());
      const res = replaceTrigger(text, trig, caret, preset.content);
      setText(res.text);
      requestAnimationFrame(() => ref.current?.setSelectionRange(res.caret, res.caret));
    }
    setMenu(null);
    ref.current?.focus();
  };

  return (
    <div ref={frame} className="relative grid w-full gap-2 rounded-2xl border bg-card p-2.5 shadow-sm" data-capture-hide>
      <Textarea
        ref={ref}
        value={text}
        onChange={(e) => {
          setText(e.target.value);
          syncMenu(e.target.value, e.target.selectionStart, e.target.selectionEnd);
        }}
        onClick={(e) => syncMenu(e.currentTarget.value, e.currentTarget.selectionStart, e.currentTarget.selectionEnd)}
        onKeyUp={(e) => {
          if (["ArrowLeft", "ArrowRight", "Home", "End"].includes(e.key)) syncMenu(e.currentTarget.value, e.currentTarget.selectionStart, e.currentTarget.selectionEnd);
        }}
        onBlur={() => setTimeout(() => setMenu(null), 120)}
        rows={2}
        aria-label={t("chatMsg.editArea")}
        aria-haspopup={mention ? "listbox" : undefined}
        aria-controls={mention && menu ? listId : undefined}
        className="max-h-[360px] resize-none border-0 bg-transparent shadow-none focus-visible:ring-0"
        onKeyDown={(e) => {
          if (menu && menuRef.current?.handleKey(e)) return;
          if (e.key === "Escape") {
            e.preventDefault();
            if (menu) setMenu(null);
            else onCancel();
            return;
          }
          if (isComposingKey(e)) return;
          if (isSendKey(e, env.prefs.sendOn, apple)) {
            e.preventDefault();
            if (disabled) return;
            // The send shortcut regenerates when that is offered, else it saves.
            if (onRegenerate) onRegenerate(trimmed);
            else void save();
          }
        }}
      />
      {mention ? (
        <MentionMenu
          ref={menuRef}
          open={!!menu}
          trigger={menu?.trigger ?? "@"}
          query={menu?.query ?? ""}
          anchorRef={frame}
          kinds={menu?.trigger === "/" ? ["prompt"] : ["model"]}
          models={mention.models}
          selectedModel={mention.selectedModel}
          tools={[]}
          selectedToolIds={[]}
          selectedSkillIds={[]}
          selectedKbIds={[]}
          kbAvailable={false}
          onPick={pick}
          onClose={() => setMenu(null)}
          listId={listId}
        />
      ) : null}
      <div className="flex flex-wrap items-center justify-end gap-2">
        <Button variant="ghost" size="sm" onClick={onCancel}>
          {t("common.actions.cancel")}
        </Button>
        <Tooltip label={t("chatMsg.saveHint")}>
          <Button variant="outline" size="sm" disabled={disabled} loading={saving} onClick={() => void save()}>
            {t("common.actions.save")}
          </Button>
        </Tooltip>
        {onRegenerate ? (
          <Tooltip label={t("chatMsg.regenerateHint")}>
            <Button size="sm" disabled={disabled} onClick={() => onRegenerate(trimmed)}>
              {t("chatMsg.regenerate")}
            </Button>
          </Tooltip>
        ) : null}
      </div>
    </div>
  );
}
