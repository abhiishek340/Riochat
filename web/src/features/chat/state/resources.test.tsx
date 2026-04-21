// @vitest-environment jsdom
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

const listModels = vi.hoisted(() => vi.fn());
vi.mock("../api", async (orig) => ({ ...(await orig<object>()), listModels }));

import { modelsResource, resetResources, useModelsState } from "./resources";

function Probe() {
  const s = useModelsState();
  return <p data-testid="out">{s.loaded ? `loaded:${s.data.length}` : "empty"}</p>;
}

afterEach(() => {
  cleanup();
  resetResources();
  listModels.mockReset();
});

describe("resource hooks", () => {
  it("load again after a reset while still mounted", async () => {
    listModels.mockResolvedValue([{ platformModelName: "m1" }]);
    render(<Probe />);
    await waitFor(() => expect(screen.getByTestId("out").textContent).toBe("loaded:1"));
    listModels.mockResolvedValue([{ platformModelName: "m1" }, { platformModelName: "m2" }]);
    act(() => resetResources());
    await waitFor(() => expect(screen.getByTestId("out").textContent).toBe("loaded:2"));
    expect(modelsResource.store.get().loaded).toBe(true);
    expect(listModels).toHaveBeenCalledTimes(2);
  });

  it("does not retry in a loop after a failure", async () => {
    listModels.mockRejectedValue(new Error("down"));
    render(<Probe />);
    await waitFor(() => expect(modelsResource.store.get().error).not.toBeNull());
    await new Promise((r) => setTimeout(r, 50));
    expect(listModels).toHaveBeenCalledTimes(1);
  });
});
