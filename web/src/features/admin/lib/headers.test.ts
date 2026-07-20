import { describe, expect, it } from "vitest";
import { fillHeaders, HEADER_TEMPLATES } from "./headers";

describe("fillHeaders", () => {
  it("adds to empty text", () => {
    const out = fillHeaders("", HEADER_TEMPLATES.sessionId);
    expect(out.kind).toBe("ok");
    if (out.kind === "ok") expect(JSON.parse(out.text)).toEqual({ "X-Session-Id": "${RIOCHAT_SESSION_ID}" });
  });
  it("refuses text that is not a JSON object", () => {
    expect(fillHeaders("not json", HEADER_TEMPLATES.sessionId).kind).toBe("invalid");
    expect(fillHeaders("[1]", HEADER_TEMPLATES.sessionId).kind).toBe("invalid");
  });
  it("matches names case-insensitively and does not overwrite in skip mode", () => {
    expect(fillHeaders('{"x-session-id":"mine"}', HEADER_TEMPLATES.sessionId).kind).toBe("unchanged");
  });
  it("reports nothing to do when the same value is present", () => {
    expect(fillHeaders('{"X-Session-Id":"${RIOCHAT_SESSION_ID}"}', HEADER_TEMPLATES.sessionId, "strict").kind).toBe("unchanged");
  });
  it("refuses a strict preset that collides with a different value", () => {
    const out = fillHeaders('{"session-id":"abc"}', HEADER_TEMPLATES.codexAffinity, "strict");
    expect(out).toEqual({ kind: "conflict", names: ["session-id"] });
  });
  it("adds only the missing members of a preset", () => {
    const out = fillHeaders('{"session-id":"${RIOCHAT_SESSION_ID}"}', HEADER_TEMPLATES.codexAffinity, "strict");
    expect(out.kind).toBe("ok");
    if (out.kind === "ok") expect(out.added).toEqual(["thread-id", "x-client-request-id"]);
  });
});
