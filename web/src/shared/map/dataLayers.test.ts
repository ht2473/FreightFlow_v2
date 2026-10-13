import { describe, expect, it } from "vitest";
import { buildDataLayers, codeOf, interactiveLayerIds, type MapLayer } from "./dataLayers";

const base = {
  description: "",
  minzoom: 8,
  maxzoom: 16,
  version: "3",
  attribution: "© участники OpenStreetMap",
  visible: true,
  properties: [],
};

const catalog: MapLayer[] = [
  {
    ...base,
    code: "restrictions",
    group: "network",
    title: "Ограничения",
    geometry: "line",
    tiles: "/tiles/restrictions/{z}/{x}/{y}.mvt?v=3",
    style: {
      kind: "category",
      property: "severity",
      dashedBy: "conditional",
      categories: [{ values: ["ban"], label: "Запрет", color: "crit" }],
    },
  },
  {
    ...base,
    code: "facilities",
    group: "objects",
    title: "Объекты",
    geometry: "point",
    tiles: "/tiles/facilities/{z}/{x}/{y}.mvt?v=3",
    style: { kind: "single", color: "acc", categories: [] },
  },
  {
    ...base,
    code: "industrial_zones",
    group: "territory",
    title: "Промзоны",
    geometry: "polygon",
    tiles: "/tiles/industrial_zones/{z}/{x}/{y}.mvt?v=1",
    style: { kind: "single", color: "n9", categories: [] },
  },
];

const palette = { acc: "#5b5bd6", crit: "#c4262c", n9: "#8b8d98", panel: "#ffffff", tx: "#1c2024" };

describe("buildDataLayers", () => {
  it("строит источники и слои только для включённых слоёв", () => {
    const { sources, layers } = buildDataLayers(
      catalog,
      {
        restrictions: { visible: true, opacity: 1 },
        facilities: { visible: true, opacity: 0.5 },
        industrial_zones: { visible: false, opacity: 1 },
      },
      palette,
      "http://test",
    );
    expect(Object.keys(sources)).toEqual(["restrictions", "facilities"]);
    expect(sources.facilities).toMatchObject({
      type: "vector",
      tiles: ["http://test/tiles/facilities/{z}/{x}/{y}.mvt?v=3"],
      minzoom: 8,
      maxzoom: 16,
    });
    // Ограничения по времени — отдельным пунктирным слоем; широкая линия — для выбора.
    expect(layers.map((l) => l.id)).toEqual([
      "data:restrictions:line",
      "data:restrictions:dashed",
      "data:restrictions:hit",
      "data:facilities:circle",
    ]);
    const circle = layers.find((l) => l.id === "data:facilities:circle");
    expect(circle?.type).toBe("circle");
    if (circle?.type === "circle") {
      expect(circle.paint?.["circle-color"]).toBe("#5b5bd6");
      // Масштаб — только на верхнем уровне интерполяции (требование MapLibre).
      expect(circle.paint?.["circle-radius"]).toEqual(expect.arrayContaining(["interpolate", ["zoom"]]));
    }
    const line = layers.find((l) => l.id === "data:restrictions:line");
    if (line?.type === "line") {
      expect(line.paint?.["line-color"]).toEqual([
        "match",
        ["to-string", ["get", "severity"]],
        ["ban"],
        "#c4262c",
        "#8b8d98",
      ]);
    }
  });

  it("порядок наведения — сверху вниз, только точки, заливки и широкие линии", () => {
    const { layers } = buildDataLayers(
      catalog,
      {
        restrictions: { visible: true, opacity: 1 },
        facilities: { visible: true, opacity: 1 },
        industrial_zones: { visible: true, opacity: 1 },
      },
      palette,
      "http://test",
    );
    expect(interactiveLayerIds(layers)).toEqual([
      "data:industrial_zones:fill",
      "data:facilities:circle",
      "data:restrictions:hit",
    ]);
    expect(codeOf("data:facilities:circle")).toBe("facilities");
    expect(codeOf("water")).toBeUndefined();
  });
});
