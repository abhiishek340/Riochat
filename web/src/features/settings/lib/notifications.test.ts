import { describe, expect, it } from "vitest";
import { completionNotificationBody, NOTIFICATION_BODY_LIMIT, shouldShowCompletionNotification } from "./notifications";

describe("completionNotificationBody", () => {
  it("collapses whitespace", () => {
    expect(completionNotificationBody("  hello \n\n  world\t!", "fallback")).toBe("hello world !");
  });
  it("falls back when empty", () => {
    expect(completionNotificationBody(" \n ", "Your reply is ready.")).toBe("Your reply is ready.");
  });
  it("cuts at 140 characters with an ellipsis", () => {
    const long = "x".repeat(300);
    const out = completionNotificationBody(long, "f");
    expect(Array.from(out)).toHaveLength(NOTIFICATION_BODY_LIMIT);
    expect(out.endsWith("…")).toBe(true);
    expect(completionNotificationBody("y".repeat(140), "f")).toHaveLength(140);
  });
});

describe("shouldShowCompletionNotification", () => {
  const on = { enabled: true, permission: "granted" as const, visible: false, focused: false };
  it("shows only when enabled, granted and the window is not both visible and focused", () => {
    expect(shouldShowCompletionNotification(on)).toBe(true);
    expect(shouldShowCompletionNotification({ ...on, visible: true, focused: true })).toBe(false);
    expect(shouldShowCompletionNotification({ ...on, visible: true, focused: false })).toBe(true);
    expect(shouldShowCompletionNotification({ ...on, enabled: false })).toBe(false);
    expect(shouldShowCompletionNotification({ ...on, permission: "denied" })).toBe(false);
  });
});
