// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { TooltipProvider } from "@/components/ui";

vi.mock("@/lib/i18n/provider", async () => (await import("../shared/testing/i18n-mock")).providerModule());

import { ItemCard } from "./item-card";
import type { LibraryItem } from "./model";

afterEach(cleanup);

const item = (over: Partial<LibraryItem> = {}): LibraryItem => ({
  id: 1,
  scope: "user",
  title: "review",
  trigger: "review",
  description: "Review a diff",
  enabled: true,
  sortOrder: 0,
  createdAt: "",
  updatedAt: "",
  ...over,
});

function setup(over: Partial<LibraryItem> = {}) {
  const handlers = { onOpen: vi.fn(), onToggle: vi.fn(), onDelete: vi.fn() };
  render(
    <TooltipProvider>
      <ul>
        <ItemCard item={item(over)} {...handlers} />
      </ul>
    </TooltipProvider>,
  );
  return handlers;
}

describe("ItemCard", () => {
  it("opens with click, Enter and Space", async () => {
    const user = userEvent.setup();
    const h = setup();
    const card = screen.getByRole("button", { name: /^\/review/ });
    await user.click(card);
    card.focus();
    await user.keyboard("{Enter}");
    await user.keyboard(" ");
    expect(h.onOpen).toHaveBeenCalledTimes(3);
  });
  it("keeps list semantics: the card is a list item holding separate controls", () => {
    setup();
    expect(screen.getAllByRole("listitem")).toHaveLength(1);
    const card = screen.getByRole("button", { name: /^\/review/ });
    expect(card.contains(screen.getByRole("switch"))).toBe(false);
  });
  it("opens when the card's own padding is clicked", () => {
    const h = setup();
    fireEvent.click(screen.getByRole("listitem"));
    expect(h.onOpen).toHaveBeenCalledTimes(1);
  });
  it("does not open when the switch or delete button is used (scenario 30)", () => {
    const h = setup();
    fireEvent.click(screen.getByRole("switch"));
    expect(h.onToggle).toHaveBeenCalledWith(false);
    fireEvent.keyDown(screen.getByRole("switch"), { key: "Enter" });
    fireEvent.click(screen.getByRole("button", { name: "Delete review" }));
    expect(h.onDelete).toHaveBeenCalledTimes(1);
    expect(h.onOpen).not.toHaveBeenCalled();
  });
  it("shows built-in items read-only (scenario 31)", () => {
    setup({ scope: "builtin" });
    expect(screen.getByText("Built-in")).toBeTruthy();
    expect(screen.queryByRole("switch")).toBeNull();
    expect(screen.queryByRole("button", { name: "Delete review" })).toBeNull();
  });
  it("dims disabled items and shows the trigger with a slash", () => {
    setup({ enabled: false });
    expect(screen.getByText("/review")).toBeTruthy();
    expect(document.querySelector("[data-disabled]")).toBeTruthy();
  });
});
