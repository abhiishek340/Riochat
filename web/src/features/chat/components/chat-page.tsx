"use client";

import { Suspense, useCallback, useEffect, useMemo, useRef, useState, useSyncExternalStore, type DragEvent } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { ConfirmDialog, Spinner, toast } from "@/components/ui";
import { usePageTitle } from "@/components/shell/header-context";
import { useBranding } from "@/lib/branding/provider";
import { useFeaturePolicy } from "@/lib/session/feature-policy";
import { isAdminRole, useSession } from "@/lib/session/provider";
import { useI18n, useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useDraft } from "../hooks/use-draft";
import { removeMessage, useChatActions } from "../hooks/use-chat-actions";
import { useConversationActions } from "../hooks/use-conversation-actions";
import { useArtifactPanel } from "../hooks/use-artifact-panel";
import { useModelState } from "../hooks/use-model-state";
import { useSelections, useUiComponentChoice, useVisualPrompt } from "../hooks/use-selections";
import type { Currency } from "../lib/billing";
import { messageFileCap } from "../lib/file-policy";
import { CONTENT_WIDTH_REM } from "../lib/settings";
import { decideTask, requestedResponseType } from "../lib/task-decision";
import { lastPersistedLeaf } from "../lib/tree";
import { notificationBody, notifyDone } from "../lib/notify";
import { toolCap } from "../lib/selection";
import { capturable } from "../lib/screenshot";
import { useScreenshot } from "../hooks/use-screenshot";
import { chatEngine } from "../state/engine-instance";
import { ensureConversation, conversationStore, findConversation } from "../state/conversation-store";
import { DRAFT_KEY, TEMP_KEY, type ChatView, type Notice, type SendParams } from "../state/engine-types";
import { useNewChatRequest } from "../state/new-chat";
import {
  modelsResource,
  useChatPrefs,
  useFilePolicy,
  useMcpPolicy,
  useMcpTools,
  useModelsState,
  useOptionPolicy,
  useUiComponents,
  useBilling,
  settingsResource,
} from "../state/resources";
import { useStore } from "../state/store";
import { tempEngine } from "../state/temp-engine";
import type { ChatAttachment, ChatMessage } from "../types";
import { ArtifactPanel } from "./artifact-panel";
import { ChatHeader, type HeaderAction } from "./chat-header";
import { Composer } from "./composer/composer";
import type { ComposerApi, ComposerController } from "./composer/types";
import { DeleteConversationDialog, LabelsDialog, MoveDialog, RenameDialog, ShareDialog } from "./conversation-dialogs";
import { LandingHeading } from "./landing";
import { ExternalLinkDialog } from "./markdown/external-link-dialog";
import { SelectionBar, ScreenshotPreview } from "./screenshot";
import { MessageEnvContext, type MessageEnv, type SelectionMode } from "./message/env";
import { MessageList } from "./message-list";
import { SidePanelFrame } from "./side-panel";

export function ChatPage() {
  return (
    <Suspense fallback={<div className="flex h-full items-center justify-center"><Spinner className="size-5" /></div>}>
      <ChatPageInner />
    </Suspense>
  );
}

function useEngineView(key: string, temporary: boolean): ChatView {
  const subscribe = useCallback((cb: () => void) => (temporary ? tempEngine.subscribe(cb) : chatEngine.subscribe(key, cb)), [key, temporary]);
  const snapshot = useCallback(() => (temporary ? tempEngine.view() : chatEngine.view(key)), [key, temporary]);
  return useSyncExternalStore(subscribe, snapshot, snapshot);
}

function ChatPageInner() {
  const t = useT();
  const { locale } = useI18n();
  const router = useRouter();
  const params = useSearchParams();
  const errorMessage = useErrorMessage();
  const { user, accessToken } = useSession();
  const { branding } = useBranding();
  const { policy: featurePolicy } = useFeaturePolicy();
  const prefs = useChatPrefs();
  const request = useNewChatRequest();
  const list = useStore(conversationStore);

  const temporary = params.get("temporary") === "true";
  const urlConversation = temporary ? "" : (params.get("conversation_id") ?? "");
  const urlProject = temporary || urlConversation ? "" : (params.get("project_id") ?? "");
  const [localId, setLocalId] = useState("");
  const openId = urlConversation || localId;
  const chatKey = temporary ? TEMP_KEY : openId || DRAFT_KEY;
  const isDraft = chatKey === DRAFT_KEY;
  const userId = user?.publicID ?? "";
  const authed = accessToken !== "";

  // Navigation resets the locally created conversation id.
  useEffect(() => setLocalId(""), [urlConversation]);
  const revisionSeen = useRef(request.revision);
  useEffect(() => {
    if (revisionSeen.current === request.revision) return;
    revisionSeen.current = request.revision;
    setLocalId("");
    chatEngine.resetDraft();
  }, [request.revision]);

  // --- data ----------------------------------------------------------------------------------
  const conversation = openId ? (list.conversations.find((c) => c.publicID === openId) ?? null) : null;
  useEffect(() => {
    if (openId && !findConversation(openId)) void ensureConversation(openId);
  }, [openId]);
  const projectId = isDraft ? urlProject : (conversation?.projectID ?? "");
  const project = projectId ? (list.projects.find((p) => p.publicID === projectId) ?? null) : null;

  const modelsState = useModelsState();
  const optionPolicy = useOptionPolicy();
  const filePolicy = useFilePolicy();
  const mcp = useMcpTools();
  const mcpPolicy = useMcpPolicy();
  const components = useUiComponents();
  const billing = useBilling();
  useEffect(() => {
    void settingsResource.ensure();
  }, []);

  const currency: Currency = useMemo(() => ({ code: billing.displayCurrency, rate: billing.displayCurrency === "CNY" ? billing.usdToCNYRate : 1 }), [billing]);

  // --- the engine view -----------------------------------------------------------------------------
  const view = useEngineView(chatKey, temporary);

  useEffect(() => {
    if (temporary) {
      chatEngine.setVisible(null);
      return;
    }
    chatEngine.setVisible(chatKey);
    if (chatKey !== DRAFT_KEY) void chatEngine.load(chatKey, { reload: chatEngine.view(chatKey).loadedOnce });
    return () => chatEngine.setVisible(null);
  }, [chatKey, temporary]);

  // Leaving temporary mode discards it; so does closing the tab.
  useEffect(() => {
    if (!temporary) return;
    const hide = () => tempEngine.discard();
    window.addEventListener("pagehide", hide);
    return () => {
      window.removeEventListener("pagehide", hide);
      tempEngine.discard();
    };
  }, [temporary]);

  // --- composer state -------------------------------------------------------------------------------
  const draft = useDraft({ key: chatKey, userId, policy: filePolicy, preserve: prefs.preserveDrafts, authed });
  const modelState = useModelState({
    key: chatKey,
    conversation,
    project,
    models: modelsState.data,
    modelsLoaded: modelsState.loaded,
    policy: optionPolicy,
    userDefault: prefs.defaultModel,
    reuseOptions: prefs.reuseOptions,
    authed,
    newChatRevision: request.revision,
  });
  const cap = toolCap(mcpPolicy.maxSelectedToolsPerMessage);
  const selections = useSelections({
    key: chatKey,
    userId,
    project,
    newChatRevision: request.revision,
    tools: mcp.data,
    toolsLoaded: mcp.loaded,
    cap,
    userDefaultToolIds: prefs.defaultToolIds,
    kbEnabled: featurePolicy.knowledgeBaseEnabled,
    authed,
  });
  const uiChoice = useUiComponentChoice(components.data);
  const visualPrompt = useVisualPrompt();
  const selectionsRef = useRef(selections);
  selectionsRef.current = selections;
  const composerApi = useRef<ComposerApi | null>(null);
  const lastPrefill = useRef("");

  const decision = useMemo(
    () =>
      decideTask({
        model: modelState.facts,
        attachments: draft.attachments.map((a) => ({ fileCategory: a.fileCategory, mimeType: a.mimeType, detectedMime: a.detectedMime })),
        requested: requestedResponseType(modelState.options),
      }),
    [modelState.facts, modelState.options, draft.attachments],
  );

  // A draft chat that gets its conversation moves its address and selection along.
  useEffect(
    () =>
      chatEngine.on("conversationCreated", ({ fromKey, conversation: created }) => {
        if (fromKey !== DRAFT_KEY) return;
        selectionsRef.current.adopt(created.publicID);
        setLocalId(created.publicID);
        window.history.replaceState(window.history.state, "", `/chat?conversation_id=${encodeURIComponent(created.publicID)}`);
      }),
    [],
  );

  // Notices become toasts.
  useEffect(() => {
    const show = (n: Notice) => {
      switch (n.code) {
        case "concurrency":
          toast.warning(t("chatMisc.toast.concurrency", { count: n.count ?? 5 }));
          break;
        case "noModel":
          toast.error(t("chatMisc.toast.noModel"), { description: t("chatMisc.toast.noModelDescription") });
          break;
        case "blocked":
          toast.error(t("chatComposer.blockedTitle"), { description: t(`chatComposer.blocked.${n.block ?? "B9"}` as "chatComposer.blocked.B1") });
          break;
        case "attachmentsTruncated":
          toast.warning(t("chatComposer.attach.truncated", { count: n.count ?? 0 }));
          break;
        case "createFailed":
          toast.error(t("chatMisc.toast.createFailed"), { description: errorMessage(n.error) });
          break;
        case "sendFailed":
          toast.error(t("chatMisc.toast.sendFailed"), { description: n.summary || errorMessage(n.error) });
          break;
        case "queuedCanceled":
          toast.warning(t("chatMisc.toast.queuedCanceled"), { description: t("chatMisc.toast.queuedCanceledDescription") });
          break;
        case "moderation":
          toast.error(t("chatMsg.alert.blockedTitle"));
          break;
        default:
          break;
      }
    };
    const off = chatEngine.on("notice", show);
    const offTemp = tempEngine.onNotice(show);
    return () => {
      off();
      offTemp();
    };
  }, [t, errorMessage]);

  // Completion notifications while the page is not in front.
  useEffect(
    () =>
      chatEngine.on("completed", ({ conversationId, text, title }) => {
        notifyDone({
          title: title || branding.title,
          body: notificationBody(text, t("chatMisc.notify.ready")),
          tag: conversationId,
          icon: branding.pwaIcon192URL || undefined,
        });
      }),
    [t, branding.title, branding.pwaIcon192URL],
  );

  // --- sending ----------------------------------------------------------------------------------------
  const leaf = lastPersistedLeaf(view.path);
  const base = useCallback(
    (): Omit<SendParams, "text" | "attachments" | "parentId" | "sourceId" | "branchReason" | "assistantOnly" | "answeringUserId" | "fromComposer" | "restore" | "runId"> => ({
      key: chatKey,
      projectId: isDraft ? projectId || null : null,
      model: modelState.model?.platformModelName ?? "",
      modelFacts: modelState.facts,
      options: modelState.options,
      optionPolicyMode: optionPolicy?.mode,
      controls: modelState.requestControls,
      selections: {
        toolIds: selections.toolIds,
        skillIds: selections.skills.map((s) => s.id),
        knowledgeBaseIds: selections.kbIds,
        uiComponentIds: uiChoice.effective,
        visualPrompt: visualPrompt[0],
      },
      attachmentOnlyText: t("chatMisc.attachmentOnly"),
      maxMessageFiles: messageFileCap(filePolicy),
      autoGenerateLabels: prefs.autoGenerateLabels,
      restoreOnFailure: prefs.restoreDraftOnFailure,
    }),
    [chatKey, isDraft, projectId, modelState, optionPolicy?.mode, selections, uiChoice.effective, visualPrompt, t, filePolicy, prefs.autoGenerateLabels, prefs.restoreDraftOnFailure],
  );

  const tempSend = useCallback(
    (text: string, attachments: ChatAttachment[], restore?: () => boolean) => {
      const b = base();
      void tempEngine.send({
        text,
        attachments,
        model: b.model,
        modelFacts: b.modelFacts,
        options: b.options,
        optionPolicyMode: b.optionPolicyMode,
        controls: b.controls,
        selections: b.selections,
        attachmentOnlyText: b.attachmentOnlyText,
        omittedNote: t("chatMisc.temp.historyOmitted"),
        attachmentNote: (names) => t("chatMisc.temp.attachmentsOmitted", { names: names.join(", ") }),
        sessionExpiredText: t("chatMisc.temp.sessionExpired"),
        stoppedText: t("chatMisc.temp.stopped"),
        restore,
      });
    },
    [base, t],
  );

  const submit = useCallback(() => {
    if (draft.busy) return;
    const taken = draft.take();
    if (!taken.text.trim() && taken.attachments.length === 0) return;
    const sendKey = chatKey;
    const restore = () => {
      if (sendKey !== chatKeyRef.current) {
        return false;
      }
      draft.restore(taken);
      return true;
    };
    if (temporary) {
      if (tempEngine.running) {
        draft.restore(taken);
        return;
      }
      tempSend(taken.text, taken.attachments, restore);
      return;
    }
    const send: SendParams = {
      ...base(),
      text: taken.text,
      attachments: taken.attachments,
      parentId: leaf?.id ?? null,
      sourceId: null,
      branchReason: "default",
      fromComposer: true,
      restore,
    };
    if (chatEngine.shouldQueue(chatKey)) {
      if (!chatEngine.enqueue(send)) draft.restore(taken);
      return;
    }
    void chatEngine.send(send).then((res) => {
      if (res.status === "not_sent" && res.reason !== "empty") draft.restore(taken);
    });
  }, [base, chatKey, draft, leaf?.id, tempSend, temporary]);
  const chatKeyRef = useRef(chatKey);
  chatKeyRef.current = chatKey;

  const onStop = useCallback(() => {
    if (temporary) tempEngine.stop();
    else void chatEngine.stop(chatKey);
  }, [chatKey, temporary]);

  // --- queue ---------------------------------------------------------------------------------------------
  const queueAll = useStore(chatEngine.queueStore);
  const queued = useMemo(() => queueAll.filter((q) => q.conversationKey === chatKey), [queueAll, chatKey]);

  // --- panels, dialogs ---------------------------------------------------------------------------------------
  const artifactPanel = useArtifactPanel(view.path, chatKey);
  const [toDelete, setToDelete] = useState<ChatMessage | null>(null);
  const [dialog, setDialog] = useState<null | "rename" | "labels" | "move" | "delete" | "share">(null);
  const [exitTemp, setExitTemp] = useState<null | (() => void)>(null);
  const [selectionMode, setSelectionMode] = useState(false);
  const [selected, setSelected] = useState<ReadonlySet<string>>(new Set());
  const workspace = useRef<HTMLDivElement>(null);
  const scroller = useRef<HTMLDivElement>(null);

  const convActions = useConversationActions((id) => {
    if (id === openId) router.push("/chat");
  });

  const actions = useChatActions({
    chatKey,
    temporary,
    view,
    models: modelsState.data,
    policy: optionPolicy,
    draft,
    model: modelState,
    base,
    tempSend: (text, atts) => tempSend(text, atts),
    continueText: t("chatMisc.continueInstruction"),
    openCode: (m, args) => artifactPanel.openCode(m, args),
    openBlock: () => undefined,
    requestDelete: setToDelete,
    composerInsert: (text) => composerApi.current?.insert(text),
    lastPrefill,
    forkSuffix: t("chatMisc.forkSuffix"),
  });

  // --- selection mode (screenshots) -------------------------------------------------------------------------
  const selection: SelectionMode = useMemo(
    () => ({
      active: selectionMode,
      selected,
      toggle: (id) => {
        // The warning is raised here, not inside the state updater (which may run twice).
        if (!selected.has(id) && selected.size >= 100) {
          toast.warning(t("chatNav.screenshot.limit"));
          return;
        }
        setSelected((cur) => {
          const next = new Set(cur);
          if (next.has(id)) next.delete(id);
          else next.add(id);
          return next;
        });
      },
    }),
    [selectionMode, selected, t],
  );
  useEffect(() => {
    setSelectionMode(false);
    setSelected(new Set());
  }, [chatKey]);
  // Selected messages that disappeared are dropped; no messages ends the mode.
  useEffect(() => {
    if (!selectionMode) return;
    const ids = new Set(view.path.map((m) => m.id));
    setSelected((cur) => (Array.from(cur).every((id) => ids.has(id)) ? cur : new Set(Array.from(cur).filter((id) => ids.has(id)))));
    if (view.path.length === 0) setSelectionMode(false);
  }, [view.path, selectionMode]);

  const shot = useScreenshot(branding.title);
  const shotRows = useMemo(() => capturable(view.path), [view.path]);
  useEffect(() => shot.abort, [chatKey, shot.abort]);

  const env: MessageEnv = useMemo(
    () => ({
      conversationKey: chatKey,
      temporary,
      readOnly: false,
      prefs,
      currency,
      billingMode: billing.mode,
      busy: view.busy,
      locale,
      actions,
      selection,
      hasRememberApi: true,
      mention: { models: modelsState.data, selectedModel: modelState.model?.platformModelName ?? "", selectModel: modelState.select },
    }),
    [chatKey, temporary, prefs, currency, billing.mode, view.busy, locale, actions, selection, modelsState.data, modelState.model?.platformModelName, modelState.select],
  );

  // --- header ---------------------------------------------------------------------------------------------------
  const title = temporary ? t("chatMisc.temp.title") : openId ? conversation?.title || t("chatNav.header.untitled") : project ? project.name : null;
  usePageTitle(title);

  const toggleTemporary = () => {
    const go = () => router.push(temporary ? "/chat" : "/chat?temporary=true");
    if (temporary && (tempEngine.running || tempEngine.hasMessages)) setExitTemp(() => go);
    else go();
  };

  const onHeaderAction = (a: HeaderAction) => {
    if (!conversation) return;
    if (a === "star") void convActions.toggleStar(conversation);
    else if (a === "autoRename") void convActions.autoRename(conversation);
    else if (a === "export") void convActions.exportJson(conversation);
    else if (a === "captureLatest") void shot.capture(new Set(shotRows));
    else if (a === "captureSelect") {
      setSelected(new Set());
      setSelectionMode(true);
    } else setDialog(a);
  };

  // --- layout ------------------------------------------------------------------------------------------------------
  const landing = !view.hasContent && !view.loading && !view.loadError;
  const [dragging, setDragging] = useState(false);
  const dragDepth = useRef(0);
  const dropDisabled = (isDraft ? false : view.loading) || draft.busy;
  const onDragEnter = (e: DragEvent) => {
    if (!e.dataTransfer?.types.includes("Files")) return;
    dragDepth.current++;
    setDragging(true);
  };
  const onDragLeave = () => {
    dragDepth.current = Math.max(0, dragDepth.current - 1);
    if (dragDepth.current === 0) setDragging(false);
  };

  const composer: ComposerController = {
    chatKey,
    temporary,
    conversationLoading: view.loading,
    draft,
    model: modelState,
    models: modelsState.data,
    modelsLoading: !modelsState.loaded,
    policy: optionPolicy,
    selections,
    tools: mcp.data,
    toolsLoaded: mcp.loaded,
    uiComponents: components.data,
    uiChoice,
    visualPrompt,
    prefs,
    filePolicy,
    kbEnabled: featurePolicy.knowledgeBaseEnabled,
    isAdmin: isAdminRole(user?.role),
    currency,
    locale,
    decision,
    busy: view.busy,
    queued,
    landing,
    dragging,
    onSubmit: submit,
    onStop,
    onEditQueued: (id, text) => chatEngine.editQueued(id, text),
    onRemoveQueued: (id) => chatEngine.removeQueued(id),
    onGuideQueued: (id) => chatEngine.guideQueued(id),
    pickerPlacement: landing ? "bottom" : "top",
    registerApi: (api) => {
      composerApi.current = api;
    },
  };

  const contentRem = CONTENT_WIDTH_REM[prefs.contentWidth];
  const loadErrorText = !authed ? t("chatMsg.loadError.signIn") : t("chatMsg.loadError.message");
  const modelsError = modelsState.loaded && modelsState.data.length === 0 && modelsResource.store.get().error !== null;

  return (
    <MessageEnvContext.Provider value={env}>
      <ChatHeader
        conversation={temporary ? null : conversation}
        temporary={temporary}
        canToggleTemporary={temporary || !openId}
        onToggleTemporary={toggleTemporary}
        onAction={onHeaderAction}
        projectName={conversation?.projectName || project?.name || ""}
      />
      <div ref={workspace} className="relative flex h-full min-h-0 w-full">
        <div
          className="relative flex min-h-0 min-w-0 flex-1 flex-col"
          onDragEnter={onDragEnter}
          onDragLeave={onDragLeave}
          onDragOver={(e) => {
            if (e.dataTransfer?.types.includes("Files")) e.preventDefault();
          }}
          onDrop={(e) => {
            if (!e.dataTransfer?.files.length) return;
            e.preventDefault();
            dragDepth.current = 0;
            setDragging(false);
            if (dropDisabled) return;
            void draft.addFiles(Array.from(e.dataTransfer.files));
          }}
        >
          {landing ? null : (
            <MessageList
              view={view}
              onLoadOlder={() => void chatEngine.loadOlder(chatKey)}
              onReload={() => void chatEngine.load(chatKey)}
              onNewChat={() => router.push(projectId ? `/chat?project_id=${encodeURIComponent(projectId)}` : "/chat")}
              contentMaxRem={contentRem}
              panelOpen={artifactPanel.open}
              scrollerRef={scroller}
              selectedIds={selected}
              loadErrorText={loadErrorText}
              footer={
                modelsError ? (
                  <div role="alert" className="my-2 rounded-xl border bg-muted/40 px-3 py-2 text-sm text-muted-foreground">
                    {t("chatMisc.modelsLoadFailed", { error: errorMessage(modelsResource.store.get().error) })}
                  </div>
                ) : null
              }
            />
          )}
          {selectionMode ? (
            <div className="px-4">
              <SelectionBar
                count={selected.size}
                busy={shot.busy}
                onSelectAll={() => setSelected(new Set(shotRows))}
                onSelectLatest={() => setSelected(new Set(shotRows))}
                onClear={() => setSelected(new Set())}
                onCapture={() => void shot.capture(selected)}
                onExit={() => {
                  setSelectionMode(false);
                  setSelected(new Set());
                }}
              />
            </div>
          ) : null}
          {view.loadError && view.path.length === 0 ? null : (
            <div className={landing ? "m-auto flex w-full flex-col px-4 pb-10" : "w-full shrink-0 px-4 pb-4"} style={{ maxWidth: `${contentRem}rem`, marginInline: "auto" }}>
              {landing ? <LandingHeading project={project} temporary={temporary} /> : null}
              <Composer c={composer} />
            </div>
          )}
          {dragging ? (
            <div className="pointer-events-none absolute inset-2 z-20 flex items-center justify-center rounded-2xl border-2 border-dashed border-primary bg-primary/5 text-sm font-medium text-primary">{t("chatComposer.attach.dropHint")}</div>
          ) : null}
        </div>
        {artifactPanel.open ? (
          <SidePanelFrame containerRef={workspace}>
            <ArtifactPanel artifacts={artifactPanel.artifacts} selected={artifactPanel.selected} onSelect={artifactPanel.select} onClose={artifactPanel.close} />
          </SidePanelFrame>
        ) : null}
      </div>

      <ExternalLinkDialog />
      <ConfirmDialog
        open={toDelete !== null}
        onOpenChange={(o) => !o && setToDelete(null)}
        title={t("chatMsg.deleteDialog.title")}
        description={t("chatMsg.deleteDialog.description")}
        confirmLabel={t("chatMsg.deleteDialog.confirm")}
        destructive
        onConfirm={async () => {
          const m = toDelete;
          if (!m) return;
          if (temporary) return;
          try {
            await removeMessage(chatKey, m);
          } catch (error) {
            toast.error(t("chatMsg.toast.deleteFailed"), { description: errorMessage(error) });
            throw error;
          }
        }}
      />
      <ConfirmDialog
        open={exitTemp !== null}
        onOpenChange={(o) => !o && setExitTemp(null)}
        title={t("chatMisc.temp.exitTitle")}
        description={t("chatMisc.temp.exitDescription")}
        confirmLabel={t("chatMisc.temp.exitConfirm")}
        destructive
        onConfirm={() => {
          const go = exitTemp;
          setExitTemp(null);
          go?.();
        }}
      />
      <ScreenshotPreview result={shot.result} title={title ?? ""} onClose={shot.release} />
      <RenameDialog conversation={conversation} open={dialog === "rename"} onOpenChange={(o) => !o && setDialog(null)} onSave={(title) => (conversation ? convActions.rename(conversation, title) : Promise.resolve(false))} />
      <LabelsDialog conversation={conversation} open={dialog === "labels"} onOpenChange={(o) => !o && setDialog(null)} onSave={(labels) => (conversation ? convActions.setLabels(conversation, labels) : Promise.resolve(false))} />
      <MoveDialog conversation={conversation} projects={list.projects} open={dialog === "move"} onOpenChange={(o) => !o && setDialog(null)} onMove={(id, name) => (conversation ? convActions.moveToProject(conversation, id, name) : Promise.resolve(false))} />
      <DeleteConversationDialog conversation={conversation} open={dialog === "delete"} onOpenChange={(o) => !o && setDialog(null)} defaultDeleteFiles={prefs.deleteFilesByDefault} onDelete={(files) => (conversation ? convActions.remove(conversation, files) : Promise.resolve())} />
      <ShareDialog conversation={conversation} open={dialog === "share"} onOpenChange={(o) => !o && setDialog(null)} defaultMessageIds={view.path.filter((m) => m.persisted && m.status !== "pending").map((m) => m.id)} />
    </MessageEnvContext.Provider>
  );
}
