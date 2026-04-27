// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n/provider";

const state = vi.hoisted(() => ({ status: "ready", role: "admin" as string | undefined }));
vi.mock("next/link", () => ({ default: ({ href, children }: { href: string; children: React.ReactNode }) => <a href={href}>{children}</a> }));
vi.mock("@/lib/session/provider", () => ({
  useSession: () => ({ user: state.role ? { role: state.role } : null, status: state.status }),
  isAdminRole: (role: string | undefined) => role === "admin" || role === "superadmin",
}));

import { AdminGate } from "./admin-gate";

function renderGate() {
  return render(
    <I18nProvider>
      <AdminGate>
        <p>console content</p>
      </AdminGate>
    </I18nProvider>,
  );
}

beforeEach(() => {
  state.status = "ready";
  state.role = "admin";
});
afterEach(cleanup);

describe("AdminGate", () => {
  it("shows neither the console nor the denied screen while the profile loads", () => {
    state.status = "loading";
    state.role = undefined;
    renderGate();
    expect(screen.queryByText("console content")).toBeNull();
    expect(screen.queryByRole("link")).toBeNull();
  });

  it("renders the console for administrators and super administrators", () => {
    renderGate();
    expect(screen.getByText("console content")).toBeTruthy();
    cleanup();
    state.role = "superadmin";
    renderGate();
    expect(screen.getByText("console content")).toBeTruthy();
  });

  it("answers other viewers with a denied screen and never mounts the console", () => {
    state.role = "user";
    renderGate();
    expect(screen.queryByText("console content")).toBeNull();
    expect(screen.getByRole("link")).toBeTruthy();
  });
});
