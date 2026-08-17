import { describe, expect, it } from "vitest";
import { parseBlock, type Block } from "./block";
import { buttonAction, declaredState, diffState, isLiveBlock, mergeState, resolveLive, type ResolvedNode } from "./live";

function block(src: string): Block {
  const r = parseBlock(src, false);
  if (r.kind !== "ok") throw new Error(JSON.stringify(r));
  return r.block;
}

function resolve(src: string, stored?: Record<string, unknown>) {
  const b = block(src);
  const declared = declaredState(b).state;
  return { b, res: resolveLive(b, mergeState(declared, stored)), declared };
}

const find = (n: ResolvedNode | null, component: string): ResolvedNode | undefined => {
  if (!n) return undefined;
  if (n.component === component) return n;
  for (const c of n.children) {
    const f = find(c, component);
    if (f) return f;
  }
  return undefined;
};

describe("state", () => {
  it("merges stored values only for declared names of the same shape", () => {
    expect(mergeState({ n: 1, s: "a" }, { n: "x", s: "b", z: 1 })).toEqual({ n: 1, s: "b" });
    expect(mergeState({ n: null, l: [1] }, { n: 5, l: "no" })).toEqual({ n: 5, l: [1] });
    expect(mergeState({ o: { a: 1 } }, { o: [1] })).toEqual({ o: { a: 1 } });
  });

  it("diffs against the declared values", () => {
    expect(diffState({ n: 1, s: "a" }, { n: 2, s: "a" })).toEqual({ n: 2 });
    expect(diffState({ l: [1, 2] }, { l: [1, 2] })).toEqual({});
  });

  it("drops invalid names and values with an issue, and adds implicit state for bound controls", () => {
    const b = block('{"component":"panel","state":{"ok":1,"bad name":2,"deep":[[[[[1]]]]]},"children":[{"component":"slider","bind":"vol","min":3},{"component":"switch","bind":"on"},{"component":"select","bind":"pick","options":[{"value":"a"},"b"]},{"component":"input","type":"number","bind":"qty"},{"component":"tabs","bind":"tab","items":["one","two"]}]}');
    const d = declaredState(b);
    expect(d.state).toEqual({ ok: 1, vol: 3, on: false, pick: "a", qty: 0, tab: "one" });
    expect(d.issues).toHaveLength(2);
  });

  it("binds a control through value: {name} when it has no bind", () => {
    const { res } = resolve('{"component":"panel","state":{"n":4},"children":[{"component":"slider","value":"{n}","min":0,"max":9}]}');
    expect(find(res.root, "slider")?.bind).toBe("n");
  });
});

describe("resolution", () => {
  it("computes values and resolves templates", () => {
    const { res } = resolve('{"component":"panel","state":{"years":5},"computed":{"total":"years * 12"},"children":[{"component":"text","text":"total {total} months"}]}');
    expect(find(res.root, "text")?.props.text).toBe("total 60 months");
    expect(res.values.total).toBe(60);
    expect(res.issues).toEqual([]);
  });

  it("unwraps braces around computed formulas and supports constants", () => {
    const { res } = resolve('{"component":"panel","state":{"a":2},"computed":{"b":"{a + 1}","table":[1,2,3]},"children":[{"component":"text","text":"{table[b - 1]}"}]}');
    expect(res.values.b).toBe(3);
    expect(find(res.root, "text")?.props.text).toBe(3);
  });

  it("hides a node whose visible is false or fails", () => {
    const { res } = resolve('{"component":"panel","state":{"a":1},"children":[{"component":"text","text":"x","visible":"a > 5"},{"component":"text","text":"y","visible":"nope + 1"},{"component":"text","text":"z","visible":"{a == 1}"}]}');
    expect(res.root?.children.map((c) => c.props.text)).toEqual(["z"]);
    expect(res.issues.some((i) => /nope/.test(i.message))).toBe(true);
  });

  it("reports cycles and fails both entries", () => {
    const { res } = resolve('{"component":"panel","computed":{"a":"b+1","b":"a+1"},"children":[{"component":"text","text":"v {a}"},{"component":"stat","value":"{b}","label":"x"}]}');
    expect(res.issues.some((i) => /cycle/.test(i.message))).toBe(true);
    expect("a" in res.values || "b" in res.values).toBe(false);
    expect(find(res.root, "text")?.props.text).toBe("v —");
    expect("value" in (find(res.root, "stat")?.props ?? {})).toBe(false);
  });

  it("keeps per-point formulas for charts: x and i, non-finite becomes a gap", () => {
    const { res } = resolve('{"component":"chart","type":"line","x":[1,2,3],"series":[{"name":"s","data":"{x * 2}"},{"name":"r","data":"{1/(x-2)}"}]}');
    const series = res.root?.props.series as { data: (number | null)[] }[];
    expect(series[0]?.data).toEqual([2, 4, 6]);
    expect(series[1]?.data).toEqual([-1, null, 1]);
  });

  it("asks for x when a per-point formula has none", () => {
    const { res } = resolve('{"component":"chart","type":"line","series":[{"name":"s","data":"{x}"}]}');
    expect((res.root?.props.series as { data: unknown[] }[])[0]?.data).toEqual([]);
    expect(res.issues.some((i) => /need x/.test(i.message))).toBe(true);
  });

  it("filters rows with where", () => {
    const { res } = resolve('{"component":"data-table","columns":[{"key":"price","label":"P"}],"rows":[{"price":5},{"price":20},{"price":9}],"where":"{row.price < 10}"}');
    expect(res.root?.props.rows).toEqual([{ price: 5 }, { price: 9 }]);
    const none = resolve('{"component":"data-table","columns":[],"rows":[{"a":1}],"where":false}');
    expect(none.res.root?.props.rows).toEqual([]);
  });

  it("ignores where on other components", () => {
    const { res } = resolve('{"component":"chart","type":"bar","series":[{"name":"a","data":[1]}],"where":true}');
    expect(res.issues.some((i) => /where/.test(i.path))).toBe(true);
    expect("where" in (res.root?.props ?? {})).toBe(false);
  });

  it("does not treat x, i or row as point variables when the block declares them", () => {
    const { res } = resolve('{"component":"panel","state":{"x":7},"children":[{"component":"text","text":"x is {x}"}]}');
    expect(find(res.root, "text")?.props.text).toBe("x is 7");
  });

  it("leaves diff before/after alone", () => {
    const { res } = resolve('{"component":"diff","before":"a {x} b","after":"{{c}}"}');
    expect(res.root?.props).toMatchObject({ before: "a {x} b", after: "{{c}}" });
  });

  it("drops a prop whose template is broken", () => {
    const { res } = resolve('{"component":"panel","children":[{"component":"text","text":"a } b"}]}');
    expect(find(res.root, "text")?.props.text).toBeUndefined();
    expect(res.issues).toHaveLength(1);
  });

  it("caps depth and node count", () => {
    let src = '{"component":"text","text":"leaf"}';
    for (let i = 0; i < 10; i++) src = `{"component":"stack","children":[${src}]}`;
    const { res } = resolve(src);
    expect(res.issues.some((i) => /deeper/.test(i.message))).toBe(true);
    const many = `{"component":"panel","children":[${Array.from({ length: 320 }, () => '{"component":"divider"}').join(",")}]}`;
    expect(resolve(many).res.root?.children.length).toBe(299);
  });
});

describe("actions and liveness", () => {
  it("picks ask, then copy, then set", () => {
    expect(buttonAction({ ask: "  Why?  " }, {})).toEqual({ kind: "ask", text: "Why?" });
    expect(buttonAction({ ask: "", copy: "x" }, {})).toEqual({ kind: "copy", text: "x" });
    expect(buttonAction({ set: { undeclared: 1 } }, { n: 1 })).toBeNull();
    expect(buttonAction({ set: { n: 2, other: 3 } }, { n: 1 })).toEqual({ kind: "set", patch: { n: 2 } });
    expect(buttonAction({ ask: "x".repeat(200) }, {})).toMatchObject({ kind: "ask" });
    expect((buttonAction({ ask: "x".repeat(200) }, {}) as { text: string }).text).toHaveLength(120);
  });

  it("recognises live blocks", () => {
    expect(isLiveBlock(block('{"component":"chart","type":"bar","series":[]}'))).toBe(false);
    expect(isLiveBlock(block('{"component":"chart","state":{"a":1}}'))).toBe(true);
    expect(isLiveBlock(block('{"component":"panel"}'))).toBe(true);
  });
});
