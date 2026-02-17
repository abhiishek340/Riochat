/** Type utilities that make catalogs structurally checked against en-US. */

export type DeepStrings<T> = { [K in keyof T]: T[K] extends string ? string : DeepStrings<T[K]> };

type Join<P extends string, K extends string> = P extends "" ? K : `${P}.${K}`;

export type Paths<T, P extends string = ""> = {
  [K in keyof T & string]: T[K] extends string ? Join<P, K> : Paths<T[K], Join<P, K>>;
}[keyof T & string];

export function flatten(tree: unknown, prefix = "", out: Record<string, string> = {}): Record<string, string> {
  if (typeof tree === "string") {
    out[prefix] = tree;
  } else if (typeof tree === "object" && tree !== null) {
    for (const [k, v] of Object.entries(tree)) flatten(v, prefix ? `${prefix}.${k}` : k, out);
  }
  return out;
}
