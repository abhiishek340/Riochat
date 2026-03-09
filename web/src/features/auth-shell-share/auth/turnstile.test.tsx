// @vitest-environment jsdom
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderWithI18n } from "../test-utils";
import { Turnstile } from "./turnstile";

afterEach(() => {
  cleanup();
  document.head.querySelectorAll("script").forEach((s) => s.remove());
  delete window.turnstile;
});

const scripts = () => [...document.head.querySelectorAll("script")].filter((s) => s.src.includes("turnstile"));

describe("Turnstile", () => {
  it("says so and offers a retry when the widget script cannot be loaded", async () => {
    const onToken = vi.fn();
    renderWithI18n(<Turnstile siteKey="k" onToken={onToken} resetKey={0} />);
    await waitFor(() => expect(scripts()).toHaveLength(1));
    scripts()[0]!.onerror?.(new Event("error"));
    const alert = await screen.findByRole("alert");
    expect(alert.textContent).toContain("could not be loaded");
    expect(onToken).toHaveBeenCalledWith("");

    const render = vi.fn(() => "w1");
    window.turnstile = { render, reset: vi.fn(), remove: vi.fn() };
    await userEvent.click(screen.getByRole("button", { name: "Try again" }));
    await waitFor(() => expect(render).toHaveBeenCalledTimes(1));
    expect(screen.queryByRole("alert")).toBeNull();
  });
});
