import { describe, expect, it, vi } from "vitest";
import { normalizeOrigin, probeServer } from "./server-url";

describe("normalizeOrigin", () => {
  it("accepts absolute http(s) origins and canonicalises them", () => {
    expect(normalizeOrigin("HTTPS://Chat.Example.com:443/")).toBe("https://chat.example.com");
    expect(normalizeOrigin(" http://127.0.0.1:8080/ ")).toBe("http://127.0.0.1:8080");
    expect(normalizeOrigin("https://chat.example.com")).toBe("https://chat.example.com");
  });
  it.each(["chat.example.com", "ftp://x", "https://u:p@x", "https://x/api", "https://x/?q=1", "https://x/#f", "javascript:alert(1)", "", "   "])("rejects %s", (input) => {
    expect(normalizeOrigin(input)).toBe("");
  });
});

describe("probeServer", () => {
  it("is ok for a 2xx health check and asks the health endpoint without caching", async () => {
    const fetchImpl = vi.fn(async () => new Response("{}", { status: 200 }));
    expect(await probeServer("https://x.example", { fetchImpl: fetchImpl as never })).toEqual({ kind: "ok" });
    expect(fetchImpl).toHaveBeenCalledWith("https://x.example/healthz", expect.objectContaining({ cache: "no-store" }));
  });
  it("reports the status of a non-2xx answer", async () => {
    const fetchImpl = vi.fn(async () => new Response("", { status: 503 }));
    expect(await probeServer("https://x.example", { fetchImpl: fetchImpl as never })).toEqual({ kind: "status", status: 503 });
  });
  it("tells a network failure from a timeout", async () => {
    const failing = vi.fn(async () => {
      throw new TypeError("failed to fetch");
    });
    expect(await probeServer("https://x.example", { fetchImpl: failing as never })).toEqual({ kind: "network" });
    const hanging = vi.fn((_url: string, init: RequestInit) => new Promise<Response>((_res, rej) => init.signal?.addEventListener("abort", () => rej(new DOMException("aborted", "AbortError")))));
    expect(await probeServer("https://x.example", { fetchImpl: hanging as never, timeoutMs: 20 })).toEqual({ kind: "timeout" });
  });
});
