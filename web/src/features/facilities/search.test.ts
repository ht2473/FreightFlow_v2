import { describe, expect, it } from "vitest";
import { area, confidenceLevel, distance, osmUrl } from "./labels";
import { facilityQuery, facilitySearchSchema, formatSort, hasFilters, parseSort, toggleListValue } from "./search";

describe("фильтры реестра в адресе", () => {
  it("разбирает списки и отбрасывает неверные значения", () => {
    const s = facilitySearchSchema.parse({ type: "warehouse,terminal", conf: "best", mkad: 30, restr: "yes" });
    expect(s.conf).toBeUndefined();
    expect(facilityQuery(s)).toMatchObject({
      type: ["warehouse", "terminal"],
      maxMkadMin: 30,
      nearRestrictions: true,
      minConfidence: undefined,
    });
    expect(hasFilters({})).toBe(false);
    expect(hasFilters(s)).toBe(true);
  });

  it("добавляет и убирает значение списка", () => {
    expect(toggleListValue(undefined, "a")).toBe("a");
    expect(toggleListValue("a,b", "a")).toBe("b");
    expect(toggleListValue("b", "b")).toBeUndefined();
  });

  it("сортировка — в обе стороны", () => {
    expect(parseSort(undefined)).toEqual([
      { id: "confidence", desc: true },
      { id: "name", desc: false },
    ]);
    expect(formatSort([{ id: "area", desc: true }])).toBe("-area");
    expect(formatSort(parseSort(undefined))).toBeUndefined();
  });
});

describe("подписи реестра", () => {
  it("площадь, расстояние, достоверность, ссылка OSM", () => {
    expect(area(12400)).toBe("12 400 м²");
    expect(area(250_000)).toBe("25 га");
    expect(distance(347)).toBe("350 м");
    expect(distance(2440)).toBe("2,4 км");
    expect(confidenceLevel(0.75).label).toBe("высокая");
    expect(confidenceLevel(0.2).tone).toBe("neutral");
    expect(osmUrl("W", 123)).toBe("https://www.openstreetmap.org/way/123");
  });
});
