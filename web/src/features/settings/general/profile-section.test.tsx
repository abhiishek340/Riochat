// @vitest-environment jsdom
import { cleanup, fireEvent, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError } from "@/lib/api/errors";
import type { AuthUser } from "@/lib/api/types";
import { clearToasts, renderWithApp, toastTitles } from "../test-utils";

const mocks = vi.hoisted(() => ({
  user: null as unknown,
  publish: vi.fn(),
  patchProfile: vi.fn(),
  patchUsername: vi.fn(),
  uploadAvatar: vi.fn(),
}));
vi.mock("@/lib/session/provider", () => ({
  useSession: () => ({ user: mocks.user, status: "ready", accessToken: "t" }),
  publishUserUpdate: mocks.publish,
}));
vi.mock("../api", () => ({ patchProfile: mocks.patchProfile, patchUsername: mocks.patchUsername, uploadAvatar: mocks.uploadAvatar }));

import { ProfileSection } from "./profile-section";

const baseUser = (over: Partial<AuthUser> = {}): AuthUser =>
  ({
    id: 1,
    publicID: "u_1",
    username: "ada",
    displayName: "Ada Lovelace",
    avatarURL: "",
    timezone: "Etc/UTC",
    locale: "en-US",
    profilePreferences: "",
    initialUsernameRequired: false,
    updatedAt: "2025-01-01T00:00:00Z",
    ...over,
  }) as AuthUser;

beforeEach(() => {
  mocks.user = baseUser();
  mocks.publish.mockReset();
  mocks.patchProfile.mockReset();
  mocks.patchUsername.mockReset();
  mocks.uploadAvatar.mockReset();
  clearToasts();
});
afterEach(cleanup);

const nameBox = () => screen.getByRole("textbox", { name: "Display name" });
const usernameBox = () => screen.getByRole("textbox", { name: "Username" });

describe("ProfileSection save", () => {
  it("shows Reset and Save only after an edit", async () => {
    const user = userEvent.setup();
    renderWithApp(<ProfileSection />);
    expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
    await user.type(nameBox(), "x");
    expect(screen.getByRole("button", { name: "Save" })).toBeTruthy();
    await user.click(screen.getByRole("button", { name: "Reset" }));
    expect((nameBox() as HTMLInputElement).value).toBe("Ada Lovelace");
    expect(screen.queryByRole("button", { name: "Save" })).toBeNull();
  });

  it("refuses a display name that is too short without sending anything", async () => {
    const user = userEvent.setup();
    renderWithApp(<ProfileSection />);
    await user.clear(nameBox());
    await user.type(nameBox(), "ab");
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(toastTitles()).toContain("Could not save your profile");
    expect(mocks.patchProfile).not.toHaveBeenCalled();
    expect(mocks.patchUsername).not.toHaveBeenCalled();
  });

  it("refuses a reserved username", async () => {
    const user = userEvent.setup();
    renderWithApp(<ProfileSection />);
    await user.clear(usernameBox());
    await user.type(usernameBox(), "Admin");
    expect((usernameBox() as HTMLInputElement).value).toBe("admin"); // lower-cased as typed
    await user.click(screen.getByRole("button", { name: "Save" }));
    expect(toastTitles()).toContain("Could not set the username");
    expect(mocks.patchUsername).not.toHaveBeenCalled();
  });

  it("stops after a failed username change and does not send the profile", async () => {
    mocks.patchUsername.mockRejectedValue(new ApiError({ message: "username change already used", status: 409 }));
    const user = userEvent.setup();
    renderWithApp(<ProfileSection />);
    await user.clear(usernameBox());
    await user.type(usernameBox(), "grace");
    await user.type(nameBox(), "!");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(toastTitles()).toContain("Could not set the username"));
    expect(mocks.patchProfile).not.toHaveBeenCalled();
  });

  it("sends all five profile fields together and publishes the result", async () => {
    const saved = baseUser({ displayName: "Ada Byron" });
    mocks.patchProfile.mockResolvedValue(saved);
    const user = userEvent.setup();
    renderWithApp(<ProfileSection />);
    await user.clear(nameBox());
    await user.type(nameBox(), "Ada Byron");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(mocks.patchProfile).toHaveBeenCalledTimes(1));
    expect(mocks.patchProfile).toHaveBeenCalledWith({
      avatarURL: "",
      displayName: "Ada Byron",
      timezone: "Etc/UTC",
      locale: "en-US",
      profilePreferences: "",
    });
    expect(mocks.publish).toHaveBeenCalledWith(saved);
    await waitFor(() => expect(toastTitles()).toContain("Profile saved"));
  });

  it("still publishes the new username when the profile request fails afterwards", async () => {
    const renamed = baseUser({ username: "grace", usernameChangedAt: "2025-03-01T00:00:00Z" });
    mocks.patchUsername.mockResolvedValue(renamed);
    mocks.patchProfile.mockRejectedValue(new Error("profile down"));
    const user = userEvent.setup();
    renderWithApp(<ProfileSection />);
    await user.clear(usernameBox());
    await user.type(usernameBox(), "grace");
    await user.type(nameBox(), "!");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(toastTitles()).toContain("Could not save your profile"));
    expect(mocks.patchUsername).toHaveBeenCalledWith("grace");
    // The username cannot be changed again, so the app must hear about it.
    expect(mocks.publish).toHaveBeenCalledWith(renamed);
  });

  it("publishes once when only the username changes", async () => {
    const renamed = baseUser({ username: "grace", usernameChangedAt: "2025-03-01T00:00:00Z" });
    mocks.patchUsername.mockResolvedValue(renamed);
    const user = userEvent.setup();
    renderWithApp(<ProfileSection />);
    await user.clear(usernameBox());
    await user.type(usernameBox(), "grace");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(toastTitles()).toContain("Username saved"));
    expect(mocks.patchProfile).not.toHaveBeenCalled();
    expect(mocks.publish).toHaveBeenCalledTimes(1);
  });

  it("makes the username read-only once it has been changed", () => {
    mocks.user = baseUser({ usernameChangedAt: "2025-01-02T00:00:00Z" });
    renderWithApp(<ProfileSection />);
    expect((usernameBox() as HTMLInputElement).disabled).toBe(true);
  });
});

describe("ProfileSection loading", () => {
  it("fills the form when the account arrives after the first render", () => {
    mocks.user = null;
    const view = renderWithApp(<ProfileSection />);
    expect((nameBox() as HTMLInputElement).value).toBe("");
    mocks.user = baseUser({ displayName: "Grace Hopper", username: "grace" });
    view.rerenderWithApp(<ProfileSection />);
    expect((nameBox() as HTMLInputElement).value).toBe("Grace Hopper");
    expect((usernameBox() as HTMLInputElement).value).toBe("grace");
  });
});

describe("avatar dialog", () => {
  it("rejects a non-image upload without any request", async () => {
    const user = userEvent.setup({ applyAccept: false });
    renderWithApp(<ProfileSection />);
    await user.click(screen.getByRole("button", { name: "Change avatar" }));
    const input = document.querySelector('input[type="file"]') as HTMLInputElement;
    await user.upload(input, new File(["x"], "notes.txt", { type: "text/plain" }));
    expect(toastTitles()).toContain("Please choose an image file.");
    expect(mocks.uploadAvatar).not.toHaveBeenCalled();
  });

  it("applying a changed address sends only the avatar", async () => {
    const saved = baseUser({ avatarURL: "https://img.test/a.png" });
    mocks.patchProfile.mockResolvedValue(saved);
    const user = userEvent.setup();
    renderWithApp(<ProfileSection />);
    await user.type(nameBox(), "!"); // an unsaved edit that must survive
    await user.click(screen.getByRole("button", { name: "Change avatar" }));
    const address = await screen.findByRole("textbox", { name: "Image address" });
    fireEvent.change(address, { target: { value: "https://img.test/a.png" } });
    await user.click(screen.getByRole("button", { name: "Apply" }));
    await waitFor(() => expect(mocks.patchProfile).toHaveBeenCalledTimes(1));
    expect(mocks.patchProfile).toHaveBeenCalledWith({ avatarURL: "https://img.test/a.png" });
    expect((nameBox() as HTMLInputElement).value).toBe("Ada Lovelace!");
  });

  it("closing with an unchanged value sends nothing", async () => {
    const user = userEvent.setup();
    renderWithApp(<ProfileSection />);
    await user.click(screen.getByRole("button", { name: "Change avatar" }));
    await user.click(await screen.findByRole("button", { name: "Apply" }));
    expect(mocks.patchProfile).not.toHaveBeenCalled();
  });
});
