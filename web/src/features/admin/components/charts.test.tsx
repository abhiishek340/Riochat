// @vitest-environment jsdom
import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { BarList, LineChart } from "./charts";

afterEach(cleanup);

describe("charts", () => {
  it("labels every focusable point of a line chart and keeps the group named", () => {
    render(
      <LineChart
        label="Usage"
        format={(v) => `${v} calls`}
        points={[
          { label: "Jan 1", value: 3 },
          { label: "Jan 2", value: 5 },
        ]}
      />,
    );
    expect(screen.getByRole("group", { name: "Usage" })).toBeTruthy();
    expect(screen.getByRole("img", { name: "Jan 2: 5 calls" }).getAttribute("tabindex")).toBe("0");
  });

  it("says so when a line chart has nothing to draw", () => {
    render(<LineChart label="Usage" format={String} points={[]} empty="No data in this range" />);
    expect(screen.getByText("No data in this range")).toBeTruthy();
    expect(screen.queryByRole("group")).toBeNull();
  });

  it("shows the empty text for an empty ranking", () => {
    render(<BarList items={[]} format={String} empty="Nothing yet" />);
    expect(screen.getByText("Nothing yet")).toBeTruthy();
  });
});
