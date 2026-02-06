import { describe, expect, it } from "vitest";
import { classifyAuthError } from "./errors";

describe("classifyAuthError", () => {
  it("separates session-ending 401s from recoverable ones", () => {
    expect(classifyAuthError({ status: 401, errorCode: "auth.invalid_refresh_token" })).toBe("session_terminated");
    expect(classifyAuthError({ status: 401, errorCode: "auth.session_invalid" })).toBe("session_terminated");
    expect(classifyAuthError({ status: 401 })).toBe("unauthorized");
    expect(classifyAuthError({ status: 403 })).toBe("other");
    expect(classifyAuthError({})).toBe("other");
    expect(classifyAuthError(undefined)).toBe("other");
  });
});
