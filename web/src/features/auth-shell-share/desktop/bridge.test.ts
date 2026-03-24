// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import { ShellError, invoke, listen, openExternal, readCredentials, readShellServer, toShellError } from "./bridge";

afterEach(() => {
  delete (window as unknown as Record<string, unknown>).__RIOCHAT_DESKTOP__;
  delete (window as unknown as Record<string, unknown>).__TAURI_INTERNALS__;
  vi.restoreAllMocks();
});

describe("toShellError", () => {
  it("understands plain strings, policy strings and structured errors", () => {
    expect(toShellError("boom")).toMatchObject({ kind: "other", message: "boom" });
    expect(toShellError("policy: local mode is disabled")).toMatchObject({ kind: "policy" });
    expect(toShellError({ kind: "http", message: "bad", status: 401, errorCode: "auth.invalid_refresh_token" })).toMatchObject({
      kind: "http",
      status: 401,
      errorCode: "auth.invalid_refresh_token",
    });
    expect(toShellError({ kind: "nonsense", message: "x" }).kind).toBe("other");
    expect(toShellError(new Error("e")).message).toBe("e");
    const existing = new ShellError("tabs", "dup");
    expect(toShellError(existing)).toBe(existing);
  });
});

describe("payload readers", () => {
  it("validates the bound server", () => {
    expect(readShellServer({ mode: "local", origin: "http://127.0.0.1:5000" })).toEqual({ mode: "local", origin: "http://127.0.0.1:5000" });
    expect(readShellServer(null)).toBeNull();
    expect(readShellServer({ mode: "other", origin: "x" })).toBeNull();
    expect(readShellServer({ mode: "remote", origin: "" })).toBeNull();
  });
  it("needs an access token in credentials", () => {
    expect(readCredentials({ accessToken: "a", sessionID: "s" })).toEqual({ accessToken: "a", sessionID: "s" });
    expect(readCredentials({ accessToken: "", sessionID: "s" })).toBeNull();
    expect(readCredentials("x")).toBeNull();
  });
});

describe("invoke", () => {
  it("is unavailable outside the desktop shell", async () => {
    await expect(invoke("get_server")).rejects.toMatchObject({ kind: "unavailable" });
  });
  it("calls the shell and converts its failures", async () => {
    const call = vi.fn(async (cmd: string) => {
      if (cmd === "bad") throw { kind: "tabs", message: "already open" };
      return { ok: cmd };
    });
    (window as unknown as Record<string, unknown>).__RIOCHAT_DESKTOP__ = true;
    (window as unknown as Record<string, unknown>).__TAURI_INTERNALS__ = { invoke: call };
    expect(await invoke("get_server", { a: 1 })).toEqual({ ok: "get_server" });
    expect(call).toHaveBeenCalledWith("get_server", { a: 1 });
    await expect(invoke("bad")).rejects.toMatchObject({ kind: "tabs", message: "already open" });
  });
  it("subscribes to events through the event plugin and unsubscribes", async () => {
    let handler: ((message: unknown) => void) | null = null;
    const call = vi.fn(async (cmd: string, args?: Record<string, unknown>) => {
      if (cmd === "plugin:event|listen") return 42 + (args ? 0 : 0);
      return null;
    });
    (window as unknown as Record<string, unknown>).__RIOCHAT_DESKTOP__ = true;
    (window as unknown as Record<string, unknown>).__TAURI_INTERNALS__ = {
      invoke: call,
      transformCallback: (cb: (m: unknown) => void) => {
        handler = cb;
        return 7;
      },
    };
    const received: unknown[] = [];
    const off = await listen("downloads:finished", (p) => received.push(p));
    handler!({ event: "downloads:finished", id: 1, payload: { fileName: "a.txt", success: true } });
    expect(received).toEqual([{ fileName: "a.txt", success: true }]);
    off();
    expect(call).toHaveBeenCalledWith("plugin:event|unlisten", { event: "downloads:finished", eventId: 42 });
  });
});

describe("openExternal", () => {
  it("refuses anything but http(s)", async () => {
    expect(await openExternal("javascript:alert(1)")).toBe(false);
    expect(await openExternal("file:///etc/passwd")).toBe(false);
  });
  it("opens http(s) links in a new context without an opener", async () => {
    const open = vi.spyOn(window, "open").mockReturnValue({} as Window);
    expect(await openExternal("https://example.com")).toBe(true);
    expect(open).toHaveBeenCalledWith("https://example.com", "_blank", "noopener,noreferrer");
  });
  it("asks the shell to open it on desktop", async () => {
    const call = vi.fn(async () => null);
    (window as unknown as Record<string, unknown>).__RIOCHAT_DESKTOP__ = true;
    (window as unknown as Record<string, unknown>).__TAURI_INTERNALS__ = { invoke: call };
    expect(await openExternal("https://example.com/r")).toBe(true);
    expect(call).toHaveBeenCalledWith("plugin:opener|open_url", { url: "https://example.com/r" });
  });
});
