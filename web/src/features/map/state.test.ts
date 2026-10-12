import { describe, expect, it } from "vitest";
import type { MapLayer } from "@/shared/map/dataLayers";
import { formatCenter, mapSearchSchema, parseCenter, visibleLayers } from "./state";

const layer = (code: string, visible: boolean) => ({ code, visible }) as MapLayer;
const catalog = [layer("facilities", true), layer("fuel_stations", false), layer("rings", true)];

describe("состояние карты в адресе", () => {
  it("центр: разбор и запись с точностью до метра", () => {
    expect(parseCenter("37.62000,55.75000")).toEqual([37.62, 55.75]);
    expect(parseCenter(formatCenter(37.6175123, 55.7520456))).toEqual([37.61751, 55.75205]);
    expect(parseCenter("abc")).toBeNull();
    expect(parseCenter("37.6")).toBeNull();
    expect(parseCenter("200,55")).toBeNull();
    expect(parseCenter(undefined)).toBeNull();
  });

  it("слои: по умолчанию — из каталога, иначе — из адреса без неизвестных кодов", () => {
    expect([...visibleLayers(catalog, undefined)]).toEqual(["facilities", "rings"]);
    expect([...visibleLayers(catalog, "fuel_stations,nope")]).toEqual(["fuel_stations"]);
    expect([...visibleLayers(catalog, "")]).toEqual([]);
  });

  it("схема отбрасывает неверные значения, не ломая страницу", () => {
    expect(mapSearchSchema.parse({ z: 12, c: "37,55" })).toEqual({ z: 12, c: "37,55" });
    expect(mapSearchSchema.parse({ z: 40, l: 5 })).toEqual({ z: undefined, l: undefined, c: undefined });
  });
});
