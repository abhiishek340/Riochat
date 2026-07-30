// @vitest-environment jsdom
import { cleanup, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { LoginProvider } from "@/lib/api/types";
import { renderWithApp } from "../test-utils";
import type { IdentityRow } from "./api";
import { bindableProviders, IdentitiesSection } from "./identities-section";

afterEach(cleanup);

const provider = (slug: string, over: Partial<LoginProvider> = {}): LoginProvider =>
  ({ slug, name: slug.toUpperCase(), logoURL: "", loginEnabled: true, type: "oidc", ...over }) as LoginProvider;
const identity = (id: number, slug: string): IdentityRow => ({
  id,
  providerSlug: slug,
  providerName: slug.toUpperCase(),
  providerType: "oidc",
  providerLogoURL: "",
  providerDisplayName: "ada",
  email: "ada@example.test",
  linkedAt: "2025-02-03T04:05:06Z",
});

describe("bindableProviders", () => {
  it("lists login-enabled providers that are not linked yet, matched by slug", () => {
    const list = bindableProviders([provider("a"), provider("b"), provider("c", { loginEnabled: false })], [identity(1, "a")]);
    expect(list.map((p) => p.slug)).toEqual(["b"]);
  });
});

describe("IdentitiesSection", () => {
  it("blocks unlinking the only sign-in when there is no password, and says why to keyboard users", () => {
    renderWithApp(<IdentitiesSection identities={[identity(1, "a")]} providers={[provider("a")]} loading={false} hasPassword={false} onChanged={vi.fn()} />);
    const button = screen.getByRole("button", { name: /^Unlink/ }) as HTMLButtonElement;
    expect(button.disabled).toBe(true);
    const wrapper = button.parentElement as HTMLElement;
    expect(wrapper.tabIndex).toBe(0);
    expect(wrapper.getAttribute("aria-label")).toBeTruthy();
  });

  it("allows unlinking when a password exists", () => {
    renderWithApp(<IdentitiesSection identities={[identity(1, "a")]} providers={[provider("a")]} loading={false} hasPassword onChanged={vi.fn()} />);
    const button = screen.getByRole("button", { name: /^Unlink/ }) as HTMLButtonElement;
    expect(button.disabled).toBe(false);
    expect((button.parentElement as HTMLElement).tabIndex).toBe(-1);
  });

  it("shows a loading row while loading", () => {
    renderWithApp(<IdentitiesSection identities={[]} providers={[provider("a")]} loading hasPassword onChanged={vi.fn()} />);
    expect(screen.getByText("Loading…")).toBeTruthy();
  });

  it("shows an empty row when nothing is linked", () => {
    renderWithApp(<IdentitiesSection identities={[]} providers={[provider("a")]} loading={false} hasPassword onChanged={vi.fn()} />);
    expect(screen.queryByText("Loading…")).toBeNull();
    expect(within(screen.getByRole("table")).getAllByRole("row")).toHaveLength(2);
  });
});
