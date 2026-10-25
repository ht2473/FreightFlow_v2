import { Link, useParams } from "@tanstack/react-router";
import type { Geometry } from "geojson";
import { Building2, Map as MapIcon } from "lucide-react";
import type { LayerSpecification, Map as MapLibreMap } from "maplibre-gl";
import { useEffect, useMemo, useRef, useState } from "react";
import { Guard } from "@/app/Guard";
import { $api, problemMessage, type Schemas } from "@/shared/api/client";
import { num } from "@/shared/format";
import { basemapStyle } from "@/shared/map/basemap";
import { buildDataLayers, color, readPalette } from "@/shared/map/dataLayers";
import { mountMap, onStyleReady, unmountMap } from "@/shared/map/instance";
import { emptyStyle, fullStyle, useBasemapAvailable } from "@/shared/map/style";
import { PageHeader, Section, Skeleton } from "@/shared/ui";
import { useResolvedTheme } from "@/shared/useResolvedTheme";
import f from "../facilities/facilities.module.css";
import { area } from "../facilities/labels";
import { formatCenter, layerStates } from "../map/state";
import { parseSort, sortRows } from "./search";
import { TerritoryTable } from "./TerritoriesPage";
import { TerritoryMetrics } from "./TerritoryMetrics";
import s from "./territories.module.css";

type Profile = Schemas["TerritoryProfile"];

/** Слои на карте территории. */
const contextLayers = new Set(["facilities", "road_works"]);

export function TerritoryPage() {
  return (
    <Guard permission="map.view">
      <Territory />
    </Guard>
  );
}

/** Центр и масштаб карты по рамке территории. */
export function viewOf(bbox: number[]): { c: string; z: number } {
  const [w = 0, so = 0, e = 0, n = 0] = bbox;
  const span = Math.max(e - w, (n - so) * 1.8, 0.005);
  const z = Math.min(15, Math.max(8, Math.round(Math.log2(360 / span) - 0.6)));
  return { c: formatCenter((w + e) / 2, (so + n) / 2), z };
}

function Territory() {
  const { territoryId } = useParams({ from: "/territories/$territoryId" });
  const q = $api.useQuery("get", "/territories/{territoryId}", { params: { path: { territoryId } } });
  const p = q.data;
  const okrug = p?.kind === "okrug";
  const [sort, setSort] = useState(parseSort(undefined));
  return (
    <>
      <PageHeader
        title={p?.name ?? "Территория"}
        subtitle={p ? (okrug ? "Административный округ Москвы" : `Район · ${p.parentName ?? ""}`) : undefined}
        actions={
          p && (
            <div className={s.actions}>
              <Link
                to="/map"
                search={{ ...viewOf(p.bbox), l: "boundaries,facility_areas,facilities" }}
                className="btn btn-sm"
              >
                <MapIcon size={14} aria-hidden /> На карте
              </Link>
              <Link to="/facilities" search={okrug ? { okrug: p.id } : { district: p.id }} className="btn btn-sm">
                <Building2 size={14} aria-hidden /> Объекты в реестре
              </Link>
            </div>
          )
        }
      />
      {q.isPending ? (
        <Section>
          <Skeleton height={240} />
        </Section>
      ) : q.error || !p ? (
        <Section>
          <div className="notice notice-error" role="alert">
            {problemMessage(q.error)}
          </div>
          <Link to="/territories">К списку территорий</Link>
        </Section>
      ) : (
        <>
          <div className={f.cardLayout}>
            <Section>
              <Facts p={p} />
            </Section>
            <div>
              <TerritoryMap p={p} />
              <p className={f.mapNote}>Граница территории, объекты логистики, дорожные работы.</p>
            </div>
          </div>
          <TerritoryMetrics level={okrug ? "okrug" : "district"} id={p.id} />
          {okrug && p.children.length > 0 && (
            <Section title="Районы округа">
              <TerritoryTable
                rows={sortRows(p.children, "", sort)}
                label="Районы округа"
                withParent={false}
                sort={sort}
                onSort={setSort}
              />
            </Section>
          )}
        </>
      )}
    </>
  );
}

function Facts({ p }: { p: Profile }) {
  const a = p.summary;
  const series = p.population_series.slice(-8);
  const max = Math.max(1, ...series.map((x) => x.value));
  return (
    <div className={s.facts}>
      <dl className={s.grid}>
        <dt>Площадь</dt>
        <dd className="num">{num(p.areaKm2, p.areaKm2 < 10 ? 2 : 1)} км²</dd>
        <dt>Население</dt>
        <dd className="num">
          {p.population === undefined ? "нет данных" : `${num(p.population)} (${p.populationYear})`}
        </dd>
        <dt>Плотность</dt>
        <dd className="num">{p.density === undefined ? "—" : `${num(p.density)} чел./км²`}</dd>
        <dt>Объектов реестра</dt>
        <dd className="num">{num(p.facilities)}</dd>
        <dt>Площадь застройки</dt>
        <dd className="num">{area(p.buildingAreaM2)}</dd>
        <dt>Общая площадь (оценка)</dt>
        <dd className="num">{area(a.floorAreaM2)}</dd>
        <dt>Участков с ограничениями</dt>
        <dd className="num">
          {num(a.restrictions)} · {num(a.restrictedKm, 1)} км
        </dd>
        <dt>Дорожные работы</dt>
        <dd className="num">
          идут {num(a.worksActive)}, запланировано {num(a.worksPlanned)}
        </dd>
      </dl>

      {a.types.length > 0 && (
        <>
          <h3 className={s.sub}>Объекты по типам</h3>
          <p className={s.line}>{a.types.map((x) => `${x.label}: ${num(x.count)}`).join(" · ")}</p>
        </>
      )}
      {a.poi.length > 0 && (
        <>
          <h3 className={s.sub}>Инфраструктура</h3>
          <p className={s.line}>{a.poi.map((x) => `${x.label}: ${num(x.count)}`).join(" · ")}</p>
        </>
      )}
      {series.length > 1 && (
        <>
          <h3 className={s.sub}>Население на 1 января</h3>
          <table className={s.series} aria-label="Население по годам">
            <tbody>
              {series.map((x) => (
                <tr key={x.year}>
                  <th scope="row">{x.year}</th>
                  <td>
                    <span className={s.bar} style={{ width: `${(x.value / max) * 100}%` }} aria-hidden />
                  </td>
                  <td className="num">{num(x.value)}</td>
                </tr>
              ))}
            </tbody>
          </table>
          <p className={s.note}>Росстат, база показателей муниципальных образований.</p>
        </>
      )}
      {a.top.length > 0 && (
        <>
          <h3 className={s.sub}>Крупнейшие объекты</h3>
          <ul className={s.top}>
            {a.top.map((x) => (
              <li key={x.id}>
                <Link to="/facilities/$facilityId" params={{ facilityId: x.id }}>
                  {x.name ?? x.typeName}
                </Link>
                <span className="num">{area(x.buildingAreaM2)}</span>
              </li>
            ))}
          </ul>
        </>
      )}
    </div>
  );
}

function TerritoryMap({ p }: { p: Profile }) {
  const theme = useResolvedTheme();
  const basemap = useBasemapAvailable();
  const catalog = $api.useQuery("get", "/layers", {}, { staleTime: 60_000 });
  const hostRef = useRef<HTMLDivElement>(null);
  const [map, setMap] = useState<MapLibreMap | null>(null);
  const palette = useMemo(() => {
    void theme;
    return readPalette();
  }, [theme]);

  const style = useMemo(() => {
    if (basemap.data === undefined || !catalog.data) {
      return null;
    }
    const layers = catalog.data.layers;
    const base = basemap.data ? basemapStyle(theme) : emptyStyle(theme, palette);
    const data = buildDataLayers(layers, layerStates(layers, contextLayers, {}, {}), palette);
    const boundary = {
      type: "Feature" as const,
      properties: {},
      geometry: p.geometry as unknown as Geometry,
    };
    const overlay: LayerSpecification[] = [
      {
        id: "territory:fill",
        type: "fill",
        source: "territory",
        paint: { "fill-color": color(palette, "acc"), "fill-opacity": 0.06 },
      },
      {
        id: "territory:line",
        type: "line",
        source: "territory",
        paint: { "line-color": color(palette, "acc"), "line-width": 2 },
      },
    ];
    return fullStyle(base, layers, data, {
      sources: { territory: { type: "geojson", data: boundary } },
      layers: overlay,
    });
  }, [basemap.data, catalog.data, theme, palette, p]);

  const latest = useRef(style);
  latest.current = style;
  const ready = style !== null;
  const [w = 37.6, so = 55.7, e = 37.7, n = 55.8] = p.bbox;
  useEffect(() => {
    const st = latest.current;
    if (!ready || !hostRef.current || !st) {
      return;
    }
    setMap(mountMap(hostRef.current, { center: [(w + e) / 2, (so + n) / 2], zoom: 11, style: st }));
    return () => {
      unmountMap();
      setMap(null);
    };
  }, [ready, w, so, e, n]);

  useEffect(() => {
    if (!map || !style) {
      return;
    }
    return onStyleReady(map, () => map.setStyle(style, { diff: true }));
  }, [map, style]);

  useEffect(() => {
    map?.fitBounds(
      [
        [w, so],
        [e, n],
      ],
      { padding: 24, duration: 0 },
    );
  }, [map, w, so, e, n]);

  return <section ref={hostRef} className={f.miniMap} aria-label="Карта территории" />;
}
