// @vitest-environment jsdom
import { cleanup, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderWithApp } from "../test-utils";
import type { FieldDef } from "../lib/settings-form";
import type { SettingsForm } from "../lib/use-settings-form";
import { SettingsGroup } from "./settings-group";

afterEach(cleanup);

const fields: FieldDef[] = [{ ns: "mcp", key: "mcp_enable", type: "bool", label: "adminTools.settings.title" as FieldDef["label"], def: "false" }];

function formOf(over: Partial<SettingsForm>): SettingsForm {
  return {
    draft: {},
    saved: {},
    configured: new Set<string>(),
    tree: null,
    loading: false,
    failed: false,
    saving: false,
    set: () => undefined,
    setDraft: () => undefined,
    dirtyOf: () => [],
    revert: () => undefined,
    save: async () => ({ ok: true, sent: [] }),
    saveValues: async () => ({ ok: true, sent: [] }),
    reload: () => undefined,
    ...over,
  } as SettingsForm;
}

describe("SettingsGroup", () => {
  it("offers a retry instead of blank fields after a failed read", async () => {
    const user = userEvent.setup();
    const reload = vi.fn();
    renderWithApp(
      <SettingsGroup form={formOf({ failed: true, reload })} fields={fields} title="Group">
        <p>extra content</p>
      </SettingsGroup>,
    );
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(screen.queryByRole("switch")).toBeNull();
    expect(screen.queryByText("extra content")).toBeNull();
    await user.click(screen.getByRole("button", { name: "Try again" }));
    expect(reload).toHaveBeenCalledTimes(1);
  });

  it("shows the fields once the read worked", async () => {
    renderWithApp(<SettingsGroup form={formOf({})} fields={fields} title="Group" />);
    expect(await screen.findByRole("switch")).toBeTruthy();
    expect(screen.queryByRole("alert")).toBeNull();
  });
});
