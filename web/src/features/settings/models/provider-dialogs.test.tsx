// @vitest-environment jsdom
import { cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { StrictMode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TooltipProvider } from "@/components/ui/tooltip";
import { I18nProvider } from "@/lib/i18n/provider";
import { clearToasts } from "../test-utils";

const mocks = vi.hoisted(() => ({ fetchProviderModels: vi.fn(), updateProvider: vi.fn() }));
vi.mock("./api", async (orig) => ({ ...(await orig<object>()), fetchProviderModels: mocks.fetchProviderModels, updateProvider: mocks.updateProvider }));

import { normalizeProvider, type ProviderAccess } from "./api";
import { EditProviderDialog } from "./provider-dialogs";

const access: ProviderAccess = { enabled: true, maxPerUser: 5, protocols: [], modelProtocols: [] };
const provider = normalizeProvider({
  id: "p1",
  name: "Mine",
  baseURL: "https://api.example.test/v1",
  host: "api.example.test",
  protocol: "openai_chat_completions",
  status: "active",
  models: ["alpha"],
});

beforeEach(() => {
  mocks.fetchProviderModels.mockReset().mockResolvedValue([{ name: "alpha", suggestedProtocols: ["openai_chat_completions"] }]);
  mocks.updateProvider.mockReset();
  clearToasts();
});
afterEach(cleanup);

function renderDialog(strict: boolean, onOpenChange = vi.fn()) {
  const ui = (
    <I18nProvider>
      <TooltipProvider>
        <EditProviderDialog provider={provider} onOpenChange={onOpenChange} access={access} onSaved={vi.fn()} />
      </TooltipProvider>
    </I18nProvider>
  );
  return render(strict ? <StrictMode>{ui}</StrictMode> : ui);
}

describe("EditProviderDialog", () => {
  it("finishes loading the live model list when effects run twice (strict mode)", async () => {
    renderDialog(true);
    await waitFor(() => expect(mocks.fetchProviderModels).toHaveBeenCalled());
    const save = (await screen.findByRole("button", { name: "Save" })) as HTMLButtonElement;
    await waitFor(() => expect(save.disabled).toBe(false));
  });

  it("closes without a request when nothing changed", async () => {
    const onOpenChange = vi.fn();
    const user = userEvent.setup();
    renderDialog(false, onOpenChange);
    const save = (await screen.findByRole("button", { name: "Save" })) as HTMLButtonElement;
    await waitFor(() => expect(save.disabled).toBe(false));
    await user.click(save);
    expect(mocks.updateProvider).not.toHaveBeenCalled();
    expect(onOpenChange).toHaveBeenCalledWith(false);
  });

  it("sends only the changed name", async () => {
    mocks.updateProvider.mockResolvedValue(provider);
    const user = userEvent.setup();
    renderDialog(false);
    const save = (await screen.findByRole("button", { name: "Save" })) as HTMLButtonElement;
    await waitFor(() => expect(save.disabled).toBe(false));
    const name = screen.getByRole("textbox", { name: "Name" });
    await user.clear(name);
    await user.type(name, "Renamed");
    await user.click(save);
    expect(mocks.updateProvider).toHaveBeenCalledWith("p1", { name: "Renamed" });
  });
});
