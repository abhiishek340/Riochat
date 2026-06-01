// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { ReactElement } from "react";
import { toast } from "@/components/ui";
import { TooltipProvider } from "@/components/ui/tooltip";
import { I18nProvider } from "@/lib/i18n/provider";
import { msg } from "../../lib/fixtures";
import { DEFAULT_PREFS } from "../../lib/settings";
import { AssistantMessage } from "./assistant-message";
import { MessageEnvContext, type MessageActions, type MessageEnv } from "./env";
import { UserMessage, EditBox } from "./user-message";

afterEach(cleanup);

function actions(over: Partial<MessageActions> = {}): MessageActions {
  const fn = vi.fn();
  return {
    retry: fn, continueReply: fn, saveEdit: vi.fn(async () => true), regenerate: fn, remove: fn, fork: fn, feedback: fn, cycle: fn, openCode: fn, openBlock: fn,
    editImage: fn, imageEditable: () => false, extendVideo: fn, canExtendVideo: () => false, rememberPreference: vi.fn(async () => true), loadFullTrace: fn, ask: fn, prefill: fn,
    ...over,
  };
}

function env(over: Partial<MessageEnv> = {}): MessageEnv {
  return {
    conversationKey: "c1", temporary: false, readOnly: false, prefs: DEFAULT_PREFS, currency: { code: "USD", rate: 1 }, billingMode: "usage", busy: false, locale: "en-US",
    actions: actions(), selection: { active: false, selected: new Set(), toggle: vi.fn() }, hasRememberApi: true, ...over,
  };
}

function view(ui: ReactElement, e: MessageEnv = env()) {
  return render(
    <I18nProvider>
      <TooltipProvider>
        <MessageEnvContext.Provider value={e}>{ui}</MessageEnvContext.Provider>
      </TooltipProvider>
    </I18nProvider>,
  );
}

describe("UserMessage actions", () => {
  it("keeps retry on while a reply runs and exposes the expand state", async () => {
    const e = env({ busy: true });
    view(<UserMessage message={msg({ id: "u9", role: "user", content: "again", persisted: true, parentId: "p" } as never)} />, e);
    const retry = (await screen.findByRole("button", { name: /try again/i })) as HTMLButtonElement;
    expect(retry.disabled).toBe(false);
  });
});

describe("UserMessage", () => {
  it("shows the text of the turn", () => {
    view(<UserMessage message={msg({ id: "u1", role: "user", content: "What is 2+2?" })} />);
    expect(screen.getByText("What is 2+2?")).toBeTruthy();
  });

  it("cycles branches through the navigator", async () => {
    const e = env();
    view(<UserMessage message={msg({ id: "u1", role: "user", content: "q" })} nav={{ index: 2, total: 3, canPrevious: true, canNext: true } as never} />, e);
    fireEvent.click(await screen.findByRole("button", { name: /previous/i }));
    expect(e.actions.cycle).toHaveBeenCalledWith(expect.objectContaining({ id: "u1" }), -1);
  });
});

describe("EditBox", () => {
  it("keeps Save disabled until the text changes and is not empty", async () => {
    const user = userEvent.setup();
    const onSave = vi.fn();
    view(<EditBox initial="hello" onCancel={() => undefined} onSave={onSave} />);
    const save = await screen.findByRole("button", { name: "Save" });
    expect((save as HTMLButtonElement).disabled).toBe(true);
    const area = screen.getByRole("textbox");
    await user.type(area, " there");
    expect((save as HTMLButtonElement).disabled).toBe(false);
    await user.click(save);
    expect(onSave).toHaveBeenCalledWith("hello there");
    await user.clear(area);
    expect((save as HTMLButtonElement).disabled).toBe(true);
  });

  it("cancels on Escape", async () => {
    const user = userEvent.setup();
    const onCancel = vi.fn();
    view(<EditBox initial="x" onCancel={onCancel} onSave={() => undefined} />);
    await user.type(await screen.findByRole("textbox"), "{Escape}");
    expect(onCancel).toHaveBeenCalled();
  });

  it("regenerates with the trimmed text", async () => {
    const user = userEvent.setup();
    const onRegenerate = vi.fn();
    view(<EditBox initial="a" onCancel={() => undefined} onSave={() => undefined} onRegenerate={onRegenerate} />);
    await user.type(await screen.findByRole("textbox"), "b  ");
    await user.click(screen.getByRole("button", { name: "Generate" }));
    expect(onRegenerate).toHaveBeenCalledWith("ab");
  });
});

describe("EditBox shortcuts and menus", () => {
  it("saves with the send shortcut when no regenerate is offered", async () => {
    const user = userEvent.setup();
    const onSave = vi.fn();
    view(<EditBox initial="a" onCancel={() => undefined} onSave={onSave} />);
    await user.type(await screen.findByRole("textbox"), "b{Enter}");
    await waitFor(() => expect(onSave).toHaveBeenCalledWith("ab"));
  });

  it("does not submit while an IME composition is active", async () => {
    const onSave = vi.fn();
    view(<EditBox initial="a" onCancel={() => undefined} onSave={onSave} />);
    const area = await screen.findByRole("textbox");
    fireEvent.change(area, { target: { value: "ab" } });
    fireEvent.keyDown(area, { key: "Enter", isComposing: true });
    fireEvent.keyDown(area, { key: "Process", keyCode: 229 });
    expect(onSave).not.toHaveBeenCalled();
  });

  it("offers the model menu for @ while editing, and nothing for plain text", async () => {
    const user = userEvent.setup();
    const select = vi.fn();
    const models = [{ platformModelName: "alpha-1", vendor: "v", vendorName: "Vendor", vendorIcon: "", displayGroupID: null, displayGroupName: "", displayGroupIcon: "", kindsJSON: "[]", icon: "", protocolsJSON: "[]", capabilitiesJSON: "{}", description: "", sortOrder: 0, pricing: null, reasoning: null, controls: null, inputModalities: null, outputModalities: null, contextWindow: null, source: "", providerName: "", providerIcon: "" }];
    view(<EditBox initial="see " onCancel={() => undefined} onSave={() => undefined} onRegenerate={() => undefined} mentions />, env({ mention: { models, selectedModel: "", selectModel: select } }));
    const area = await screen.findByRole("textbox");
    await user.type(area, "plain");
    expect(screen.queryByRole("listbox")).toBeNull();
    await user.type(area, " @al");
    const option = await screen.findByRole("option", { name: /alpha/i });
    await user.click(option);
    expect(select).toHaveBeenCalledWith("alpha-1");
    expect((area as HTMLTextAreaElement).value).toBe("see plain ");
  });
});

describe("AssistantMessage", () => {
  const done = msg({ id: "a1", role: "assistant", content: "The answer is **4**.", status: "success", model: "m", latencyMs: 1200 });

  it("renders markdown and the action row", async () => {
    const { container } = view(<AssistantMessage message={done} />);
    expect(container.querySelector("strong")?.textContent).toBe("4");
    expect(await screen.findByRole("button", { name: /try again/i })).toBeTruthy();
  });

  it("retries and gives feedback through the env actions", async () => {
    const e = env();
    view(<AssistantMessage message={done} />, e);
    fireEvent.click(await screen.findByRole("button", { name: /try again/i }));
    expect(e.actions.retry).toHaveBeenCalledWith(expect.objectContaining({ id: "a1" }));
    fireEvent.click(screen.getByRole("button", { name: /good reply/i }));
    expect(e.actions.feedback).toHaveBeenCalledWith(expect.objectContaining({ id: "a1" }), "up");
  });

  it("keeps retry available while another reply runs, but not editing", async () => {
    const e = env({ busy: true });
    view(<AssistantMessage message={done} />, e);
    const retry = (await screen.findByRole("button", { name: /try again/i })) as HTMLButtonElement;
    expect(retry.disabled).toBe(false);
    fireEvent.click(retry);
    expect(e.actions.retry).toHaveBeenCalled();
    expect((screen.getByRole("button", { name: "Edit" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("copies interactive blocks as their current state", async () => {
    const writeText = vi.fn(async () => undefined);
    Object.defineProperty(navigator, "clipboard", { value: { writeText }, configurable: true });
    const content = 'Intro\n```riochat-ui\n{"component":"slider","id":"s1","title":"Budget","state":{"amount":10}}\n```';
    view(<AssistantMessage message={msg({ id: "a3", role: "assistant", content, status: "success", model: "m" })} />);
    fireEvent.click(await screen.findByRole("button", { name: "Copy" }));
    await waitFor(() => expect(writeText).toHaveBeenCalled());
    expect(writeText).toHaveBeenCalledWith("Intro\nBudget\namount: 10");
  });

  it("toasts when copying fails", async () => {
    Object.defineProperty(navigator, "clipboard", { value: { writeText: vi.fn(async () => Promise.reject(new Error("no"))) }, configurable: true });
    const exec = vi.fn(() => false);
    (document as unknown as { execCommand: typeof exec }).execCommand = exec;
    const error = vi.spyOn(toast, "error").mockImplementation(() => "t");
    view(<AssistantMessage message={done} />);
    fireEvent.click(await screen.findByRole("button", { name: "Copy" }));
    await waitFor(() => expect(error).toHaveBeenCalled());
    error.mockRestore();
  });

  it("hides the editing actions in a read-only view", async () => {
    view(<AssistantMessage message={done} />, env({ readOnly: true }));
    await waitFor(() => expect(screen.queryByRole("button", { name: /try again/i })).toBeNull());
  });

  it("shows an alert card for failed replies", async () => {
    const failed = msg({ id: "a2", role: "assistant", content: "", status: "blocked", alert: { kind: "blocked", title: "blocked", lines: [{ key: "description", value: "Not allowed" }] } });
    view(<AssistantMessage message={failed} />);
    expect(await screen.findByRole("alert")).toBeTruthy();
  });
});
