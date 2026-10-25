import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import type { FeatureCollection, Point } from "geojson";
import { ArrowRight, Building2 } from "lucide-react";
import type { Map as MapLibreMap } from "maplibre-gl";
import { useEffect, useMemo, useRef, useState } from "react";
import { Guard } from "@/app/Guard";
import { useOpenHit } from "@/app/shell/useOpenHit";
import { $api, problemMessage, type Schemas } from "@/shared/api/client";
import { num } from "@/shared/format";
import { basemapStyle } from "@/shared/map/basemap";
import { buildDataLayers, color, readPalette } from "@/shared/map/dataLayers";
import { DEFAULT_VIEW, mountMap, onStyleReady, unmountMap } from "@/shared/map/instance";
import { emptyStyle, fullStyle, useBasemapAvailable } from "@/shared/map/style";
import { groupHits, type Hit, kindIcons, kindTitles, pushRecent, registrySearch } from "@/shared/search";
import { Button, EmptyState, PageHeader, Section, Skeleton, TextField } from "@/shared/ui";
import { useResolvedTheme } from "@/shared/useResolvedTheme";
import f from "../facilities/facilities.module.css";
import { area } from "../facilities/labels";
import s from "./search.module.css";

type Result = Schemas["SearchResult"];
type Item = Schemas["FacilityItem"];

export function SearchPage() {
  return (
    <Guard permission="map.view">
      <SearchResults />
    </Guard>
  );
}

function SearchResults() {
  const { q = "" } = useSearch({ from: "/search" });
  const navigate = useNavigate({ from: "/search" });
  const [text, setText] = useState(q);
  useEffect(() => setText(q), [q]);
  const result = $api.useQuery("get", "/search", { params: { query: { q } } }, { enabled: q.length > 0 });
  const openHit = useOpenHit();
  const [focus, setFocus] = useState<string | undefined>();
  const r = result.data;

  return (
    <>
      <PageHeader title="Поиск" subtitle="Объекты, адреса, улицы, территории, акты, работы и сохранённые виды" />
      <form
        className={s.form}
        onSubmit={(e) => {
          e.preventDefault();
          if (text.trim()) {
            pushRecent({ q: text.trim() });
            void navigate({ search: { q: text.trim() } });
          }
        }}
      >
        <TextField
          label="Запрос"
          value={text}
          onChange={setText}
          placeholder="Например, «склады ЮВАО больше 10 000 м² у МКАД»"
        />
        <Button type="submit" variant="primary">
          Найти
        </Button>
      </form>
      {!q ? (
        <EmptyState title="Введите запрос">
          Запрос с типом объекта, округом или районом, площадью и расстоянием до МКАД превращается в фильтры реестра.
        </EmptyState>
      ) : result.isPending ? (
        <Section>
          <Skeleton height={200} />
        </Section>
      ) : result.error || !r ? (
        <div className="notice notice-error" role="alert">
          {problemMessage(result.error)}
        </div>
      ) : (
        <div className={s.layout}>
          <div className={s.list}>
            {r.parsed.chips.length > 0 && <Filters r={r} />}
            {r.facilities && <Facilities r={r} onFocus={setFocus} />}
            {r.hits.length === 0 && !r.facilities ? (
              <EmptyState title="Ничего не найдено">Проверьте написание или упростите запрос.</EmptyState>
            ) : (
              groupHits(r.hits).map((g) => {
                const Icon = kindIcons[g.kind];
                return (
                  <Section key={g.kind} title={kindTitles[g.kind]}>
                    <ul className={s.hits} aria-label={kindTitles[g.kind]}>
                      {g.hits.map((h) => (
                        <li key={h.ref}>
                          <button
                            type="button"
                            className={s.hit}
                            onClick={() => {
                              pushRecent({ hit: h });
                              void openHit(h);
                            }}
                            onMouseEnter={() => setFocus(`${h.kind}:${h.ref}`)}
                            onMouseLeave={() => setFocus(undefined)}
                          >
                            <Icon size={16} strokeWidth={1.7} aria-hidden />
                            <span>
                              <span className={s.hitTitle}>{h.title}</span>
                              {h.subtitle && <span className={s.hitNote}>{h.subtitle}</span>}
                            </span>
                          </button>
                        </li>
                      ))}
                    </ul>
                  </Section>
                );
              })
            )}
            <p className={s.took}>
              {r.fuzzy && "Совпадений по словам нет — показаны похожие по написанию. "}Поиск занял {num(r.tookMs)} мс.
            </p>
          </div>
          <ResultMap r={r} focus={focus} />
        </div>
      )}
    </>
  );
}

/** Разобранные фильтры и переход в реестр с ними. */
function Filters({ r }: { r: Result }) {
  const near = r.near;
  return (
    <Section title="Разобранные фильтры">
      <ul className={`${f.chips} ${s.parsed}`} aria-label="Разобранные фильтры">
        {r.parsed.chips.map((c) => (
          <li key={`${c.field}:${c.label}`} className={f.chip}>
            {c.label}
          </li>
        ))}
        {r.parsed.text && <li className={f.chip}>Текст: «{r.parsed.text}»</li>}
      </ul>
      {r.parsed.nearText && (
        <p className={s.took}>
          {near ? `Место «${r.parsed.nearText}»: ${near.title}.` : `Место «${r.parsed.nearText}» не найдено.`}
        </p>
      )}
      <Link to="/facilities" search={registrySearch(r.parsed, near)} className="btn btn-sm">
        <Building2 size={14} aria-hidden /> Открыть в реестре — фильтры можно изменить
      </Link>
    </Section>
  );
}

function Facilities({ r, onFocus }: { r: Result; onFocus: (k: string | undefined) => void }) {
  const page = r.facilities;
  if (!page) {
    return null;
  }
  return (
    <Section title={`Объекты по фильтрам: ${num(page.total)}`}>
      {page.total === 0 ? (
        <p className={s.took}>Объектов с такими условиями нет.</p>
      ) : (
        <>
          <ul className={s.hits} aria-label="Объекты по фильтрам">
            {page.items.map((it: Item) => (
              <li key={it.id}>
                <Link
                  to="/facilities/$facilityId"
                  params={{ facilityId: it.id }}
                  className={s.hit}
                  onMouseEnter={() => onFocus(`facility:${it.id}`)}
                  onMouseLeave={() => onFocus(undefined)}
                >
                  <Building2 size={16} strokeWidth={1.7} aria-hidden />
                  <span>
                    <span className={s.hitTitle}>{it.name ?? it.typeName}</span>
                    <span className={s.hitNote}>
                      {[it.typeName, it.okrug, area(it.buildingAreaM2)].filter(Boolean).join(" · ")}
                    </span>
                  </span>
                </Link>
              </li>
            ))}
          </ul>
          {page.total > page.items.length && (
            <Link to="/facilities" search={registrySearch(r.parsed, r.near)} className={s.more}>
              Все {num(page.total)} в реестре <ArrowRight size={14} aria-hidden />
            </Link>
          )}
        </>
      )}
    </Section>
  );
}

/** Точки результата: документы с координатами и объекты по фильтрам. */
function points(r: Result): FeatureCollection<Point> {
  const hits: Hit[] = r.hits.filter((h) => h.point);
  return {
    type: "FeatureCollection",
    features: [
      ...hits.map((h) => ({
        type: "Feature" as const,
        properties: { key: `${h.kind}:${h.ref}`, kind: h.kind },
        geometry: { type: "Point" as const, coordinates: [h.point?.lon ?? 0, h.point?.lat ?? 0] },
      })),
      ...(r.facilities?.items ?? []).map((it) => ({
        type: "Feature" as const,
        properties: { key: `facility:${it.id}`, kind: "facility" },
        geometry: { type: "Point" as const, coordinates: [it.point.lon, it.point.lat] },
      })),
    ],
  };
}

function ResultMap({ r, focus }: { r: Result; focus: string | undefined }) {
  const theme = useResolvedTheme();
  const basemap = useBasemapAvailable();
  const catalog = $api.useQuery("get", "/layers", {}, { staleTime: 60_000 });
  const hostRef = useRef<HTMLDivElement>(null);
  const [map, setMap] = useState<MapLibreMap | null>(null);
  const palette = useMemo(() => {
    void theme;
    return readPalette();
  }, [theme]);
  const data = useMemo(() => points(r), [r]);

  const style = useMemo(() => {
    if (basemap.data === undefined || !catalog.data) {
      return null;
    }
    const layers = catalog.data.layers;
    const base = basemap.data ? basemapStyle(theme) : emptyStyle(theme, palette);
    return fullStyle(base, layers, buildDataLayers(layers, {}, palette), {
      sources: { results: { type: "geojson", data } },
      layers: [
        {
          id: "results:point",
          type: "circle",
          source: "results",
          paint: {
            "circle-radius": ["case", ["==", ["get", "key"], focus ?? ""], 9, 6],
            "circle-color": [
              "case",
              ["==", ["get", "key"], focus ?? ""],
              color(palette, "crit"),
              color(palette, "acc"),
            ],
            "circle-stroke-color": color(palette, "panel"),
            "circle-stroke-width": 2,
          },
        },
      ],
    });
  }, [basemap.data, catalog.data, theme, palette, data, focus]);

  const latest = useRef(style);
  latest.current = style;
  const ready = style !== null;
  useEffect(() => {
    const st = latest.current;
    if (!ready || !hostRef.current || !st) {
      return;
    }
    setMap(mountMap(hostRef.current, { ...DEFAULT_VIEW, style: st }));
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

  // Рамка по найденным точкам.
  useEffect(() => {
    const coords = data.features.map((x) => x.geometry.coordinates as [number, number]);
    if (!map || coords.length === 0) {
      return;
    }
    const lons = coords.map((c) => c[0]);
    const lats = coords.map((c) => c[1]);
    map.fitBounds(
      [
        [Math.min(...lons), Math.min(...lats)],
        [Math.max(...lons), Math.max(...lats)],
      ],
      { padding: 48, maxZoom: 15, duration: 0 },
    );
  }, [map, data]);

  return <section ref={hostRef} className={`${f.miniMap} ${s.map}`} aria-label="Карта результатов" />;
}
