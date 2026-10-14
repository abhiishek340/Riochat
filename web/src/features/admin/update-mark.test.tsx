// @vitest-environment jsdom
import { cleanup, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { renderWithApp } from "./test-utils";
import { cacheRelease, LATEST_RELEASE_KEY } from "./lib/update-check";

const version = vi.hoisted(() => ({ fetchVersion: vi.fn() }));
vi.mock("@/lib/api/endpoints/public", async (importOriginal) => ({ ...(await importOriginal<Record<string, unknown>>()), ...version }));

import { AdminUpdateMark } from "./update-mark";

beforeEach(() => {
  version.fetchVersion.mockReset();
  window.localStorage.removeItem(LATEST_RELEASE_KEY);
});
afterEach(cleanup);

describe("AdminUpdateMark", () => {
  it("shows nothing and asks for no version while no release is known", async () => {
    renderWithApp(<AdminUpdateMark />);
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.queryByRole("img")).toBeNull();
    expect(version.fetchVersion).not.toHaveBeenCalled();
  });

  it("shows the labelled arrow when the cached release is newer than the running version", async () => {
    version.fetchVersion.mockResolvedValue({ version: "0.4.5" });
    cacheRelease({ version: "v0.5.0", url: "https://example.test/r" });
    renderWithApp(<AdminUpdateMark />);
    expect(await screen.findByRole("img", { name: "Update available" })).toBeTruthy();
  });

  it("stays hidden when the running version is current", async () => {
    version.fetchVersion.mockResolvedValue({ version: "0.5.0" });
    cacheRelease({ version: "v0.5.0", url: "https://example.test/r" });
    renderWithApp(<AdminUpdateMark />);
    await waitFor(() => expect(version.fetchVersion).toHaveBeenCalled());
    await new Promise((r) => setTimeout(r, 20));
    expect(screen.queryByRole("img")).toBeNull();
  });
});
