// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { TooltipProvider } from "@/components/ui";
import { dismiss, getToasts } from "@/components/ui/toast-store";
import type { LibraryItem } from "./model";

vi.mock("@/lib/i18n/provider", async () => (await import("../shared/testing/i18n-mock")).providerModule());
vi.mock("../shared/session", () => ({ hasSession: async () => true }));
vi.mock("./api", async () => {
  const actual = (await vi.importActual("./api")) as Record<string, unknown>;
  return {
    ...actual,
    listMine: vi.fn(),
    listVisible: vi.fn(),
    getItem: vi.fn(),
    createItem: vi.fn(),
    updateItem: vi.fn(),
    setItemEnabled: vi.fn(),
    deleteItem: vi.fn(),
  };
});

import * as api from "./api";
import { LibraryPage } from "./library-page";

const m = vi.mocked(api);

function item(id: number, over: Partial<LibraryItem> = {}): LibraryItem {
  return {
    id,
    scope: "user",
    title: `item${id}`,
    trigger: `item${id}`,
    description: `About item ${id}`,
    enabled: true,
    sortOrder: 0,
    createdAt: "",
    updatedAt: "",
    ...over,
  };
}

const empty = { items: [], total: 0 };

function renderPage() {
  return render(
    <TooltipProvider>
      <LibraryPage />
    </TooltipProvider>,
  );
}
