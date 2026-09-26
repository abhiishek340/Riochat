// @vitest-environment jsdom
import { cleanup, fireEvent, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { BarChart } from "./bar-chart";

afterEach(cleanup);

describe("BarChart", () => {
  it("ties the focused column to its tooltip so the numbers are announced", () => {
    const { container } = render(
      <BarChart
        data={[
          { id: "a", label: "3/1", values: { tokens: 10 } },
          { id: "b", label: "3/2", values: { tokens: 20 } },
        ]}
        series={[{ key: "tokens", label: "Tokens", color: "red" }]}
        hidden={new Set()}
        label="Usage"
        tooltip={(d) => <span>detail {d.label}</span>}
      />,
    );
    const columns = Array.from(container.querySelectorAll<SVGRectElement>("rect[tabindex='0']"));
    expect(columns).toHaveLength(2);
    expect(columns[0]?.getAttribute("aria-describedby")).toBeNull();
    fireEvent.focus(columns[1] as SVGRectElement);
    const id = (columns[1] as SVGRectElement).getAttribute("aria-describedby");
    expect(id).toBeTruthy();
    expect(container.querySelector(`[id="${id}"]`)?.textContent).toBe("detail 3/2");
    fireEvent.blur(columns[1] as SVGRectElement);
    expect(container.querySelector(`[id="${id}"]`)).toBeNull();
  });
});
