// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { CSP, cssDocument, escapeScript, escapeStyle, htmlDocument, javascriptDocument, previewDocument, themeStyle, validateSvg } from "./sandbox-doc";

const theme = { variables: { "--background": "#fff", "--evil": "red;} body{display:none", "--primary": "oklch(0.5 0.1 200)" }, colorScheme: "dark" as const };

describe("documents", () => {
  it("puts the CSP first in a full document and keeps user markup", () => {
    const doc = htmlDocument("<!doctype html><html><head><title>t</title></head><body><p>hi</p></body></html>", theme);
    expect(doc.indexOf("Content-Security-Policy")).toBeLessThan(doc.indexOf("<title>t</title>"));
    expect(doc).toContain("<p>hi</p>");
    expect(doc).toContain(CSP);
  });

  it("wraps fragments and adds a head to head-less html", () => {
    const frag = htmlDocument("<div>x</div>", theme);
    expect(frag.startsWith("<!doctype html><html><head>")).toBe(true);
    expect(frag).toContain("<body><div>x</div></body>");
    const noHead = htmlDocument("<html><body>x</body></html>", theme);
    expect(noHead.indexOf("<head>")).toBeLessThan(noHead.indexOf("<body>"));
  });

  it("denies network and frames in the policy", () => {
    for (const d of ["connect-src 'none'", "frame-src 'none'", "worker-src 'none'", "form-action 'none'", "base-uri 'none'", "object-src 'none'", "default-src 'none'"]) {
      expect(CSP).toContain(d);
    }
  });

  it("neutralises closing tags inside injected style and script", () => {
    expect(escapeStyle("a{}</style><script>x</script>")).not.toContain("</style");
    expect(escapeScript("var s='</script><b>'")).not.toContain("</script");
    const css = cssDocument("body{}</style><script>alert(1)</script>", theme);
    expect(css.match(/<\/style>/g)?.length).toBe(3);
    // Parsed as a document, the injected text stays inside the style element: only our own script exists.
    expect(new DOMParser().parseFromString(css, "text/html").querySelectorAll("script")).toHaveLength(1);
    const js = javascriptDocument("console.log('</script><img src=x onerror=alert(1)>')", theme);
    expect((js.match(/<\/script>/g) ?? []).length).toBe(3);
    expect(new DOMParser().parseFromString(js, "text/html").querySelectorAll("img")).toHaveLength(0);
  });

  it("keeps only well-formed theme variables with safe values", () => {
    const style = themeStyle(theme);
    expect(style).toContain("--background:#fff");
    expect(style).toContain("color-scheme:dark");
    expect(style).not.toContain("--evil");
  });

  it("mirrors console output in javascript previews and routes kinds", () => {
    const doc = previewDocument("javascript", "console.log(1)", theme);
    expect(doc).toContain('id="console"');
    expect(doc).toContain("console.log(1)");
    expect(previewDocument("css", "p{color:red}", theme)).toContain("p{color:red}");
  });
});

describe("svg", () => {
  it("accepts valid markup and reports invalid markup", () => {
    expect(validateSvg('<svg xmlns="http://www.w3.org/2000/svg"><circle r="1"/></svg>')).toEqual({ ok: true });
    const bad = validateSvg("<svg><circle></svg>");
    expect(bad.ok).toBe(false);
    expect(validateSvg("<div/>").ok).toBe(false);
  });
});
