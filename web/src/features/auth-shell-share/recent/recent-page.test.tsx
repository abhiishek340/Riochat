// @vitest-environment jsdom
import { cleanup, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { getToasts } from "@/components/ui/toast-store";
import { clearToasts, renderWithI18n, toastTitles } from "../test-utils";
import { readConversation, type ConversationRow } from "./model";

const router = vi.hoisted(() => ({ push: vi.fn(), replace: vi.fn() }));
let search = "";
vi.mock("next/navigation", () => ({ useRouter: () => router, useSearchParams: () => new URLSearchParams(search), usePathname: () => "/recent" }));
vi.mock("@/components/shell/header-context", () => ({ HeaderActions: ({ children }: { children: React.ReactNode }) => <div data-testid="header-actions">{children}</div> }));
const api = vi.hoisted(() => ({
  fetchConversationPage: vi.fn(),
  fetchProjects: vi.fn(),
  fetchDeleteFilesDefault: vi.fn(),
  setArchived: vi.fn(),
  setStarred: vi.fn(),
  revokeShares: vi.fn(),
  exportAllJsonl: vi.fn(),
  exportConversationJson: vi.fn(),
  deleteConversation: vi.fn(),
  moveManyToProject: vi.fn(),
  moveToProject: vi.fn(),
  renameConversation: vi.fn(),
  regenerateTitle: vi.fn(),
  setLabels: vi.fn(),
  fetchShare: vi.fn(),
  createShare: vi.fn(),
  revokeShare: vi.fn(),
}));
vi.mock("./api", () => api);
const download = vi.hoisted(() => ({ downloadBlob: vi.fn() }));
vi.mock("../lib/download", async (orig) => ({ ...(await orig<object>()), downloadBlob: download.downloadBlob }));

import { RecentPage } from "./recent-page";

const row = (n: number, over: Record<string, unknown> = {}): ConversationRow =>
  readConversation({ publicID: `c${n}`, title: `Chat ${n}`, status: "active", updatedAt: new Date(Date.UTC(2025, 0, 1, 0, 0, 100 - n)).toISOString(), ...over })!;
const page = (rows: ConversationRow[], total = rows.length) => ({ rows, total, received: rows.length });

const lastFilters = () => api.fetchConversationPage.mock.calls.at(-1)![0];

beforeEach(() => {
  search = "";
  Object.values(api).forEach((fn) => fn.mockReset());
  download.downloadBlob.mockReset();
  router.push.mockReset();
  api.fetchProjects.mockResolvedValue([{ publicID: "P1", name: "Alpha" }]);
  api.fetchDeleteFilesDefault.mockResolvedValue(false);
  api.fetchConversationPage.mockResolvedValue(page([row(1), row(2)]));
  clearToasts();
});
afterEach(cleanup);

const rowFor = (title: string) => screen.getByText(title).closest("li") as HTMLElement;
