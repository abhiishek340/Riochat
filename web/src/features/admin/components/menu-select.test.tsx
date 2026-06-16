// @vitest-environment jsdom
import { cleanup, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it } from "vitest";
import { renderWithApp } from "../test-utils";
import { MultiSelectMenu, SelectMenu } from "./menu-select";

afterEach(cleanup);

const options = [
  { value: "a", label: "Alpha" },
  { value: "b", label: "Beta" },
];

describe("menus announce their state", () => {
  it("marks the chosen option of a single choice", async () => {
    const user = userEvent.setup();
    renderWithApp(<SelectMenu value="b" onChange={() => undefined} options={options} label="Letter" />);
    await user.click(screen.getByRole("button", { name: "Letter: Beta" }));
    const items = await screen.findAllByRole("menuitemradio");
    expect(items.map((i) => i.getAttribute("aria-checked"))).toEqual(["false", "true"]);
  });

  it("marks every ticked option of a multiple choice", async () => {
    const user = userEvent.setup();
    renderWithApp(<MultiSelectMenu values={["a"]} onChange={() => undefined} options={options} label="Letters" />);
    await user.click(screen.getByRole("button", { name: "Letters: Alpha" }));
    const items = await screen.findAllByRole("menuitemcheckbox");
    expect(items.map((i) => i.getAttribute("aria-checked"))).toEqual(["true", "false"]);
  });
});
