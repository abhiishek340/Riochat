// @vitest-environment jsdom
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const api = vi.hoisted(() => ({ fetchAccess: vi.fn() }));
vi.mock("./api", () => api);
vi.mock("@/lib/session/provider", () => ({ useSession: () => ({ user: { publicID: "u_1" }, accessToken: "tok" }) }));

import { PROVIDER_ACCESS_CHANGED_EVENT, useProviderAccess } from "./use-provider-access";

beforeEach(() => api.fetchAccess.mockReset());
afterEach(cleanup);

describe("useProviderAccess", () => {
  it("asks again when the console reports a policy change", async () => {
    api.fetchAccess.mockResolvedValueOnce({ enabled: false, maxPerUser: 0, protocols: [], modelProtocols: [] });
    const { result } = renderHook(() => useProviderAccess());
    await waitFor(() => expect(result.current.access?.enabled).toBe(false));
    api.fetchAccess.mockResolvedValueOnce({ enabled: true, maxPerUser: 5, protocols: [], modelProtocols: [] });
    act(() => {
      window.dispatchEvent(new CustomEvent(PROVIDER_ACCESS_CHANGED_EVENT));
    });
    await waitFor(() => expect(result.current.access?.enabled).toBe(true));
    expect(api.fetchAccess).toHaveBeenCalledTimes(2);
  });
});
