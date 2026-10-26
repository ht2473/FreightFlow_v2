import type { Feature, FeatureCollection, Geometry } from "geojson";
import type { ExpressionSpecification, LayerSpecification } from "maplibre-gl";
import type { Schemas } from "@/shared/api/client";
import { color } from "@/shared/map/dataLayers";
import { deltaValue } from "./labels";
import type { Overlay, Palette } from "./ScenarioMap";

const empty = (): FeatureCollection => ({ type: "FeatureCollection", features: [] });

/** Перекрытия: участки — линиями, области — заливкой с контуром. */
function closureLayers(source: string, p: Palette): LayerSpecification[] {
  return [
    {
      id: `${source}-fill`,
      type: "fill",
      source,
      filter: ["==", ["geometry-type"], "Polygon"],
      paint: { "fill-color": color(p, "crit"), "fill-opacity": 0.14 },
    },
    {
      id: `${source}-outline`,
      type: "line",
      source,
      filter: ["==", ["geometry-type"], "Polygon"],
      paint: { "line-color": color(p, "crit"), "line-width": 2, "line-dasharray": [2, 1.5] },
    },
    {
      id: `${source}-casing`,
      type: "line",
      source,
      filter: ["!=", ["geometry-type"], "Polygon"],
      layout: { "line-cap": "round", "line-join": "round" },
      paint: { "line-color": color(p, "panel"), "line-width": 9 },
    },
    {
      id: `${source}-line`,
      type: "line",
      source,
      filter: ["!=", ["geometry-type"], "Polygon"],
      layout: { "line-cap": "round", "line-join": "round" },
      paint: { "line-color": color(p, "crit"), "line-width": 5 },
    },
  ];
}

function pointLayers(source: string, p: Palette): LayerSpecification[] {
  return [
    {
      id: `${source}-circle`,
      type: "circle",
      source,
      paint: {
        "circle-radius": ["match", ["get", "kind"], "new", 9, 7],
        "circle-color": ["match", ["get", "kind"], "new", color(p, "acc"), color(p, "cat2")],
        "circle-stroke-color": color(p, "panel"),
        "circle-stroke-width": 2,
      },
    },
  ];
}

/** Слои конструктора: перекрытия, новый объект и объекты для изохрон. */
export function builderOverlay(closures: Feature[], points: Feature[], p: Palette): Overlay {
  return {
    sources: {
      "sc:closures": { type: "geojson", data: { type: "FeatureCollection", features: closures } },
      "sc:points": { type: "geojson", data: { type: "FeatureCollection", features: points } },
    },
    layers: [...closureLayers("sc:closures", p), ...pointLayers("sc:points", p)],
  };
}

/** Цвет изменения времени объекта: потеря доступа и большой прирост — красный, улучшение — зелёный. */
function deltaColor(p: Palette): ExpressionSpecification {
  return [
    "case",
    ["==", ["get", "lost"], true],
    color(p, "crit"),
    ["<", ["get", "d"], 0],
    color(p, "ok"),
    ["step", ["get", "d"], color(p, "info"), 1, color(p, "warn"), 5, color(p, "crit")],
  ];
}

/** Объекты с изменением времени до цели как точки GeoJSON. */
export function facilityFeatures(rows: Schemas["ScenarioFacility"][]): Feature[] {
  return rows
    .filter((r) => r.entity !== "new")
    .map((r) => {
      const d = deltaValue(r);
      return {
        type: "Feature",
        properties: {
          name: r.name,
          d: Number.isFinite(d) ? d : d > 0 ? 999 : -999,
          lost: d === Number.POSITIVE_INFINITY,
        },
        geometry: { type: "Point", coordinates: [r.point.lon, r.point.lat] },
      };
    });
}

/**
 * Слои результата для варианта: изохроны этого варианта; для сценария — ещё перекрытия и
 * объекты с изменением времени (на базе перекрытия показаны пунктиром для ориентира).
 */
export function resultOverlay(
  variant: "base" | "scenario",
  isochrones: FeatureCollection | undefined,
  closures: FeatureCollection | undefined,
  facilities: Feature[],
  origins: Feature[],
  p: Palette,
): Overlay {
  const iso = {
    type: "FeatureCollection" as const,
    features: (isochrones?.features ?? [])
      .filter((f) => f.properties?.variant === variant)
      // Дальние контуры снизу, ближние поверх.
      .sort((a, b) => Number(b.properties?.minutes ?? 0) - Number(a.properties?.minutes ?? 0)),
  };
  const isoColor = variant === "base" ? color(p, "cat2") : color(p, "acc");
  const layers: LayerSpecification[] = [
    {
      id: "sc:iso-fill",
      type: "fill",
      source: "sc:iso",
      paint: { "fill-color": isoColor, "fill-opacity": 0.12 },
    },
    {
      id: "sc:iso-line",
      type: "line",
      source: "sc:iso",
      paint: { "line-color": isoColor, "line-width": 1.4, "line-opacity": 0.85 },
    },
  ];
  if (variant === "scenario") {
    layers.push(...closureLayers("sc:closures", p));
    layers.push({
      id: "sc:facilities",
      type: "circle",
      source: "sc:facilities",
      paint: {
        "circle-radius": ["interpolate", ["linear"], ["zoom"], 9, 4, 14, 8],
        "circle-color": deltaColor(p),
        "circle-stroke-color": color(p, "panel"),
        "circle-stroke-width": 1.2,
      },
    });
  } else {
    layers.push({
      id: "sc:closures-ghost",
      type: "line",
      source: "sc:closures",
      paint: { "line-color": color(p, "crit"), "line-width": 2, "line-dasharray": [1, 1.5], "line-opacity": 0.7 },
    });
  }
  layers.push(...pointLayers("sc:points", p));
  return {
    sources: {
      "sc:iso": { type: "geojson", data: iso },
      "sc:closures": { type: "geojson", data: closures ?? empty() },
      "sc:facilities": { type: "geojson", data: { type: "FeatureCollection", features: facilities } },
      "sc:points": { type: "geojson", data: { type: "FeatureCollection", features: origins } },
    },
    layers,
  };
}

/** Точка GeoJSON. */
export function pointFeature(lon: number, lat: number, props: Record<string, unknown>): Feature {
  return { type: "Feature", properties: props, geometry: { type: "Point", coordinates: [lon, lat] } };
}

/** Объект GeoJSON с геометрией из ответа API. */
export function geoFeature(geometry: Record<string, unknown>, props: Record<string, unknown>): Feature {
  return { type: "Feature", properties: props, geometry: geometry as unknown as Geometry };
}
