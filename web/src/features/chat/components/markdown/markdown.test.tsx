// @vitest-environment jsdom
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import type { ReactElement } from "react";
import { TooltipProvider } from "@/components/ui/tooltip";
import { I18nProvider } from "@/lib/i18n/provider";
import { linkConfirmStore } from "../../state/link-confirm";
import { ExternalLinkDialog } from "./external-link-dialog";
import { Markdown } from "./markdown";

afterEach(cleanup);

function view(ui: ReactElement) {
  return render(
    <I18nProvider>
      <TooltipProvider>{ui}</TooltipProvider>
    </I18nProvider>,
  );
}

describe("Markdown", () => {
  it("renders headings, emphasis, lists and inline code", () => {
    const { container } = view(<Markdown content={"# Title\n\nSome **bold** and `code`.\n\n- one\n- two"} />);
    expect(container.querySelector("h1")?.textContent).toBe("Title");
    expect(container.querySelector("strong")?.textContent).toBe("bold");
    expect(container.querySelectorAll("li")).toHaveLength(2);
    expect(container.querySelector("code")?.textContent).toContain("code");
  });

  it("renders tables with header cells", () => {
    const { container } = view(<Markdown content={"| a | b |\n|---|--:|\n| 1 | 2 |"} />);
    expect(container.querySelectorAll("th")).toHaveLength(2);
    expect(container.querySelectorAll("td")).toHaveLength(2);
  });

  it("shows fenced code with its language and keeps the source text", () => {
    const { container } = view(<Markdown content={"```python\nprint('hi')\n```"} />);
    expect(container.textContent).toContain("print('hi')");
    expect(container.textContent?.toLowerCase()).toContain("python");
  });

  it("closes an unfinished code fence while streaming", () => {
    const { container } = view(<Markdown content={"text\n\n```js\nconst a = 1"} streaming />);
    expect(container.textContent).toContain("const a = 1");
  });

  it("renders math as MathML", () => {
    const { container } = view(<Markdown content={"Inline $x^2$ here."} />);
    expect(container.querySelector("math")).not.toBeNull();
  });

  it("asks before opening an external link", async () => {
    view(
      <>
        <Markdown content={"[site](https://example.com/page)"} />
        <ExternalLinkDialog />
      </>,
    );
    fireEvent.click(screen.getByText("site"));
    expect(await screen.findByText("https://example.com/page")).toBeTruthy();
    linkConfirmStore.set(null);
  });

  it("does not turn raw html into elements", () => {
    const { container } = view(<Markdown content={'<img src=x onerror="alert(1)"> <b>raw</b>'} />);
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector("b")).toBeNull();
    expect(container.textContent).toContain("raw");
  });

  it("splits a leading think block out of the answer", () => {
    const { container } = view(<Markdown content={"<think>plan the answer</think>Final answer"} />);
    expect(container.textContent).toContain("Final answer");
    expect(container.textContent).not.toContain("plan the answer");
    fireEvent.click(screen.getByRole("button", { name: /thought/i }));
    expect(container.textContent).toContain("plan the answer");
  });
});
