import { describe, expect, it } from "vitest";
import { pairKey, pairs, pairwiseMatrix, scaleId, scaleValue } from "./pairwise";

describe("pairwise", () => {
  it("строит обратносимметричную матрицу", () => {
    const m = pairwiseMatrix(["a", "b", "c"], { [pairKey("a", "b")]: 3, [pairKey("b", "c")]: 1 / 5 });
    expect(m[0]).toEqual([1, 3, 1]);
    expect(m[1]?.[0]).toBeCloseTo(1 / 3);
    expect(m[1]?.[2]).toBeCloseTo(1 / 5);
    expect(m[2]?.[1]).toBeCloseTo(5);
    expect(m[2]?.[2]).toBe(1);
  });

  it("переводит значения шкалы туда и обратно", () => {
    for (const v of [9, 3, 1, 1 / 3, 1 / 9]) {
      expect(scaleValue(scaleId(v))).toBeCloseTo(v);
    }
    expect(scaleId(1 / 7)).toBe("1/7");
  });

  it("перечисляет пары над диагональю", () => {
    expect(pairs(["a", "b", "c"])).toEqual([
      ["a", "b"],
      ["a", "c"],
      ["b", "c"],
    ]);
  });
});
