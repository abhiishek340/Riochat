// @vitest-environment jsdom
import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { clearToasts, renderWithApp, toastTitles } from "../test-utils";

const mocks = vi.hoisted(() => ({
  settings: { values: {} as Record<string, string>, loaded: true, failed: false },
  reload: vi.fn(),
  update: vi.fn(),
  models: vi.fn(),
  mode: vi.fn(),
  compact: vi.fn(),
  schedule: vi.fn(),
  policy: { processTraceEnabled: true },
}));
vi.mock("../lib/use-user-settings", () => ({
  useUserSettings: () => ({ values: mocks.settings.values, loaded: mocks.settings.loaded, failed: mocks.settings.failed, update: mocks.update, reload: mocks.reload }),
}));
vi.mock("./api", async (orig) => ({ ...(await orig<object>()), listChatModels: mocks.models, fetchBillingMode: mocks.mode, fetchCompactPolicy: mocks.compact }));
vi.mock("@/lib/session/feature-policy", () => ({ useFeaturePolicy: () => ({ policy: mocks.policy, loaded: true, patch: vi.fn() }) }));
vi.mock("@/components/shell/header-context", () => ({ usePageTitle: () => undefined }));
vi.mock("../memory/preference-memory", () => ({ PreferenceMemory: () => null }));
vi.mock("../lib/appearance-device", () => ({
  useChatFont: () => "default",
  useChatFontWeight: () => "regular",
  setChatFont: vi.fn(),
  setChatFontWeight: vi.fn(),
  useAppearanceSaver: () => ({ schedule: mocks.schedule }),
}));

import { ChatSettingsPage } from "./chat-page";

beforeEach(() => {
  mocks.settings = { values: {}, loaded: true, failed: false };
  mocks.reload.mockReset();
  mocks.update.mockReset().mockResolvedValue(undefined);
  mocks.models.mockReset().mockResolvedValue([
    { name: "gpt-x", group: "Group A" },
    { name: "claude-y", group: "Group B" },
  ]);
  mocks.mode.mockReset().mockResolvedValue("usage");
  mocks.compact.mockReset().mockResolvedValue(false);
  mocks.schedule.mockReset();
  mocks.policy = { processTraceEnabled: true };
  clearToasts();
});
afterEach(cleanup);

const sw = (name: RegExp) => screen.getByRole("switch", { name }) as HTMLButtonElement;

describe("ChatSettingsPage", () => {
  it("shows the defaults for an empty settings map", async () => {
    renderWithApp(<ChatSettingsPage />);
    await waitFor(() => expect(sw(/Format replies/).disabled).toBe(false));
    for (const name of [/Format replies/, /Open reasoning blocks/, /Open tool calls/, /Show the model name/, /Show token counts/, /Show response time/, /Name conversations/, /Add labels/]) {
      expect(sw(name).getAttribute("aria-checked")).toBe("true");
    }
    expect(sw(/Delete attached files/).getAttribute("aria-checked")).toBe("false");
    expect((screen.getByLabelText("Send with") as HTMLSelectElement).value).toBe("enter");
    expect((screen.getByLabelText("Box height") as HTMLSelectElement).value).toBe("standard");
    expect((screen.getByLabelText("How attached files are read") as HTMLSelectElement).value).toBe("auto");
    expect(screen.queryByRole("switch", { name: /Compact long conversations/ })).toBeNull();
  });

  it("keeps every control disabled until the settings load", async () => {
    mocks.settings = { values: {}, loaded: false, failed: false };
    renderWithApp(<ChatSettingsPage />);
    expect(sw(/Format replies/).disabled).toBe(true);
  });

  it("offers a retry when the settings cannot be loaded, and keeps the controls off", async () => {
    mocks.settings = { values: {}, loaded: false, failed: true };
    const user = userEvent.setup();
    renderWithApp(<ChatSettingsPage />);
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("Could not load your chat settings");
    expect(sw(/Format replies/).disabled).toBe(true);
    await user.click(within(alert).getByRole("button", { name: "Try again" }));
    expect(mocks.reload).toHaveBeenCalledTimes(1);
  });

  it("writes one key per change", async () => {
    const user = userEvent.setup();
    renderWithApp(<ChatSettingsPage />);
    await waitFor(() => expect(sw(/Format replies/).disabled).toBe(false));
    await user.click(sw(/Format replies/));
    expect(mocks.update).toHaveBeenCalledWith({ "chat.markdown_render": "false" });
    await user.click(sw(/Delete attached files/));
    expect(mocks.update).toHaveBeenCalledWith({ "chat.delete_conversation_files_by_default": "true" });
  });

  it("writes an empty default model for 'recommended' and the platform name otherwise", async () => {
    mocks.settings = { values: { "chat.default_model": "gpt-x" }, loaded: true, failed: false };
    const user = userEvent.setup();
    renderWithApp(<ChatSettingsPage />);
    const select = (await screen.findByLabelText("Default model")) as HTMLSelectElement;
    await waitFor(() => expect(select.value).toBe("gpt-x"));
    await user.selectOptions(select, "");
    expect(mocks.update).toHaveBeenCalledWith({ "chat.default_model": "" });
    await user.selectOptions(select, "claude-y");
    expect(mocks.update).toHaveBeenCalledWith({ "chat.default_model": "claude-y" });
  });

  it("locks the cost switch off while the deployment does not bill", async () => {
    mocks.mode.mockResolvedValue("self");
    mocks.settings = { values: { "chat.show_billing_cost": "true" }, loaded: true, failed: false };
    renderWithApp(<ChatSettingsPage />);
    await waitFor(() => expect(screen.getByText(/Not available while the deployment/)).toBeTruthy());
    const cost = sw(/Show the cost/);
    expect(cost.disabled).toBe(true);
    expect(cost.getAttribute("aria-checked")).toBe("false");
  });

  it("shows the cost switch as on when billing is active and the setting is on", async () => {
    renderWithApp(<ChatSettingsPage />);
    await waitFor(() => expect(sw(/Show the cost/).disabled).toBe(false));
    expect(sw(/Show the cost/).getAttribute("aria-checked")).toBe("true");
  });

  it("offers auto-compact only when the server policy allows it", async () => {
    mocks.compact.mockResolvedValue(true);
    renderWithApp(<ChatSettingsPage />);
    expect(await screen.findByRole("switch", { name: /Compact long conversations/ })).toBeTruthy();
  });

  it("hides the process trace switch when the feature policy turns it off", async () => {
    mocks.policy = { processTraceEnabled: false };
    renderWithApp(<ChatSettingsPage />);
    await screen.findByRole("switch", { name: /Format replies/ });
    expect(screen.queryByRole("switch", { name: /Show the process trace/ })).toBeNull();
  });

  it("shows an error toast when a save fails", async () => {
    mocks.update.mockRejectedValue(new Error("server said no"));
    const user = userEvent.setup();
    renderWithApp(<ChatSettingsPage />);
    await waitFor(() => expect(sw(/Format replies/).disabled).toBe(false));
    await user.click(sw(/Format replies/));
    await waitFor(() => expect(toastTitles()).toContain("Could not save the setting"));
  });

  it("describes the file mode that is selected", async () => {
    mocks.settings = { values: { "chat.file_mode": "rag" }, loaded: true, failed: false };
    renderWithApp(<ChatSettingsPage />);
    expect(await screen.findByText(/Always search the file/)).toBeTruthy();
  });

  it("schedules an account save when a reply font is picked", async () => {
    const user = userEvent.setup();
    renderWithApp(<ChatSettingsPage />);
    const group = await screen.findByRole("group", { name: "Reply font" });
    await user.click(within(group).getByRole("button", { name: /Mono/ }));
    expect(mocks.schedule).toHaveBeenCalledWith({ chatFont: "mono" });
  });
});
