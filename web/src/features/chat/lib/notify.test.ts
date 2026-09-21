import { describe, expect, it } from "vitest";
import { notificationBody } from "./notify";

describe("notificationBody", () => {
  it("collapses whitespace and shortens to 140 characters with an ellipsis", () => {
    expect(notificationBody("  hello \n\n world  ", "fallback")).toBe("hello world");
    const long = notificationBody("x".repeat(300), "fallback");
    expect(Array.from(long)).toHaveLength(140);
    expect(long.endsWith("…")).toBe(true);
    expect(notificationBody("   ", "Your reply is ready.")).toBe("Your reply is ready.");
    expect(notificationBody("y".repeat(140), "f")).toBe("y".repeat(140));
  });
});
