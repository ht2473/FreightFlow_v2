import type { FeatureCollection } from "geojson";
import type {
  CircleLayerSpecification,
  DataDrivenPropertyValueSpecification,
  ExpressionSpecification,
  LayerSpecification,
  SourceSpecification,
} from "maplibre-gl";
import type { Schemas } from "@/shared/api/client";

export type MapLayer = Schemas["MapLayer"];

/** Состояние слоя на карте: включён и непрозрачность 0–1. */
export interface LayerState {
  visible: boolean;
  opacity: number;
  /** Дополнительный отбор объектов слоя (например, работы на дату шкалы времени). */
  filter?: ExpressionSpecification;
}

/** Значения ролей цвета из токенов текущей темы. */
export type Palette = Record<string, string>;

/** Цвет роли; неизвестная роль — нейтральный серый. */
export const color = (p: Palette, role: string): string => p[role] ?? "#888888";

const roles = [
  "acc",
  "acc-ring",
  "acc-on",
  "acc-soft",
  "acc-tx",
  "panel",
  "tx",
  "tx2",
  "n8",
  "n9",
  "n10",
  "n11",
  "n12",
  "ok",
  "warn",
  "crit",
  "info",
  "cat1",
  "cat2",
  "cat3",
  "cat4",
  "cat5",
  "cat6",
  "cat7",
  "cat8",
];

/** Читает цвета ролей из CSS-переменных документа (после применения темы). */
export function readPalette(el: Element = document.documentElement): Palette {
  const cs = getComputedStyle(el);
  const out: Palette = {};
  for (const r of roles) {
    out[r] = cs.getPropertyValue(`--${r}`).trim() || "#888888";
  }
  return out;
}

/** Идентификатор слоя MapLibre: код слоя каталога и часть оформления. */
export const layerId = (code: string, part: string) => `data:${code}:${part}`;

/** Код слоя каталога по идентификатору слоя MapLibre. */
export function codeOf(id: string): string | undefined {
  const [prefix, code] = id.split(":");
  return prefix === "data" ? code : undefined;
}

function colorExpr(l: MapLayer, p: Palette): DataDrivenPropertyValueSpecification<string> {
  const st = l.style;
  if (st.kind === "category" && st.property && st.categories.length > 0) {
    const expr: unknown[] = ["match", ["to-string", ["get", st.property]]];
    for (const c of st.categories) {
      expr.push(c.values, color(p, c.color));
    }
    expr.push(color(p, "n9"));
    return expr as ExpressionSpecification;
  }
  return color(p, st.color ?? "acc");
}

const selected: ExpressionSpecification = ["boolean", ["feature-state", "selected"], false];
const hovered: ExpressionSpecification = ["boolean", ["feature-state", "hover"], false];

/**
 * Радиус точки объекта: диаметр 10 + √(площадь/1000)·1,6 px (DESIGN.md §7); на мелких
 * масштабах точки уменьшаются, чтобы не перекрывать друг друга.
 */
const facilitySize: ExpressionSpecification = [
  "+",
  5,
  ["*", 0.8, ["sqrt", ["/", ["coalesce", ["get", "area"], 0], 1000]]],
];
const facilityRadius: ExpressionSpecification = [
  "interpolate",
  ["linear"],
  ["zoom"],
  8,
  ["*", 0.45, facilitySize],
  12,
  facilitySize,
];

function pointPaint(l: MapLayer, p: Palette, op: number): CircleLayerSpecification["paint"] {
  const facilities = l.code === "facilities";
  // Низкая достоверность — бледнее (правило в легенде слоя).
  const fillOpacity: DataDrivenPropertyValueSpecification<number> = facilities
    ? ["*", op, ["case", ["<", ["coalesce", ["get", "confidence"], 1], 0.5], 0.5, 0.95]]
    : op * 0.95;
  return {
    "circle-color": colorExpr(l, p),
    "circle-opacity": fillOpacity,
    "circle-radius": facilities ? facilityRadius : ["interpolate", ["linear"], ["zoom"], 9, 3.5, 13, 5, 16, 6.5],
    "circle-stroke-color": ["case", selected, color(p, "acc-ring"), hovered, color(p, "tx"), color(p, "panel")],
    "circle-stroke-width": ["case", selected, 4, hovered, 2, 1.5],
    "circle-stroke-opacity": op,
  };
}

function pointLayers(l: MapLayer, p: Palette, op: number): LayerSpecification[] {
  return [
    {
      id: layerId(l.code, "circle"),
      type: "circle",
      source: l.code,
      "source-layer": l.code,
      paint: pointPaint(l, p, op),
    },
  ];
}

/** До этого масштаба (не включая) объекты показываются кластерами. */
export const CLUSTER_UNTIL = 12;

/** Источник точек объектов для кластеров. */
export const POINTS_SOURCE = "facilities:points";

/** Слой кластера по идентификатору слоя MapLibre. */
export const isClusterLayer = (id: string) => id === layerId("facilities", "cluster:circle");

/**
 * Кластеры объектов на мелких масштабах: точки реестра собираются в группы на стороне карты,
 * отбор реестра применяется до группировки — число в кластере точное.
 */
function clusterLayers(
  l: MapLayer,
  points: FeatureCollection,
  p: Palette,
  op: number,
  filter: ExpressionSpecification | undefined,
): { source: SourceSpecification; layers: LayerSpecification[] } {
  const isCluster: ExpressionSpecification = ["has", "point_count"];
  const zooms = { minzoom: l.minzoom, maxzoom: CLUSTER_UNTIL };
  return {
    source: {
      type: "geojson",
      data: points,
      cluster: true,
      clusterMaxZoom: CLUSTER_UNTIL - 1,
      clusterRadius: 44,
      promoteId: "id",
      attribution: l.attribution,
      ...(filter ? { filter } : {}),
    },
    layers: [
      {
        id: layerId(l.code, "cluster:circle"),
        type: "circle",
        source: POINTS_SOURCE,
        filter: isCluster,
        ...zooms,
        paint: {
          "circle-color": color(p, "acc"),
          "circle-opacity": op * 0.88,
          "circle-radius": ["step", ["get", "point_count"], 11, 10, 14, 50, 18, 200, 23],
          "circle-stroke-color": ["case", hovered, color(p, "tx"), color(p, "panel")],
          "circle-stroke-width": ["case", hovered, 2.5, 2],
          "circle-stroke-opacity": op,
        },
      },
      {
        id: layerId(l.code, "cluster:count"),
        type: "symbol",
        source: POINTS_SOURCE,
        filter: isCluster,
        ...zooms,
        layout: {
          "text-field": ["get", "point_count_abbreviated"],
          "text-font": ["Noto Sans Medium"],
          "text-size": 11.5,
          "text-allow-overlap": true,
        },
        paint: { "text-color": color(p, "acc-on"), "text-opacity": op },
      },
      {
        id: layerId(l.code, "low:circle"),
        type: "circle",
        source: POINTS_SOURCE,
        filter: ["!", isCluster],
        ...zooms,
        paint: pointPaint(l, p, op),
      },
    ],
  };
}

function lineWidth(code: string): DataDrivenPropertyValueSpecification<number> {
  switch (code) {
    case "roads":
      return [
        "interpolate",
        ["linear"],
        ["zoom"],
        8,
        ["match", ["get", "class"], ["motorway", "trunk"], 1.4, 0.8],
        14,
        ["match", ["get", "class"], ["motorway", "trunk"], 4, ["primary", "secondary"], 3, 1.5],
      ];
    case "restrictions":
      return ["interpolate", ["linear"], ["zoom"], 10, 1.5, 14, 3.5, 16, 5];
    case "bridges":
      return ["interpolate", ["linear"], ["zoom"], 12, 2, 16, 6];
    case "rings":
      return ["interpolate", ["linear"], ["zoom"], 7, 1, 12, 2];
    case "road_works":
      return ["interpolate", ["linear"], ["zoom"], 10, 2, 14, 4.5, 16, 7];
    default:
      return 1.5;
  }
}

function lineLayers(l: MapLayer, p: Palette, op: number): LayerSpecification[] {
  const base = {
    type: "line" as const,
    source: l.code,
    "source-layer": l.code,
    layout: { "line-cap": "round" as const, "line-join": "round" as const },
  };
  const paint = {
    "line-color": colorExpr(l, p),
    "line-width": lineWidth(l.code),
    "line-opacity": l.code === "bridges" ? op * 0.55 : op,
  };
  const out: LayerSpecification[] = [];
  const dashed = l.style.dashedBy;
  if (dashed) {
    // Пунктир не задаётся выражением по свойству — два слоя с фильтром.
    out.push(
      { ...base, id: layerId(l.code, "line"), filter: ["!=", ["get", dashed], true], paint },
      {
        ...base,
        id: layerId(l.code, "dashed"),
        filter: ["==", ["get", dashed], true],
        layout: { ...base.layout, "line-cap": "butt" },
        paint: { ...paint, "line-dasharray": [2, 1.5] },
      },
    );
  } else {
    out.push({ ...base, id: layerId(l.code, "line"), paint });
  }
  if (l.code === "rings") {
    out.push({
      id: layerId(l.code, "label"),
      type: "symbol",
      source: l.code,
      "source-layer": l.code,
      layout: {
        "symbol-placement": "line",
        "symbol-spacing": 400,
        "text-field": ["get", "name"],
        "text-font": ["Noto Sans Medium"],
        "text-size": 11,
        "text-letter-spacing": 0.05,
      },
      paint: {
        "text-color": color(p, "n11"),
        "text-halo-color": color(p, "panel"),
        "text-halo-width": 1.5,
        "text-opacity": op,
      },
    });
  }
  // Невидимая широкая линия для наведения и выбора тонких линий.
  out.push({
    ...base,
    id: layerId(l.code, "hit"),
    paint: { "line-color": "#000000", "line-opacity": 0, "line-width": 12 },
  });
  return out;
}

function polygonLayers(l: MapLayer, p: Palette, op: number): LayerSpecification[] {
  const color = colorExpr(l, p);
  const boundary = l.code === "boundaries";
  const fill: LayerSpecification = {
    id: layerId(l.code, "fill"),
    type: "fill",
    source: l.code,
    "source-layer": l.code,
    paint: {
      "fill-color": color,
      // Границы — без заливки; заливка только для подсветки выбранной территории.
      "fill-opacity": boundary
        ? ["case", selected, 0.08 * op, 0]
        : ["*", op, ["case", selected, 0.3, hovered, 0.22, l.code === "facility_areas" ? 0.12 : 0.14]],
    },
  };
  const outline: LayerSpecification = {
    id: layerId(l.code, "outline"),
    type: "line",
    source: l.code,
    "source-layer": l.code,
    layout: { "line-join": "round" },
    paint: {
      "line-color": color,
      "line-opacity": boundary ? op * 0.9 : op * 0.7,
      "line-width": boundary
        ? ["match", ["get", "kind"], "region", 2, "okrug", 1.4, 0.7]
        : ["interpolate", ["linear"], ["zoom"], 10, 0.6, 15, 1.2],
    },
  };
  if (boundary) {
    outline.filter = ["!=", ["get", "kind"], "district"];
    const districts: LayerSpecification = {
      ...outline,
      id: layerId(l.code, "district"),
      filter: ["==", ["get", "kind"], "district"],
      paint: { ...outline.paint, "line-dasharray": [3, 2] },
    };
    return [fill, districts, outline];
  }
  return [fill, outline];
}

/** Источники и слои MapLibre для включённых слоёв каталога (порядок каталога — снизу вверх). */
export function buildDataLayers(
  catalog: MapLayer[],
  state: Record<string, LayerState>,
  palette: Palette,
  origin = window.location.origin,
  facilityPoints?: FeatureCollection,
): { sources: Record<string, SourceSpecification>; layers: LayerSpecification[] } {
  const sources: Record<string, SourceSpecification> = {};
  const layers: LayerSpecification[] = [];
  for (const l of catalog) {
    const st = state[l.code];
    if (!st?.visible) {
      continue;
    }
    sources[l.code] = {
      type: "vector",
      tiles: [origin + l.tiles],
      minzoom: l.minzoom,
      maxzoom: l.maxzoom,
      attribution: l.attribution,
    };
    const op = Math.min(1, Math.max(0.1, st.opacity));
    const ls =
      l.geometry === "point"
        ? pointLayers(l, palette, op)
        : l.geometry === "line"
          ? lineLayers(l, palette, op)
          : polygonLayers(l, palette, op);
    // Точки объектов на мелких масштабах — кластерами, тайлы — с масштаба CLUSTER_UNTIL.
    const clustered = l.code === "facilities" && facilityPoints !== undefined;
    for (const x of ls) {
      const minzoom = clustered ? CLUSTER_UNTIL : l.minzoom;
      layers.push(withFilter({ ...x, minzoom } as LayerSpecification, st.filter));
    }
    if (clustered) {
      const c = clusterLayers(l, facilityPoints, palette, op, st.filter);
      sources[POINTS_SOURCE] = c.source;
      layers.push(...c.layers);
    }
  }
  return { sources, layers };
}

/** Добавляет отбор слоя каталога к собственному фильтру слоя оформления. */
function withFilter(layer: LayerSpecification, extra: ExpressionSpecification | undefined): LayerSpecification {
  if (!extra || layer.type === "background" || layer.type === "raster" || layer.type === "hillshade") {
    return layer;
  }
  const own = "filter" in layer ? layer.filter : undefined;
  return { ...layer, filter: own ? ["all", own as ExpressionSpecification, extra] : extra } as LayerSpecification;
}

/** Работы, действующие в день с номером day (дней от 1970-01-01): начало ≤ day < окончание. */
export function workDayFilter(day: number): ExpressionSpecification {
  return ["all", ["<=", ["get", "start_day"], day], [">", ["get", "end_day"], day]];
}

/** Номер дня от 1970-01-01 для даты по местному времени. */
export function dayNumber(d: Date): number {
  return Math.floor(Date.UTC(d.getFullYear(), d.getMonth(), d.getDate()) / 86_400_000);
}

/** Слои, по которым работают наведение и выбор: точки, контуры и широкие линии. */
export function interactiveLayerIds(layers: LayerSpecification[]): string[] {
  return layers
    .filter((x) => x.id.endsWith(":circle") || x.id.endsWith(":fill") || x.id.endsWith(":hit"))
    .map((x) => x.id)
    .reverse();
}
