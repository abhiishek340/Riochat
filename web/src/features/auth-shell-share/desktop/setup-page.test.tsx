// @vitest-environment jsdom
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { clearToasts, renderWithI18n, toastTitles } from "../test-utils";
import { FALLBACK_DISTRIBUTION, type Distribution } from "./distribution";

const shellMock = vi.hoisted(() => ({ setLocalServer: vi.fn(), setRemoteServer: vi.fn() }));
vi.mock("./bridge", async (orig) => ({ ...(await orig<object>()), shell: shellMock }));
const dist = vi.hoisted(() => ({ value: null as unknown }));
vi.mock("./distribution", async (orig) => ({ ...(await orig<object>()), loadDistribution: async () => dist.value }));
const probe = vi.hoisted(() => vi.fn());
vi.mock("./server-url", async (orig) => ({ ...(await orig<object>()), probeServer: probe }));

import { ShellError } from "./bridge";
import { ServerSetup } from "./setup-page";

const withPolicy = (policy: Partial<Distribution["policy"]>): Distribution => ({ ...FALLBACK_DISTRIBUTION, policy: { ...FALLBACK_DISTRIBUTION.policy, ...policy } });

beforeEach(() => {
  Object.values(shellMock).forEach((fn) => fn.mockReset());
  probe.mockReset();
  dist.value = withPolicy({});
  clearToasts();
});
afterEach(cleanup);

async function openRemote() {
  await userEvent.click(await screen.findByRole("button", { name: "Connect to a server" }));
  return screen.getByLabelText(/server address/i) as HTMLInputElement;
}

describe("ServerSetup", () => {
  it("starts the bundled server and reports success", async () => {
    shellMock.setLocalServer.mockResolvedValue({ mode: "local", origin: "http://127.0.0.1:1" });
    const onBound = vi.fn();
    renderWithI18n(<ServerSetup onBound={onBound} />);
    await userEvent.click(await screen.findByRole("button", { name: "Use on this computer" }));
    await waitFor(() => expect(onBound).toHaveBeenCalledWith("local"));
  });

  it("maps shell refusals to clear messages", async () => {
    renderWithI18n(<ServerSetup onBound={vi.fn()} />);
    shellMock.setLocalServer.mockRejectedValueOnce(new ShellError("tabs", "dup"));
    await userEvent.click(await screen.findByRole("button", { name: "Use on this computer" }));
    await waitFor(() => expect(toastTitles()).toContain("That server is already open in another tab."));
    shellMock.setLocalServer.mockRejectedValueOnce(new ShellError("other", "policy: local mode disabled"));
    await userEvent.click(screen.getByRole("button", { name: "Use on this computer" }));
    await waitFor(() => expect(toastTitles()).toContain("Your organisation does not allow the built-in server."));
    shellMock.setLocalServer.mockRejectedValueOnce(new ShellError("sidecar", "no port"));
    await userEvent.click(screen.getByRole("button", { name: "Use on this computer" }));
    await waitFor(() => expect(toastTitles()).toContain("The built-in server could not start."));
  });

  it("skips the choice when the organisation forbids the local server", async () => {
    dist.value = withPolicy({ localModeAllowed: false });
    renderWithI18n(<ServerSetup onBound={vi.fn()} />);
    await screen.findByLabelText(/server address/i);
    expect(screen.queryByRole("button", { name: "Use on this computer" })).toBeNull();
    expect(screen.queryByRole("button", { name: /back/i })).toBeNull();
    expect(screen.getByText(/does not allow the built-in server on this computer/i)).toBeTruthy();
  });

  it("shows a locked default address read-only with a note", async () => {
    dist.value = withPolicy({ defaultServerUrl: "https://chat.corp.example", serverUrlLocked: true });
    renderWithI18n(<ServerSetup onBound={vi.fn()} />);
    const input = await openRemote();
    expect(input.value).toBe("https://chat.corp.example");
    expect(input.readOnly).toBe(true);
    expect(screen.getByText("This address is set by your organisation.")).toBeTruthy();
  });

  it("checks the server before remembering it, and normalises the address", async () => {
    probe.mockResolvedValue({ kind: "ok" });
    shellMock.setRemoteServer.mockResolvedValue({ mode: "remote", origin: "https://chat.example.com" });
    const onBound = vi.fn();
    renderWithI18n(<ServerSetup onBound={onBound} />);
    const input = await openRemote();
    await userEvent.type(input, "HTTPS://Chat.Example.com:443/");
    await userEvent.click(screen.getByRole("button", { name: "Connect" }));
    await waitFor(() => expect(onBound).toHaveBeenCalledWith("remote"));
    expect(probe).toHaveBeenCalledWith("https://chat.example.com");
    expect(shellMock.setRemoteServer).toHaveBeenCalledWith("https://chat.example.com");
  });

  it.each([
    [{ kind: "timeout" }, "The server did not answer within 10 seconds."],
    [{ kind: "network" }, "The server could not be reached. Check the address and your connection."],
    [{ kind: "status", status: 503 }, "The server answered with status 503."],
  ])("does not pin a server that fails the check (%o)", async (result, message) => {
    probe.mockResolvedValue(result);
    renderWithI18n(<ServerSetup onBound={vi.fn()} />);
    await userEvent.type(await openRemote(), "https://down.example.com");
    await userEvent.click(screen.getByRole("button", { name: "Connect" }));
    await waitFor(() => expect(toastTitles()).toContain(message));
    expect(shellMock.setRemoteServer).not.toHaveBeenCalled();
  });

  it("rejects an address that is not an origin without probing", async () => {
    renderWithI18n(<ServerSetup onBound={vi.fn()} />);
    await userEvent.type(await openRemote(), "https://chat.example.com/api");
    await userEvent.click(screen.getByRole("button", { name: "Connect" }));
    await waitFor(() => expect(toastTitles().some((t) => t.startsWith("Enter a full address"))).toBe(true));
    expect(probe).not.toHaveBeenCalled();
  });

  it("explains when the same server is open in another tab", async () => {
    probe.mockResolvedValue({ kind: "ok" });
    shellMock.setRemoteServer.mockRejectedValue(new ShellError("tabs", "dup"));
    renderWithI18n(<ServerSetup onBound={vi.fn()} />);
    await userEvent.type(await openRemote(), "https://chat.example.com");
    await userEvent.click(screen.getByRole("button", { name: "Connect" }));
    await waitFor(() => expect(toastTitles()).toContain("That server is already open in another tab."));
  });
});
