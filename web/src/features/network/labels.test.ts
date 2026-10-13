import { describe, expect, it } from "vitest";
import { difference, duration, pointLabel, restrictionText } from "./labels";

describe("подписи маршрута", () => {
  it("длительность", () => {
    expect(duration(25 * 60)).toBe("25 мин");
    expect(duration(65 * 60 + 20)).toBe("1 ч 05 мин");
  });

  it("разница с путём без ограничений", () => {
    const truck = { timeS: 1560, lengthKm: 25, coordinates: [] };
    const base = { timeS: 1080, lengthKm: 17.3, coordinates: [] };
    expect(difference(truck, base)).toBe("+8 мин, +7,7 км");
    expect(difference(base, base)).toBe("0 мин, 0 км");
  });

  it("точки A, промежуточные, B", () => {
    expect([0, 1, 2, 3].map((i) => pointLabel(i, 4))).toEqual(["A", "1", "2", "B"]);
  });

  it("ограничение со значением", () => {
    expect(restrictionText({ kind: "maxheight", value: 3.5, street: "", origins: [], segments: 1, lengthM: 80 })).toBe(
      "Высота до 3,5 м",
    );
    expect(restrictionText({ kind: "hgv_ban", street: "", origins: [], segments: 1, lengthM: 80 })).toBe(
      "Запрет грузовых",
    );
  });
});
