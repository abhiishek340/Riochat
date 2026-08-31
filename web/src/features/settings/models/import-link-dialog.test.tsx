// @vitest-environment jsdom
import { cleanup, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderWithApp } from "../test-utils";

vi.mock("@/lib/api/base-url", async (orig) => ({ ...(await orig<object>()), resolveApiBase: () => "https://api.example.test" }));
vi.mock("@/lib/api/platform", async (orig) => ({ ...(await orig<object>()), publicWebOrigin: () => "https://chat.example.test/" }));

import { ImportLinkDialog } from "./import-link-dialog";

afterEach(cleanup);

const linkField = () => screen.getByRole("textbox", { name: /link/i }) as HTMLInputElement;

describe("ImportLinkDialog", () => {
  it("builds a version 1 link with readable placeholders while the form is empty", async () => {
    renderWithApp(<ImportLinkDialog open onOpenChange={() => undefined} />);
    expect(await screen.findByRole("dialog")).toBeTruthy();
    expect(linkField().value).toBe("https://chat.example.test/import#v=1&url={address}&key={key}");
  });

  it("gives every parameter input an accessible name", async () => {
    renderWithApp(<ImportLinkDialog open onOpenChange={() => undefined} />);
    await screen.findByRole("dialog");
    for (const name of [/^url/, /^key/, /^name/]) expect(screen.getByRole("textbox", { name })).toBeTruthy();
    expect(screen.getByRole("combobox", { name: /^protocol/ })).toBeTruthy();
  });

  it("switches to version 2 with two groups and keeps a {key} placeholder for the empty one", async () => {
    const user = userEvent.setup();
    renderWithApp(<ImportLinkDialog open onOpenChange={() => undefined} />);
    await user.type(await screen.findByRole("textbox", { name: /^url/ }), "https://a.test/v1");
    await user.type(screen.getByRole("textbox", { name: /^key/ }), "k 1");
    await user.click(screen.getByRole("button", { name: "Add entry" }));
    const urls = screen.getAllByRole("textbox", { name: /^url/ });
    await user.type(urls[1] as HTMLElement, "https://b.test");
    expect(linkField().value).toBe("https://chat.example.test/import#v=2&url=https%3A%2F%2Fa.test%2Fv1&key=k%201&url=https%3A%2F%2Fb.test&key={key}");
  });

  it("removes an entry only when several exist and goes back to version 1", async () => {
    const user = userEvent.setup();
    renderWithApp(<ImportLinkDialog open onOpenChange={() => undefined} />);
    await screen.findByRole("dialog");
    expect(screen.queryByRole("button", { name: /^Remove/ })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Add entry" }));
    await user.click(screen.getAllByRole("button", { name: /^Remove/ })[0] as HTMLElement);
    expect(linkField().value).toContain("#v=1&");
    expect(screen.queryByRole("button", { name: /^Remove/ })).toBeNull();
  });
});
