import type { FeatureCollection } from "geojson";
import { type LayerSpecification, Map as MapLibreMap } from "maplibre-gl";
import { createContext, type ReactNode, useContext, useEffect, useId, useLayoutEffect, useRef, useState } from "react";
import { problemMessage } from "@/shared/api/client";
import { dateTime } from "@/shared/format";
import { basemapStyle } from "@/shared/map/basemap";
import { color, readPalette } from "@/shared/map/dataLayers";
import { useBasemapAvailable } from "@/shared/map/style";
import s from "./print.module.css";

/** Таблица отчёта: показывается на странице и выкладывается для XLSX. */
export interface Sheet {
  name: string;
  columns: string[];
  rows: (string | number | null | undefined)[][];
}

export interface Source {
  title: string;
  attribution: string;
  license: string;
}

declare global {
  interface Window {
    __ffReport?: { title: string; sheets: Sheet[]; sources: Source[] };
  }
}

/** Ожидания страницы: каждая карта регистрирует своё и снимает после отрисовки. */
const Pending = createContext<{ add: (id: string) => void; done: (id: string) => void }>({
  add: () => {},
  done: () => {},
});

function usePending(): [Set<string>, { add: (id: string) => void; done: (id: string) => void }] {
  const [pending, setPending] = useState<Set<string>>(() => new Set());
  const api = useRef({
    add: (id: string) => setPending((p) => new Set(p).add(id)),
    done: (id: string) =>
      setPending((p) => {
        const n = new Set(p);
        n.delete(id);
        return n;
      }),
  });
  return [pending, api.current];
}

/**
 * Документ для печати. Служба pdf ждёт body[data-ready="true"]: данные загружены (loading — false),
 * карты отрисованы; ошибка загрузки — data-ready="error" с текстом в data-error.
 */
export function PrintDoc({
  title,
  subtitle,
  loading,
  error,
  sheets,
  sources,
  children,
}: {
  title: string;
  subtitle?: string;
  loading: boolean;
  error?: unknown;
  sheets: Sheet[];
  sources: Source[];
  children?: ReactNode;
}) {
  const [pending, api] = usePending();
  useLayoutEffect(() => {
    document.documentElement.dataset.theme = "light";
    if (s.body) {
      document.body.classList.add(s.body);
    }
  }, []);
  useEffect(() => {
    document.title = title;
  }, [title]);
  const failed = error !== undefined && error !== null;
  const ready = !loading && !failed && pending.size === 0;
  useEffect(() => {
    if (failed) {
      document.body.dataset.error = problemMessage(error) ?? "Ошибка загрузки данных отчёта";
      document.body.dataset.ready = "error";
      return;
    }
    if (!ready) {
      delete document.body.dataset.ready;
      return;
    }
    window.__ffReport = { title, sheets, sources };
    // Два кадра: React дорисовал разметку, графики — свои холсты.
    let raf = requestAnimationFrame(() => {
      raf = requestAnimationFrame(() => {
        document.body.dataset.ready = "true";
      });
    });
    return () => cancelAnimationFrame(raf);
  }, [ready, failed, error, title, sheets, sources]);

  return (
    <Pending.Provider value={api}>
      <article className={s.doc}>
        <header className={s.head}>
          <div className={s.brand}>FreightFlow</div>
          <h1 className={s.title}>{title}</h1>
          {subtitle && <p className={s.subtitle}>{subtitle}</p>}
          <p className={s.meta}>Сформирован {dateTime(new Date().toISOString())}</p>
        </header>
        {failed ? (
          <p role="alert">{problemMessage(error)}</p>
        ) : loading ? (
          <p className={s.meta}>Загрузка данных…</p>
        ) : (
          children
        )}
        {sources.length > 0 && (
          <footer className={s.sources}>
            <h2>Источники данных</h2>
            <ul>
              {sources.map((src) => (
                <li key={src.title}>
                  {src.attribution || src.title}
                  {src.license ? ` · ${src.license}` : ""}
                </li>
              ))}
            </ul>
          </footer>
        )}
      </article>
    </Pending.Provider>
  );
}

/** Раздел отчёта; не разрывается между страницами, если помещается. */
export function PrintSection({ title, children }: { title: string; children: ReactNode }) {
  return (
    <section className={s.section}>
      <h2>{title}</h2>
      {children}
    </section>
  );
}

/** Ключевые значения отчёта. */
export function Facts({ items }: { items: [string, ReactNode][] }) {
  return (
    <dl className={s.facts}>
      {items.map(([k, v]) => (
        <div key={k}>
          <dt>{k}</dt>
          <dd>{v}</dd>
        </div>
      ))}
    </dl>
  );
}

/** Таблица листа: на странице — первые limit строк (в XLSX — все). */
export function SheetTable({
  sheet: full,
  limit = 40,
  empty,
  show,
}: {
  sheet: Sheet;
  limit?: number;
  empty?: string;
  /** Колонки на странице (в XLSX — все). */
  show?: number[];
}) {
  if (full.rows.length === 0) {
    return <p className={s.meta}>{empty ?? "Нет данных."}</p>;
  }
  const pick = <T,>(r: T[]) => (show ? show.map((i) => r[i] as T) : r);
  const sheet = { ...full, columns: pick(full.columns), rows: full.rows.map(pick) };
  const rows = sheet.rows.slice(0, limit);
  return (
    <>
      <table className={s.table}>
        <thead>
          <tr>
            {sheet.columns.map((c) => (
              <th key={c} scope="col">
                {c}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            // biome-ignore lint/suspicious/noArrayIndexKey: строки неизменной таблицы отчёта
            <tr key={i}>
              {r.map((v, j) => (
                // biome-ignore lint/suspicious/noArrayIndexKey: ячейки неизменной строки
                <td key={j} className={typeof v === "number" ? s.num : undefined}>
                  {typeof v === "number" ? v.toLocaleString("ru-RU", { maximumFractionDigits: 2 }) : (v ?? "—")}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
      {sheet.rows.length > limit && (
        <p className={s.meta}>
          Показаны {limit} из {sheet.rows.length}; полная таблица — в выгрузке XLSX.
        </p>
      )}
    </>
  );
}

/** Охват [запад, юг, восток, север]. */
export type Bounds = [number, number, number, number];

/** Охват объектов GeoJSON. */
export function boundsOf(fc: FeatureCollection, pad = 0.004): Bounds | null {
  let b: Bounds | null = null;
  const walk = (c: unknown) => {
    if (Array.isArray(c) && typeof c[0] === "number" && typeof c[1] === "number") {
      const [x, y] = c as [number, number];
      b = b ? [Math.min(b[0], x), Math.min(b[1], y), Math.max(b[2], x), Math.max(b[3], y)] : [x, y, x, y];
    } else if (Array.isArray(c)) {
      for (const v of c) {
        walk(v);
      }
    }
  };
  for (const f of fc.features) {
    if (f.geometry && "coordinates" in f.geometry) {
      walk(f.geometry.coordinates);
    }
  }
  const r = b as Bounds | null;
  return r ? [r[0] - pad, r[1] - pad, r[2] + pad, r[3] + pad] : null;
}

/** Слои печатной карты: области, линии и точки с цветом по свойству tone (crit, warn, acc, cat2). */
function overlayLayers(p: ReturnType<typeof readPalette>): LayerSpecification[] {
  const tone = [
    "match",
    ["get", "tone"],
    "crit",
    color(p, "crit"),
    "warn",
    color(p, "warn"),
    "cat2",
    color(p, "cat2"),
    color(p, "acc"),
  ];
  return [
    {
      id: "pr-fill",
      type: "fill",
      source: "pr",
      filter: ["in", ["geometry-type"], ["literal", ["Polygon", "MultiPolygon"]]],
      // biome-ignore lint/suspicious/noExplicitAny: выражение MapLibre
      paint: { "fill-color": tone as any, "fill-opacity": 0.12 },
    },
    {
      id: "pr-outline",
      type: "line",
      source: "pr",
      filter: ["in", ["geometry-type"], ["literal", ["Polygon", "MultiPolygon"]]],
      // biome-ignore lint/suspicious/noExplicitAny: выражение MapLibre
      paint: { "line-color": tone as any, "line-width": 1.5 },
    },
    {
      id: "pr-line",
      type: "line",
      source: "pr",
      filter: ["in", ["geometry-type"], ["literal", ["LineString", "MultiLineString"]]],
      layout: { "line-cap": "round", "line-join": "round" },
      // biome-ignore lint/suspicious/noExplicitAny: выражение MapLibre
      paint: { "line-color": tone as any, "line-width": 3 },
    },
    {
      id: "pr-point",
      type: "circle",
      source: "pr",
      filter: ["in", ["geometry-type"], ["literal", ["Point", "MultiPoint"]]],
      paint: {
        "circle-radius": 5,
        // biome-ignore lint/suspicious/noExplicitAny: выражение MapLibre
        "circle-color": tone as any,
        "circle-stroke-color": "#ffffff",
        "circle-stroke-width": 1.5,
      },
    },
  ];
}

/**
 * Карта для печати: подложка и объекты; готова, когда MapLibre отрисовал всё (событие idle).
 * Без подложки на сервере — только объекты на фоне.
 */
export function PrintMap({
  data,
  bounds,
  label,
  height = 320,
}: {
  data: FeatureCollection;
  bounds: Bounds | null;
  label: string;
  height?: number;
}) {
  const host = useRef<HTMLDivElement>(null);
  const id = useId();
  const pending = useContext(Pending);
  const basemap = useBasemapAvailable();
  useLayoutEffect(() => {
    pending.add(id);
    return () => pending.done(id);
  }, [id, pending]);
  useEffect(() => {
    const el = host.current;
    if (!el || basemap.data === undefined) {
      return;
    }
    let map: MapLibreMap | null = null;
    let poll: ReturnType<typeof setInterval> | undefined;
    const create = () => {
      const p = readPalette();
      const base = basemap.data
        ? basemapStyle("light")
        : {
            version: 8 as const,
            sources: {},
            layers: [{ id: "bg", type: "background" as const, paint: { "background-color": "#f4f4f7" } }],
          };
      const m = new MapLibreMap({
        container: el,
        style: {
          ...base,
          sources: { ...base.sources, pr: { type: "geojson", data } },
          layers: [...base.layers, ...overlayLayers(p)],
        },
        center: [37.62, 55.75],
        zoom: 9.5,
        interactive: false,
        attributionControl: false,
        fadeDuration: 0,
        canvasContextAttributes: { preserveDrawingBuffer: true },
      });
      if (bounds) {
        m.fitBounds(
          [
            [bounds[0], bounds[1]],
            [bounds[2], bounds[3]],
          ],
          { padding: 24, duration: 0, maxZoom: 15 },
        );
      }
      // Готова, когда стиль и все тайлы загружены и карта дважды подряд в покое.
      let calm = 0;
      poll = setInterval(() => {
        calm = m.loaded() && m.areTilesLoaded() && !m.isMoving() ? calm + 1 : 0;
        if (calm >= 2) {
          clearInterval(poll);
          pending.done(id);
        }
      }, 250);
      map = m;
    };
    // Карта создаётся, когда у рамки есть ширина: иначе MapLibre берёт 400 пикселей.
    const ro = new ResizeObserver(() => {
      if (!map && el.clientWidth > 0) {
        create();
      } else {
        map?.resize();
      }
    });
    ro.observe(el);
    // Карта без тайлов (нет сети к подложке) не должна держать отчёт: предел ожидания.
    const t = setTimeout(() => pending.done(id), 30_000);
    return () => {
      ro.disconnect();
      clearTimeout(t);
      clearInterval(poll);
      // Карта, созданная эффектом, удаляется при его повторе или размонтировании.
      (map as MapLibreMap | null)?.remove();
    };
  }, [basemap.data, data, bounds, id, pending]);
  return <div ref={host} className={s.map} style={{ height }} role="img" aria-label={label} />;
}

/** Атрибуция подложки и OSM — для карт отчёта. */
export const osmSource: Source = {
  title: "OpenStreetMap",
  attribution: "© участники OpenStreetMap",
  license: "ODbL 1.0",
};

export const mosSource: Source = {
  title: "Портал открытых данных Правительства Москвы",
  attribution: "Портал открытых данных Правительства Москвы (data.mos.ru)",
  license: "CC BY 4.0",
};

export const freightflowSource: Source = {
  title: "Расчёты FreightFlow",
  attribution: "Расчётные показатели FreightFlow (методики — в справке системы)",
  license: "",
};
