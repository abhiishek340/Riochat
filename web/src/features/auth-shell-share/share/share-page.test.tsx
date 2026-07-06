// @vitest-environment jsdom
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api/errors";
import { readSnapshot } from "../lib/share-model";
import { clearToasts, renderWithI18n, toastTitles } from "../test-utils";

const router = vi.hoisted(() => ({ push: vi.fn(), replace: vi.fn() }));
let query = "conversation_id=abc";
vi.mock("next/navigation", () => ({ useRouter: () => router, useSearchParams: () => new URLSearchParams(query), usePathname: () => "/share" }));

const api = vi.hoisted(() => ({ fetchSharedConversation: vi.fn(), cloneSharedConversation: vi.fn() }));
vi.mock("./api", () => api);

let signedIn = false;
vi.mock("./use-viewer", () => ({ useViewer: () => ({ signedIn, resolved: true }) }));

import { SharePage } from "./share-page";

const snapshot = () =>
  readSnapshot(
    {
      shareID: "abc",
      title: "Trip plan",
      model: "gpt-x",
      createdAt: "2025-03-04T10:00:00Z",
      defaultMessagePublicIDs: ["u1", "a1"],
      messages: [
        { publicID: "u1", role: "user", content: "Where should we go?", createdAt: "2025-03-04T10:00:00Z" },
        { publicID: "a1", parentPublicID: "u1", role: "assistant", content: "First answer ![x](/api/v1/files/f1/content?v=2)", createdAt: "2025-03-04T10:00:01Z", latencyMS: 1500, inputTokens: 10, outputTokens: 20 },
        { publicID: "a2", parentPublicID: "u1", role: "assistant", content: "Second answer", createdAt: "2025-03-04T10:00:02Z", branchReason: "retry" },
      ],
    },
    "abc",
  )!;

beforeEach(() => {
  query = "conversation_id=abc";
  signedIn = false;
  Object.values(api).forEach((fn) => fn.mockReset());
  router.push.mockReset();
  clearToasts();
});
afterEach(cleanup);

describe("SharePage", () => {
  it("shows the unavailable state with the fallback description", async () => {
    api.fetchSharedConversation.mockRejectedValue(new ApiError({ message: "", status: 404 }));
    renderWithI18n(<SharePage />);
    await screen.findByText("Share unavailable");
    expect(screen.getByText(/may have been turned off/i)).toBeTruthy();
  });

  it("explains a revoked share in the viewer's language rather than the server's English sentence", async () => {
    api.fetchSharedConversation.mockRejectedValue(
      new ApiError({ message: "conversation share not found", status: 404, errorCode: "conversation_share.not_found" }),
    );
    renderWithI18n(<SharePage />);
    await screen.findByText("Share unavailable");
    expect(screen.queryByText("conversation share not found")).toBeNull();
    expect(screen.getByText(/may have been turned off/i)).toBeTruthy();
  });

  it("keeps the server's wording for failures that are not about the link itself", async () => {
    api.fetchSharedConversation.mockRejectedValue(new ApiError({ message: "Too many requests, slow down", status: 429 }));
    renderWithI18n(<SharePage />);
    await screen.findByText("Too many requests, slow down");
  });

  it("treats a missing id as unavailable without asking the server", async () => {
    query = "";
    renderWithI18n(<SharePage />);
    await screen.findByText("Share unavailable");
    expect(api.fetchSharedConversation).not.toHaveBeenCalled();
  });

  it("renders the default branch, rewrites file links and steps between siblings with clamping", async () => {
    api.fetchSharedConversation.mockResolvedValue(snapshot());
    renderWithI18n(<SharePage />);
    await screen.findByText("Trip plan");
    expect(screen.getByText("Where should we go?")).toBeTruthy();
    expect(screen.getByText(/First answer/)).toBeTruthy();
    expect(screen.getByText(/3 snapshot messages/)).toBeTruthy();
    const image = document.querySelector("img[alt='x']");
    expect(image?.getAttribute("src")).toBe("/api/v1/shared-conversations/abc/files/f1/content?v=2");
    expect(screen.getByText("1 / 2")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Previous version" }) as HTMLButtonElement).disabled).toBe(true);

    await userEvent.click(screen.getByRole("button", { name: "Next version" }));
    expect(screen.getByText("Second answer")).toBeTruthy();
    expect(screen.getByText("2 / 2")).toBeTruthy();
    expect((screen.getByRole("button", { name: "Next version" }) as HTMLButtonElement).disabled).toBe(true);
  });

  it("shows model, latency and token usage on assistant replies", async () => {
    api.fetchSharedConversation.mockResolvedValue(snapshot());
    renderWithI18n(<SharePage />);
    await screen.findByText("Trip plan");
    expect(screen.getByText("gpt-x · 1.5 s · 10 in, 20 out")).toBeTruthy();
  });

  it("sends anonymous viewers to sign in and back to this share", async () => {
    api.fetchSharedConversation.mockResolvedValue(snapshot());
    renderWithI18n(<SharePage />);
    await userEvent.click(await screen.findByRole("button", { name: "Sign in to continue" }));
    expect(router.push).toHaveBeenCalledWith("/login?next=%2Fshare%3Fconversation_id%3Dabc");
    expect(api.cloneSharedConversation).not.toHaveBeenCalled();
  });

  it("clones into the viewer's account when signed in", async () => {
    signedIn = true;
    api.fetchSharedConversation.mockResolvedValue(snapshot());
    api.cloneSharedConversation.mockResolvedValue("new-id");
    renderWithI18n(<SharePage />);
    await userEvent.click(await screen.findByRole("button", { name: "Continue conversation" }));
    await waitFor(() => expect(router.push).toHaveBeenCalledWith("/chat?conversation_id=new-id"));
    expect(api.cloneSharedConversation).toHaveBeenCalledWith("abc");
  });

  it("re-enables the button and explains when cloning fails", async () => {
    signedIn = true;
    api.fetchSharedConversation.mockResolvedValue(snapshot());
    api.cloneSharedConversation.mockRejectedValue(new ApiError({ message: "boom", status: 500 }));
    renderWithI18n(<SharePage />);
    await userEvent.click(await screen.findByRole("button", { name: "Continue conversation" }));
    await waitFor(() => expect(toastTitles()).toContain("Failed to continue conversation"));
    expect((screen.getByRole("button", { name: "Continue conversation" }) as HTMLButtonElement).disabled).toBe(false);
    expect(router.push).not.toHaveBeenCalled();
  });

  it("explains messages the server blanked out or that never finished instead of drawing empty bubbles", async () => {
    api.fetchSharedConversation.mockResolvedValue(
      readSnapshot(
        {
          shareID: "abc",
          messages: [
            { publicID: "u1", role: "user", content: "", status: "blocked", createdAt: "2025-03-04T10:00:00Z" },
            { publicID: "a1", parentPublicID: "u1", role: "assistant", content: "", status: "blocked", createdAt: "2025-03-04T10:00:01Z" },
            { publicID: "u2", parentPublicID: "a1", role: "user", content: "again", createdAt: "2025-03-04T10:00:02Z" },
            { publicID: "a2", parentPublicID: "u2", role: "assistant", content: "half an ans", status: "interrupted", createdAt: "2025-03-04T10:00:03Z" },
          ],
        },
        "abc",
      )!,
    );
    renderWithI18n(<SharePage />);
    await waitFor(() => expect(screen.getAllByText("This message is not available in the shared copy.")).toHaveLength(2));
    expect(screen.getByText("half an ans")).toBeTruthy();
    expect(screen.getByText("This reply did not finish.")).toBeTruthy();
  });
});
