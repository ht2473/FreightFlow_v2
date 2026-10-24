import { describe, expect, it } from "vitest";
import type { MapLayer } from "@/shared/map/dataLayers";
import { attributionText, legendRows } from "./exportImage";

const layer = (over: Partial<MapLayer>) =>
  ({
    code: "x",
    title: "Слой",
    geometry: "point",
    attribution: "© участники OpenStreetMap",
    style: { kind: "single", color: "acc", categories: [] },
    ...over,
  }) as MapLayer;

describe("изображение карты", () => {
  it("легенда: категории под заголовком слоя, пояснение после элементов", () => {
    const rows = legendRows([
      layer({ title: "Объекты", style: { kind: "single", color: "acc", categories: [], note: "Размер — площадь" } }),
      layer({
        title: "Ограничения",
        geometry: "line",
        style: {
          kind: "category",
          property: "kind",
          categories: [{ values: ["ban"], label: "Запрет", color: "crit" }],
        },
      }),
    ]);
    expect(rows).toEqual([
      { kind: "item", text: "Объекты", geometry: "point", color: "acc" },
      { kind: "note", text: "Размер — площадь" },
      { kind: "title", text: "Ограничения" },
      { kind: "item", text: "Запрет", geometry: "line", color: "crit" },
    ]);
  });

  it("источники: подложка, слои без повторов, MapLibre", () => {
    const text = attributionText(
      [layer({}), layer({ attribution: "Портал открытых данных, data.mos.ru (CC BY 4.0); © участники OpenStreetMap" })],
      true,
    );
    expect(text).toBe(
      "Protomaps · © участники OpenStreetMap · Портал открытых данных, data.mos.ru (CC BY 4.0) · MapLibre",
    );
  });
});
