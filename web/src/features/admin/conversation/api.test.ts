import { describe, expect, it } from "vitest";
import { manifestFromText } from "./api";

describe("manifestFromText", () => {
  it("reads the manifest on the last line", () => {
    const tail = '{"id":1}\n{"_type":"export_manifest","complete":true,"exported":2,"failed":0}\n';
    expect(manifestFromText(tail)).toEqual({ complete: true, exported: 2, failed: 0 });
  });
  it("reports an incomplete export", () => {
    expect(manifestFromText('{"_type":"export_manifest","complete":false,"exported":1,"failed":3,"error":"x"}')).toEqual({ complete: false, exported: 1, failed: 3 });
  });
  it("returns null when the last line is not a manifest", () => {
    expect(manifestFromText('{"id":1}\n')).toBeNull();
    expect(manifestFromText("")).toBeNull();
    expect(manifestFromText("garbage")).toBeNull();
  });
});
