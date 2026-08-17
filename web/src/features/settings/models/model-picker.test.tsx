// @vitest-environment jsdom
import { cleanup, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderWithApp } from "../test-utils";
import type { AvailableModel } from "./api";
import { ModelPicker } from "./model-picker";
import type { SelectedModel } from "./selection";

afterEach(cleanup);

const available: AvailableModel[] = [
  { name: "alpha", suggestedProtocols: ["openai_chat_completions"] },
  { name: "beta", suggestedProtocols: ["openai_chat_completions"] },
  { name: "img", suggestedProtocols: ["openai_image_generations", "openai_image_edits"] },
];

function setup(over: { selected?: SelectedModel[]; offered?: string[] } = {}) {
  const onConfirm = vi.fn();
  const onOpenChange = vi.fn();
  renderWithApp(
    <ModelPicker
      open
      onOpenChange={onOpenChange}
      available={available}
      loading={false}
      error={null}
      selected={over.selected ?? []}
      offeredProtocols={over.offered ?? ["openai_chat_completions", "openai_image_generations", "openai_image_edits"]}
      fallbackProtocol="openai_chat_completions"
      onConfirm={onConfirm}
    />,
  );
  return { onConfirm, onOpenChange };
}

describe("ModelPicker", () => {
  it("confirms the ticked models in list order with their protocols", async () => {
    const user = userEvent.setup();
    const { onConfirm } = setup();
    await user.click(await screen.findByRole("checkbox", { name: /Select everything shown/ }));
    await user.click(screen.getByRole("button", { name: /^Use 3 models$/ }));
    expect(onConfirm).toHaveBeenCalledWith([
      { name: "alpha", protocols: ["openai_chat_completions"] },
      { name: "beta", protocols: ["openai_chat_completions"] },
      { name: "img", protocols: ["openai_image_generations", "openai_image_edits"] },
    ]);
  });

  it("lists models selected at open first and ticks a row when its protocol changes", async () => {
    const user = userEvent.setup();
    const { onConfirm } = setup({ selected: [{ name: "beta", protocols: ["openai_chat_completions"] }] });
    const items = await screen.findAllByRole("listitem");
    expect(within(items[0] as HTMLElement).getByText("beta")).toBeTruthy();
    const select = screen.getByRole("combobox", { name: /alpha/ });
    await user.selectOptions(select, "openai_image_generations+openai_image_edits");
    await user.click(screen.getByRole("button", { name: /^Use 2 models$/ }));
    expect(onConfirm).toHaveBeenCalledWith([
      { name: "beta", protocols: ["openai_chat_completions"] },
      { name: "alpha", protocols: ["openai_image_generations", "openai_image_edits"] },
    ]);
  });

  it("hides the protocol selects when the server offers none, and disables Confirm with nothing ticked", async () => {
    setup({ offered: [] });
    await screen.findByText("alpha");
    expect(screen.queryAllByRole("combobox")).toHaveLength(0);
    expect((screen.getByRole("button", { name: /^Use 0 models$/ }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("filters by search without losing the selection", async () => {
    const user = userEvent.setup();
    const { onConfirm } = setup({ selected: [{ name: "alpha", protocols: ["openai_chat_completions"] }] });
    await user.type(await screen.findByRole("textbox", { name: /Search models/ }), "bet");
    expect(screen.queryByText("alpha")).toBeNull();
    expect(screen.getByText("beta")).toBeTruthy();
    await user.click(screen.getByRole("button", { name: /^Use 1 model$/ }));
    expect(onConfirm).toHaveBeenCalledWith([{ name: "alpha", protocols: ["openai_chat_completions"] }]);
  });
});
