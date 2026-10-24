import { describe, expect, it } from "vitest";
import type { Schemas } from "@/shared/api/client";
import { formatSort, parseSort, sortRows } from "./search";

const row = (name: string, over: Partial<Schemas["TerritoryRow"]>) =>
  ({
    id: name,
    kind: "district",
    name,
    areaKm2: 10,
    facilities: 0,
    buildingAreaM2: 0,
    poi: 0,
    worksActive: 0,
    ...over,
  }) as Schemas["TerritoryRow"];

describe("список территорий", () => {
  const rows = [
    row("Нижегородский", { population: 90000, facilities: 40 }),
    row("Арбат", { population: 37000, facilities: 1, shortName: "Арб" }),
    row("Внуково", { facilities: 12 }),
  ];

  it("сортировка по показателю; без значения — в конце при любом порядке", () => {
    expect(sortRows(rows, "", parseSort("-population")).map((r) => r.name)).toEqual([
      "Нижегородский",
      "Арбат",
      "Внуково",
    ]);
    expect(sortRows(rows, "", parseSort("population")).map((r) => r.name)).toEqual([
      "Арбат",
      "Нижегородский",
      "Внуково",
    ]);
    expect(sortRows(rows, "", parseSort("name")).map((r) => r.name)).toEqual(["Арбат", "Внуково", "Нижегородский"]);
  });

  it("подстрока названия; сортировка по умолчанию и неизвестное поле", () => {
    expect(sortRows(rows, "ниже", parseSort(undefined)).map((r) => r.name)).toEqual(["Нижегородский"]);
    expect(parseSort("-bogus")).toEqual({ id: "facilities", desc: true });
    expect(formatSort({ id: "facilities", desc: true })).toBeUndefined();
    expect(formatSort({ id: "density", desc: false })).toBe("density");
  });
});
