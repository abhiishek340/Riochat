// @vitest-environment jsdom
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeAll, describe, expect, it, vi } from "vitest";
import { createRef, type ReactElement } from "react";
import { TooltipProvider } from "@/components/ui/tooltip";
import { I18nProvider } from "@/lib/i18n/provider";
import { msg } from "../lib/fixtures";
import { DEFAULT_PREFS } from "../lib/settings";
import type { ChatView } from "../state/engine-types";
import { MessageEnvContext, type MessageActions, type MessageEnv } from "./message/env";
import { AlertCard } from "./message/alert-card";
import { MessageList } from "./message-list";
import { QueuedList } from "./composer/queued-list";
import { EXIT_DELAY_MS, ScreenshotPreview, SelectionBar } from "./screenshot";
import { SidePanelFrame } from "./side-panel";

beforeAll(() => {
  Object.defineProperty(window, "matchMedia", {
    configurable: true,
    value: (query: string) => ({ matches: false, media: query, addEventListener: () => undefined, removeEventListener: () => undefined, addListener: () => undefined, removeListener: () => undefined, onchange: null, dispatchEvent: () => false }),
  });
  if (!("ResizeObserver" in globalThis)) {
    (globalThis as unknown as { ResizeObserver: unknown }).ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    };
  }
  Element.prototype.scrollTo = (() => undefined) as never;
});

afterEach(() => {
  cleanup();
  vi.useRealTimers();
});

function wrap(ui: ReactElement, env?: MessageEnv) {
  const inner = env ? <MessageEnvContext.Provider value={env}>{ui}</MessageEnvContext.Provider> : ui;
  return render(
    <I18nProvider>
      <TooltipProvider>{inner}</TooltipProvider>
    </I18nProvider>,
  );
}

function env(): MessageEnv {
  const fn = vi.fn();
  const actions = { retry: fn, continueReply: fn, saveEdit: fn, regenerate: fn, remove: fn, fork: fn, feedback: fn, cycle: fn, openCode: fn, openBlock: fn, editImage: fn, imageEditable: () => false, extendVideo: fn, canExtendVideo: () => false, rememberPreference: fn, loadFullTrace: fn, ask: fn, prefill: fn } as unknown as MessageActions;
  return { conversationKey: "c", temporary: false, readOnly: false, prefs: DEFAULT_PREFS, currency: { code: "USD", rate: 1 }, billingMode: "usage", busy: false, locale: "en-US", actions, selection: { active: false, selected: new Set(), toggle: fn }, hasRememberApi: true };
}

function chatView(over: Partial<ChatView> = {}): ChatView {
  return { key: "c", messages: [], path: [], navigators: new Map(), selection: {}, loading: false, loadError: null, loadedOnce: true, hasOlder: false, loadingOlder: false, busy: false, streamingHere: false, hasContent: true, runIds: [], ...over };
}

function list(view: ChatView) {
  return (
    <MessageList view={view} onLoadOlder={() => undefined} onReload={() => undefined} onNewChat={() => undefined} contentMaxRem={56} panelOpen={false} scrollerRef={createRef<HTMLDivElement>()} selectedIds={new Set()} loadErrorText="x" />
  );
}

describe("MessageList semantics", () => {
  it("labels the loading skeleton as a status", async () => {
    wrap(list(chatView({ loading: true })), env());
    expect(await screen.findByRole("status", { name: /loading/i })).toBeTruthy();
  });

  it("is a labelled log that is not read out while streaming", async () => {
    const u = msg({ id: "u", role: "user", content: "hi" });
    wrap(list(chatView({ path: [u], messages: [u] })), env());
    const log = await screen.findByRole("log", { name: /conversation messages/i });
    expect(log.getAttribute("aria-live")).toBe("off");
  });

  it("announces the start and the successful end of a reply", async () => {
    const u = msg({ id: "u", role: "user", content: "hi" });
    const pending = msg({ id: "a", role: "assistant", status: "pending", content: "" });
    const done = msg({ id: "a", role: "assistant", status: "success", content: "ok" });
    const e = env();
    const { rerender } = wrap(list(chatView({ path: [u], messages: [u] })), e);
    const again = (v: ChatView) =>
      rerender(
        <I18nProvider>
          <TooltipProvider>
            <MessageEnvContext.Provider value={e}>{list(v)}</MessageEnvContext.Provider>
          </TooltipProvider>
        </I18nProvider>,
      );
    again(chatView({ path: [u, pending], messages: [u, pending], busy: true }));
    const status = screen.getAllByRole("status").find((n) => n.classList.contains("sr-only"));
    expect(status?.textContent).toMatch(/thinking/i);
    again(chatView({ path: [u, done], messages: [u, done], busy: false }));
    expect(screen.getAllByRole("status").find((n) => n.classList.contains("sr-only"))?.textContent).toMatch(/ready/i);
  });
});

describe("AlertCard", () => {
  it("shows the app's text for an error code instead of the raw server message", async () => {
    wrap(<AlertCard alert={{ kind: "interrupted", title: "interrupted", lines: [], message: "upstream rate limited", errorCode: "upstream.rate_limited" }} />);
    expect(await screen.findByText(/model service is busy/i)).toBeTruthy();
    expect(screen.queryByText("upstream rate limited")).toBeNull();
  });

  it("falls back to the raw message for codes without a text", async () => {
    wrap(<AlertCard alert={{ kind: "interrupted", title: "interrupted", lines: [], message: "something odd", errorCode: "x.unknown_code" }} />);
    expect(await screen.findByText("something odd")).toBeTruthy();
  });

  it("has app text for conversation codes", async () => {
    wrap(<AlertCard alert={{ kind: "interrupted", title: "interrupted", lines: [], message: "generation stream was interrupted; retry this message", errorCode: "conversation_run.stream_interrupted" }} />);
    expect(await screen.findByText(/connection was interrupted/i)).toBeTruthy();
  });
});

describe("QueuedList editing", () => {
  const item = { id: "q1", runId: "run_1", conversationKey: "c", text: "hello", attachments: [], model: "m", options: {}, selections: { toolIds: [], skillIds: [], knowledgeBaseIds: [], uiComponentIds: [], visualPrompt: false }, parentRunId: null, parentMessageId: null, createdAt: 0 };

  it("saves on Enter and does not save on Escape", async () => {
    const user = userEvent.setup();
    const onEdit = vi.fn();
    wrap(<QueuedList items={[item]} onEdit={onEdit} onRemove={() => undefined} onGuide={() => undefined} />);
    await user.click(await screen.findByRole("button", { name: /edit/i }));
    const field = screen.getByRole("textbox");
    await user.clear(field);
    await user.type(field, "changed{Escape}");
    expect(onEdit).not.toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: /edit/i }));
    const again = screen.getByRole("textbox");
    await user.clear(again);
    await user.type(again, "changed{Enter}");
    expect(onEdit).toHaveBeenCalledWith("q1", "changed");
  });
});

describe("ScreenshotPreview", () => {
  it("releases the picture only after the exit delay", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    const onClose = vi.fn();
    const blob = new Blob(["x"], { type: "image/png" });
    wrap(<ScreenshotPreview result={{ url: "blob:x", blob }} title="T" onClose={onClose} />);
    fireEvent.click((await screen.findAllByRole("button", { name: /close/i }))[0] as HTMLElement);
    expect(onClose).not.toHaveBeenCalled();
    act(() => {
      vi.advanceTimersByTime(EXIT_DELAY_MS - 20);
    });
    expect(onClose).not.toHaveBeenCalled();
    act(() => {
      vi.advanceTimersByTime(40);
    });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("names the selection toolbar", async () => {
    wrap(<SelectionBar count={0} busy={false} onSelectAll={() => undefined} onSelectLatest={() => undefined} onClear={() => undefined} onCapture={() => undefined} onExit={() => undefined} />);
    expect(await screen.findByRole("toolbar", { name: /select messages/i })).toBeTruthy();
  });
});

describe("SidePanelFrame", () => {
  it("reports the panel width on the divider", async () => {
    const container = createRef<HTMLDivElement>();
    wrap(
      <div ref={container}>
        <SidePanelFrame containerRef={container}>
          <p>panel</p>
        </SidePanelFrame>
      </div>,
    );
    const sep = await screen.findByRole("separator");
    expect(Number(sep.getAttribute("aria-valuenow"))).toBeGreaterThanOrEqual(33);
    expect(Number(sep.getAttribute("aria-valuenow"))).toBeLessThanOrEqual(75);
    expect(sep.getAttribute("aria-valuemin")).toBe("33");
    expect(sep.getAttribute("aria-valuemax")).toBe("75");
  });
});
