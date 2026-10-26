import type { Polygon } from "geojson";
import { Minus, Plus } from "lucide-react";
import {
  type GeoJSONSourceSpecification,
  type LayerSpecification,
  Map as MapLibreMap,
  type StyleSpecification,
} from "maplibre-gl";
import { type ReactNode, useEffect, useMemo, useRef, useState } from "react";
import { TerraDraw, TerraDrawPolygonMode } from "terra-draw";
import { TerraDrawMapLibreGLAdapter } from "terra-draw-maplibre-gl-adapter";
import { $api } from "@/shared/api/client";
import { basemapStyle } from "@/shared/map/basemap";
import { buildDataLayers, readPalette } from "@/shared/map/dataLayers";
import { DEFAULT_VIEW, mountMap, onStyleReady, unmountMap } from "@/shared/map/instance";
import { emptyStyle, fullStyle, useBasemapAvailable } from "@/shared/map/style";
import { IconButton } from "@/shared/ui";
import { useResolvedTheme } from "@/shared/useResolvedTheme";
import { layerStates } from "../map/state";
import s from "./scenario.module.css";

/** Слои страницы поверх подложки и каталога. */
export interface Overlay {
  sources: Record<string, GeoJSONSourceSpecification>;
  layers: LayerSpecification[];
}

export type Palette = ReturnType<typeof readPalette>;

/** Охват: [запад, юг, восток, север]. */
export type Bounds = [number, number, number, number];

/** Палитра текущей темы (цвета ролей из CSS-переменных). */
export function usePalette(): Palette {
  const theme = useResolvedTheme();
  return useMemo(() => {
    void theme;
    return readPalette();
  }, [theme]);
}

/** Стиль карты: подложка (или фон без неё), слои каталога context и слои страницы. */
export function useScenarioStyle(context: Set<string>, overlay: Overlay | null): StyleSpecification | null {
  const theme = useResolvedTheme();
  const basemap = useBasemapAvailable();
  const catalog = $api.useQuery("get", "/layers", {}, { staleTime: 60_000 });
  const palette = usePalette();
  return useMemo(() => {
    if (basemap.data === undefined || !catalog.data) {
      return null;
    }
    const base = basemap.data ? basemapStyle(theme) : emptyStyle(theme, palette);
    const layers = catalog.data.layers;
    const data = buildDataLayers(layers, layerStates(layers, context, {}), palette);
    return fullStyle(base, layers, data, overlay ?? undefined);
  }, [basemap.data, catalog.data, theme, palette, context, overlay]);
}

function Zoom({ map }: { map: MapLibreMap | null }) {
  return (
    <div className={s.zoom}>
      <IconButton label="Приблизить" onPress={() => map?.zoomIn()}>
        <Plus size={17} />
      </IconButton>
      <IconButton label="Отдалить" onPress={() => map?.zoomOut()}>
        <Minus size={17} />
      </IconButton>
    </div>
  );
}

function boundsKey(b: Bounds | null) {
  return b ? b.map((v) => v.toFixed(5)).join(",") : "";
}

/** Переход к охвату при его смене (не при каждом обновлении стиля). */
function useFit(map: MapLibreMap | null, fit: Bounds | null) {
  const key = boundsKey(fit);
  const latest = useRef(fit);
  latest.current = fit;
  useEffect(() => {
    const b = latest.current;
    if (!map || !b || !key) {
      return;
    }
    map.fitBounds(
      [
        [b[0], b[1]],
        [b[2], b[3]],
      ],
      { padding: 60, duration: 0, maxZoom: 14 },
    );
  }, [map, key]);
}

/**
 * Основная карта раздела — общий экземпляр рабочего пространства. onMap сообщает экземпляр
 * (для второй карты и инструментов), onClick — щелчок по карте.
 */
export function MainMap({
  style,
  label,
  hint,
  legend,
  fit,
  cursor,
  onMap,
  onClick,
  children,
}: {
  style: StyleSpecification | null;
  label?: string;
  hint?: ReactNode;
  legend?: ReactNode;
  fit?: Bounds | null;
  cursor?: string;
  onMap?: (m: MapLibreMap | null) => void;
  onClick?: (p: [number, number]) => void;
  children?: ReactNode;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const [map, setMap] = useState<MapLibreMap | null>(null);
  const latest = useRef(style);
  latest.current = style;
  const ready = style !== null;
  const mapRef = useRef(onMap);
  mapRef.current = onMap;
  useEffect(() => {
    const st = latest.current;
    if (!ready || !hostRef.current || !st) {
      return;
    }
    const m = mountMap(hostRef.current, { ...DEFAULT_VIEW, style: st });
    setMap(m);
    mapRef.current?.(m);
    return () => {
      unmountMap();
      setMap(null);
      mapRef.current?.(null);
    };
  }, [ready]);

  useEffect(() => {
    if (!map || !style) {
      return;
    }
    return onStyleReady(map, () => map.setStyle(style, { diff: true }));
  }, [map, style]);

  const clickRef = useRef(onClick);
  clickRef.current = onClick;
  useEffect(() => {
    if (!map) {
      return;
    }
    const h = (e: { lngLat: { lng: number; lat: number } }) => clickRef.current?.([e.lngLat.lng, e.lngLat.lat]);
    map.on("click", h);
    return () => {
      map.off("click", h);
    };
  }, [map]);

  useEffect(() => {
    if (map) {
      map.getCanvas().style.cursor = cursor ?? "";
    }
  }, [map, cursor]);

  useFit(map, fit ?? null);

  return (
    <section className={s.mapArea} aria-label={label ?? "Карта сценария"}>
      <div ref={hostRef} className={s.mapHost} />
      {label && <div className={s.mapLabel}>{label}</div>}
      {hint && (
        <div className={s.hint} role="status">
          {hint}
        </div>
      )}
      <Zoom map={map} />
      {legend && <div className={s.legend}>{legend}</div>}
      {children}
    </section>
  );
}

/**
 * Вторая карта для сравнения: свой экземпляр MapLibre, вид синхронизирован с основной картой в
 * обе стороны.
 */
export function SecondMap({
  style,
  sync,
  label,
  legend,
}: {
  style: StyleSpecification | null;
  sync: MapLibreMap | null;
  label: string;
  legend?: ReactNode;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const [map, setMap] = useState<MapLibreMap | null>(null);
  const latest = useRef(style);
  latest.current = style;
  const ready = style !== null;
  useEffect(() => {
    const st = latest.current;
    if (!ready || !hostRef.current || !st) {
      return;
    }
    const m = new MapLibreMap({
      container: hostRef.current,
      style: st,
      center: DEFAULT_VIEW.center,
      zoom: DEFAULT_VIEW.zoom,
      minZoom: 5,
      maxZoom: 18,
      dragRotate: false,
      pitchWithRotate: false,
      touchPitch: false,
      attributionControl: false,
    });
    m.touchZoomRotate.disableRotation();
    m.keyboard.disableRotation();
    setMap(m);
    return () => {
      m.remove();
      setMap(null);
    };
  }, [ready]);

  useEffect(() => {
    if (map && style) {
      map.setStyle(style, { diff: true });
    }
  }, [map, style]);

  // Синхронизация вида: изменение на одной карте повторяется на другой; флаг не даёт событию
  // вернуться обратно.
  useEffect(() => {
    if (!map || !sync) {
      return;
    }
    let syncing = false;
    const copy = (from: MapLibreMap, to: MapLibreMap) => () => {
      if (syncing) {
        return;
      }
      syncing = true;
      to.jumpTo({ center: from.getCenter(), zoom: from.getZoom() });
      syncing = false;
    };
    const toSecond = copy(sync, map);
    const toMain = copy(map, sync);
    toSecond();
    sync.on("move", toSecond);
    map.on("move", toMain);
    // Вторая карта появляется рядом с основной: основной нужно пересчитать размер.
    sync.resize();
    return () => {
      sync.off("move", toSecond);
      map.off("move", toMain);
      sync.resize();
    };
  }, [map, sync]);

  return (
    <section className={s.mapArea} aria-label={label}>
      <div ref={hostRef} className={s.mapHost} />
      <div className={s.mapLabel}>{label}</div>
      <Zoom map={map} />
      {legend && <div className={s.legend}>{legend}</div>}
    </section>
  );
}

/** Рисование многоугольника на карте; onDone получает замкнутый контур. */
export function useDrawPolygon(map: MapLibreMap | null, active: boolean, onDone: (ring: [number, number][]) => void) {
  const done = useRef(onDone);
  done.current = onDone;
  useEffect(() => {
    if (!map || !active) {
      return;
    }
    const draw = new TerraDraw({
      adapter: new TerraDrawMapLibreGLAdapter({ map, prefixId: "ff-scenario-draw" }),
      modes: [new TerraDrawPolygonMode()],
    });
    draw.start();
    draw.setMode("polygon");
    draw.on("finish", (id) => {
      const f = draw.getSnapshotFeature(id);
      const ring = (f?.geometry as Polygon | undefined)?.coordinates[0];
      if (ring && ring.length >= 4) {
        done.current(ring.map(([lon = 0, lat = 0]) => [lon, lat] as [number, number]));
      }
    });
    return () => {
      draw.stop();
    };
  }, [map, active]);
}

/** Охват набора координат. */
export function extentOf(coords: number[][]): Bounds | null {
  if (coords.length === 0) {
    return null;
  }
  let [w, so, e, n] = [180, 90, -180, -90];
  for (const [lon = 0, lat = 0] of coords) {
    w = Math.min(w, lon);
    e = Math.max(e, lon);
    so = Math.min(so, lat);
    n = Math.max(n, lat);
  }
  // Точка — небольшая окрестность.
  if (w === e && so === n) {
    return [w - 0.02, so - 0.01, e + 0.02, n + 0.01];
  }
  return [w, so, e, n];
}

/** Все координаты геометрий FeatureCollection. */
export function coordsOf(fc: { features?: { geometry?: unknown }[] } | undefined): number[][] {
  const out: number[][] = [];
  const walk = (c: unknown) => {
    if (Array.isArray(c) && typeof c[0] === "number") {
      out.push(c as number[]);
    } else if (Array.isArray(c)) {
      for (const x of c) {
        walk(x);
      }
    }
  };
  for (const f of fc?.features ?? []) {
    walk((f.geometry as { coordinates?: unknown } | undefined)?.coordinates);
  }
  return out;
}
