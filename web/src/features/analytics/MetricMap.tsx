import { useNavigate } from "@tanstack/react-router";
import type { FeatureCollection } from "geojson";
import type { ExpressionSpecification, Map as MapLibreMap, MapMouseEvent } from "maplibre-gl";
import { useEffect, useMemo, useRef, useState } from "react";
import { basemapStyle } from "@/shared/map/basemap";
import { color, readPalette } from "@/shared/map/dataLayers";
import { DEFAULT_VIEW, mountMap, onStyleReady, unmountMap } from "@/shared/map/instance";
import { emptyStyle, useBasemapAvailable } from "@/shared/map/style";
import { useResolvedTheme } from "@/shared/useResolvedTheme";
import s from "./analytics.module.css";

/** Смесь двух цветов #rrggbb в доле t. */
export function mix(a: string, b: string, t: number): string {
  const p = (h: string) => [1, 3, 5].map((i) => Number.parseInt(h.slice(i, i + 2), 16));
  const [x, y] = [p(a), p(b)];
  return `#${x
    .map((v, i) =>
      Math.round(v + ((y[i] ?? v) - v) * t)
        .toString(16)
        .padStart(2, "0"),
    )
    .join("")}`;
}

/** Границы классов по квантилям: не больше n классов, без повторов. */
export function breaks(values: number[], n = 5): number[] {
  const s = [...values].filter(Number.isFinite).sort((a, b) => a - b);
  if (s.length === 0) {
    return [];
  }
  const out: number[] = [];
  for (let i = 1; i < n; i++) {
    const v = s[Math.min(s.length - 1, Math.floor((i * s.length) / n))] ?? 0;
    if (out.length === 0 || v > (out[out.length - 1] ?? -Infinity)) {
      out.push(v);
    }
  }
  return out;
}

export interface ChoroplethFeature {
  entity: string;
  label: string;
  value: number;
}

/**
 * Картограмма: области с заливкой по классам значения (одноцветная индиго-шкала: в светлой теме
 * темнее — больше, в тёмной — светлее), подсказка при наведении; onSelect — щелчок по области.
 */
export function MetricMap({
  geo,
  label,
  format,
  highlight,
  onSelect,
  classes,
}: {
  geo: FeatureCollection;
  label: string;
  format: (v: number) => string;
  /** Классы вместо шкалы: значение → роль цвета палитры и подпись в легенде. */
  classes?: { value: number; color: string; label: string }[];
  /** Подсвеченные объекты (обводка): например, значимые ячейки. */
  highlight?: Set<string>;
  onSelect?: (entity: string) => void;
}) {
  const theme = useResolvedTheme();
  const basemap = useBasemapAvailable();
  const hostRef = useRef<HTMLDivElement>(null);
  const [map, setMap] = useState<MapLibreMap | null>(null);
  const [hover, setHover] = useState<{ x: number; y: number; f: ChoroplethFeature } | null>(null);
  const palette = useMemo(() => {
    void theme;
    return readPalette();
  }, [theme]);
  const values = useMemo(
    () => geo.features.map((f) => Number((f.properties as { value?: number } | null)?.value)),
    [geo],
  );
  const br = useMemo(() => breaks(values), [values]);
  const colors = useMemo(() => {
    const lo = color(palette, "acc-soft");
    const hi = color(palette, "acc-tx");
    return Array.from({ length: br.length + 1 }, (_, i) => mix(lo, hi, br.length === 0 ? 1 : i / br.length));
  }, [palette, br]);

  const style = useMemo(() => {
    if (basemap.data === undefined) {
      return null;
    }
    const base = basemap.data ? basemapStyle(theme) : emptyStyle(theme, palette);
    const fill: ExpressionSpecification = ["step", ["get", "value"], colors[0] ?? "#888888"];
    br.forEach((b, i) => {
      fill.push(b, colors[i + 1] ?? "#888888");
    });
    const byClass: unknown[] = ["match", ["get", "value"]];
    for (const c of classes ?? []) {
      byClass.push(c.value, color(palette, c.color));
    }
    byClass.push(color(palette, "n9"));
    const hl = [...(highlight ?? [])];
    return {
      ...base,
      sources: { ...base.sources, choropleth: { type: "geojson" as const, data: geo, promoteId: "entity" } },
      layers: [
        ...base.layers,
        {
          id: "choropleth:fill",
          type: "fill" as const,
          source: "choropleth",
          paint: {
            "fill-color": classes
              ? (byClass as ExpressionSpecification)
              : br.length > 0
                ? fill
                : (colors[0] ?? "#888888"),
            "fill-opacity": 0.78,
          },
        },
        {
          id: "choropleth:line",
          type: "line" as const,
          source: "choropleth",
          paint: {
            "line-color": [
              "case",
              ["in", ["get", "entity"], ["literal", hl]],
              color(palette, "crit"),
              color(palette, "panel"),
            ] as ExpressionSpecification,
            "line-width": ["case", ["in", ["get", "entity"], ["literal", hl]], 2.5, 0.6] as ExpressionSpecification,
          },
        },
      ],
    };
  }, [basemap.data, theme, palette, geo, br, colors, highlight, classes]);

  const latest = useRef(style);
  latest.current = style;
  const ready = style !== null;
  useEffect(() => {
    const st = latest.current;
    if (!ready || !hostRef.current || !st) {
      return;
    }
    setMap(mountMap(hostRef.current, { center: DEFAULT_VIEW.center, zoom: 8.6, style: st }));
    return () => {
      unmountMap();
      setMap(null);
    };
  }, [ready]);

  useEffect(() => {
    if (!map || !style) {
      return;
    }
    return onStyleReady(map, () => map.setStyle(style, { diff: true }));
  }, [map, style]);

  const selectRef = useRef(onSelect);
  selectRef.current = onSelect;
  useEffect(() => {
    if (!map) {
      return;
    }
    const pick = (e: MapMouseEvent): ChoroplethFeature | null => {
      if (!map.getLayer("choropleth:fill")) {
        return null;
      }
      const f = map.queryRenderedFeatures(e.point, { layers: ["choropleth:fill"] })[0];
      const p = f?.properties as ChoroplethFeature | undefined;
      return p ? { entity: String(p.entity), label: String(p.label ?? p.entity), value: Number(p.value) } : null;
    };
    const move = (e: MapMouseEvent) => {
      const f = pick(e);
      map.getCanvas().style.cursor = f && selectRef.current ? "pointer" : "";
      setHover(f ? { x: e.point.x, y: e.point.y, f } : null);
    };
    const click = (e: MapMouseEvent) => {
      const f = pick(e);
      if (f) {
        selectRef.current?.(f.entity);
      }
    };
    const leave = () => setHover(null);
    map.on("mousemove", move);
    map.on("click", click);
    map.on("mouseout", leave);
    return () => {
      map.off("mousemove", move);
      map.off("click", click);
      map.off("mouseout", leave);
    };
  }, [map]);

  const scale = [colors[0], ...br.map((_, i) => colors[i + 1])].map((c, i) => ({
    color: c ?? "#888888",
    text:
      br.length === 0
        ? format(values[0] ?? 0)
        : i === 0
          ? `до ${format(br[0] ?? 0)}`
          : i === br.length
            ? `от ${format(br[i - 1] ?? 0)}`
            : `${format(br[i - 1] ?? 0)} – ${format(br[i] ?? 0)}`,
  }));
  const legend = classes ? classes.map((c) => ({ color: color(palette, c.color), text: c.label })) : scale;

  return (
    <div className={s.mapWrap}>
      <section ref={hostRef} className={s.map} aria-label={label} />
      <ul className={s.legend} aria-label="Легенда картограммы">
        {legend.map((l) => (
          <li key={l.text}>
            <i style={{ background: l.color }} aria-hidden />
            {l.text}
          </li>
        ))}
      </ul>
      {hover && (
        <div className={s.tip} style={{ left: hover.x + 12, top: hover.y + 12 }} aria-hidden>
          <b>{hover.f.label}</b>
          <span>{format(hover.f.value)}</span>
        </div>
      )}
    </div>
  );
}

/** Переход к профилю территории по щелчку на картограмме районов и округов. */
export function useTerritoryOpen(level: string) {
  const navigate = useNavigate();
  return level === "district" || level === "okrug"
    ? (entity: string) => void navigate({ to: "/territories/$territoryId", params: { territoryId: entity } })
    : undefined;
}
