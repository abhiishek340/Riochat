// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { SettingsPage, SettingsSection } from "./page";

afterEach(cleanup);

describe("settings page headings", () => {
  it("leaves the level-one heading to the shell: page title h2, section titles h3", () => {
    render(
      <SettingsPage title="Chat">
        <SettingsSection title="Display">
          <p>body</p>
        </SettingsSection>
      </SettingsPage>,
    );
    expect(screen.queryAllByRole("heading", { level: 1 })).toHaveLength(0);
    expect(screen.getByRole("heading", { level: 2, name: "Chat" })).toBeTruthy();
    expect(screen.getByRole("heading", { level: 3, name: "Display" })).toBeTruthy();
  });
});
