// @vitest-environment jsdom
import { cleanup, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, describe, expect, it } from "vitest";
import { clearToasts, renderWithApp, toastTitles } from "../test-utils";
import { useCapsDoc } from "./caps-doc";
import { WindowField } from "./window-field";

afterEach(() => {
  cleanup();
  clearToasts();
});

function Harness({ initial = "" }: { initial?: string }) {
  const [text, setText] = useState(initial);
  const doc = useCapsDoc(text, setText);
  return (
    <>
      <WindowField doc={doc} detected={200_000} fallback={128_000} />
      <pre data-testid="caps">{text}</pre>
    </>
  );
}

const stored = () => JSON.parse(document.querySelector("[data-testid=caps]")?.textContent || "{}") as Record<string, unknown>;

describe("WindowField", () => {
  it("shows the detected size as the placeholder in automatic mode", async () => {
    renderWithApp(<Harness />);
    const input = (await screen.findByRole("textbox", { name: "Context window" })) as HTMLInputElement;
    expect(input.value).toBe("");
    expect(input.placeholder).toBe("200,000");
  });

  it("commits a typed value on Enter with grouped digits and no automatic marker", async () => {
    const user = userEvent.setup();
    renderWithApp(<Harness initial={JSON.stringify({ context_window: 1, _riochatContextWindowMode: "auto" })} />);
    const input = (await screen.findByRole("textbox", { name: "Context window" })) as HTMLInputElement;
    await user.type(input, "65536{Enter}");
    expect(input.value).toBe("65,536");
    expect(stored()).toEqual({ contextWindow: 65536 });
  });

  it("refuses a value outside the range and puts the old one back", async () => {
    const user = userEvent.setup();
    renderWithApp(<Harness initial={JSON.stringify({ contextWindow: 64000 })} />);
    const input = (await screen.findByRole("textbox", { name: "Context window" })) as HTMLInputElement;
    expect(input.value).toBe("64,000");
    await user.clear(input);
    await user.type(input, "10{Enter}");
    expect(input.value).toBe("64,000");
    expect(toastTitles().length).toBe(1);
    expect(stored()).toEqual({ contextWindow: 64000 });
  });

  it("returns to automatic when the field is cleared", async () => {
    const user = userEvent.setup();
    renderWithApp(<Harness initial={JSON.stringify({ contextWindow: 64000, keep: true })} />);
    const input = (await screen.findByRole("textbox", { name: "Context window" })) as HTMLInputElement;
    await user.clear(input);
    await user.keyboard("{Enter}");
    expect(stored()).toEqual({ keep: true });
  });

  it("applies a preset", async () => {
    const user = userEvent.setup();
    renderWithApp(<Harness />);
    await user.click(await screen.findByRole("button", { name: "1M" }));
    expect(stored()).toEqual({ contextWindow: 1_000_000 });
  });
});
