// @vitest-environment jsdom
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { renderWithApp } from "../test-utils";
import { CapsEditor } from "./caps-editor";

vi.mock("./api", async (orig) => ({ ...(await orig<object>()), loadNativeToolCatalog: vi.fn().mockResolvedValue([]) }));

afterEach(cleanup);

function Harness({ initial = "", onProblem }: { initial?: string; onProblem?: (p: unknown) => void }) {
  const [text, setText] = useState(initial);
  return (
    <>
      <CapsEditor text={text} setText={setText} resetKey={0} routeProtocols={["openai_responses"]} onProblem={(p) => onProblem?.(p)} />
      <pre data-testid="caps">{text}</pre>
    </>
  );
}

const stored = () => JSON.parse(document.querySelector("[data-testid=caps]")?.textContent || "{}") as Record<string, unknown>;

describe("CapsEditor", () => {
  it("writes a new option control with its default into the capabilities", async () => {
    const user = userEvent.setup();
    renderWithApp(<Harness initial={JSON.stringify({ keep: 1 })} />);
    await user.click(await screen.findByRole("button", { name: /User-adjustable options/ }));
    await user.click(screen.getByRole("button", { name: "Add option" }));
    await user.type(screen.getByRole("textbox", { name: "Option path" }), "temperature");
    await user.selectOptions(screen.getByRole("combobox", { name: "Control" }), "number");
    await user.type(screen.getByRole("textbox", { name: "Default value" }), "0.7");
    await waitFor(() =>
      expect(stored()).toEqual({ keep: 1, defaultOptions: { temperature: 0.7 }, optionControls: [{ path: "temperature", type: "number" }] }),
    );
  });

  it("reports the first problem while a row is incomplete", async () => {
    const user = userEvent.setup();
    const seen: unknown[] = [];
    renderWithApp(<Harness onProblem={(p) => seen.push(p)} />);
    await user.click(await screen.findByRole("button", { name: /User-adjustable options/ }));
    await user.click(screen.getByRole("button", { name: "Add option" }));
    await waitFor(() => expect(seen.at(-1)).toMatchObject({ key: "adminModels.caps.issues.path" }));
    await user.type(screen.getByRole("textbox", { name: "Option path" }), "a.b");
    await waitFor(() => expect(seen.at(-1)).toBeNull());
  });

  it("refuses visual edits while the text is not a JSON object", async () => {
    renderWithApp(<Harness initial="[1,2]" />);
    expect(await screen.findByText(/not a JSON object/)).toBeTruthy();
    const advanced = screen.getByRole("button", { name: /Advanced JSON/ });
    expect(advanced).toBeTruthy();
  });

  it("rebuilds the rows when the advanced text is replaced", async () => {
    const user = userEvent.setup();
    renderWithApp(<Harness />);
    await user.click(await screen.findByRole("button", { name: /Advanced JSON/ }));
    await user.click(screen.getByRole("textbox", { name: "Advanced JSON" }));
    await user.paste(JSON.stringify({ optionControls: [{ path: "x.y", type: "boolean" }] }));
    await user.click(screen.getByRole("button", { name: /User-adjustable options/ }));
    expect((screen.getByRole("textbox", { name: "Option path" }) as HTMLInputElement).value).toBe("x.y");
  });
});
