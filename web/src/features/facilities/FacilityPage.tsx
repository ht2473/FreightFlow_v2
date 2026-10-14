import { Link, useParams } from "@tanstack/react-router";
import type { Geometry } from "geojson";
import type { LayerSpecification, LngLatBoundsLike, Map as MapLibreMap } from "maplibre-gl";
import { useEffect, useMemo, useRef, useState } from "react";
import { Guard } from "@/app/Guard";
import { $api, problemMessage, type Schemas } from "@/shared/api/client";
import { basemapStyle } from "@/shared/map/basemap";
import { buildDataLayers, color, readPalette } from "@/shared/map/dataLayers";
import { mountMap, onStyleReady, unmountMap } from "@/shared/map/instance";
import { emptyStyle, fullStyle, useBasemapAvailable } from "@/shared/map/style";
import { Badge, PageHeader, Section, Skeleton } from "@/shared/ui";
import { useResolvedTheme } from "@/shared/useResolvedTheme";
import { layerStates } from "../map/state";
import { Actions, CardTabs } from "./FacilityDrawer";
import s from "./facilities.module.css";
import { confidenceLevel } from "./labels";

type Card = Schemas["FacilityCard"];

/** Слои вокруг объекта на мини-карте. */
const contextLayers = new Set(["restrictions", "road_works", "truck_parking", "freight_points"]);

export function FacilityPage() {
  return (
    <Guard permission="map.view">
      <Facility />
    </Guard>
  );
}

function Facility() {
  const { facilityId } = useParams({ from: "/facilities/$facilityId" });
  const card = $api.useQuery("get", "/facilities/{facilityId}", { params: { path: { facilityId } } });
  const c = card.data;
  const f = c?.facility;
  return (
    <>
      <PageHeader
        title={f ? (f.name ?? f.typeName) : "Объект"}
        subtitle={f ? (f.address ?? f.typeName) : undefined}
        actions={f && <Actions f={f} />}
      />
      {card.isPending ? (
        <Section>
          <Skeleton height={240} />
        </Section>
      ) : card.error || !c || !f ? (
        <Section>
          <div className="notice notice-error" role="alert">
            {problemMessage(card.error)}
          </div>
          <Link to="/facilities">К реестру</Link>
        </Section>
      ) : (
        <div className={s.cardLayout}>
          <Section>
            <div className={s.drawerTags}>
              <Badge tone="info">{f.typeName}</Badge>
              <Badge tone={confidenceLevel(f.confidence).tone}>
                достоверность {confidenceLevel(f.confidence).label}
              </Badge>
            </div>
            <CardTabs c={c} />
          </Section>
          <div>
            <MiniMap c={c} />
            <p className={s.mapNote}>
              Контур и территория объекта, ограничения для грузовиков, дорожные работы, стоянки.
            </p>
          </div>
        </div>
      )}
    </>
  );
}

/** Рамка контура объекта (или окрестность точки). */
function bounds(c: Card): LngLatBoundsLike {
  const geom = (c.territory ?? c.footprint) as unknown as Geometry | undefined;
  const { lon, lat } = c.facility.point;
  let [w, so, e, n] = [lon - 0.004, lat - 0.0025, lon + 0.004, lat + 0.0025];
  const visit = (v: unknown) => {
    if (Array.isArray(v) && typeof v[0] === "number" && typeof v[1] === "number") {
      w = Math.min(w, v[0]);
      e = Math.max(e, v[0]);
      so = Math.min(so, v[1]);
      n = Math.max(n, v[1]);
    } else if (Array.isArray(v)) {
      v.forEach(visit);
    }
  };
  if (geom && "coordinates" in geom) {
    visit(geom.coordinates);
  }
  return [
    [w, so],
    [e, n],
  ];
}

function MiniMap({ c }: { c: Card }) {
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
    const shapes = {
      type: "FeatureCollection" as const,
      features: [
        ...(c.territory
          ? [
              {
                type: "Feature" as const,
                properties: { part: "territory" },
                geometry: c.territory as unknown as Geometry,
              },
            ]
          : []),
        ...(c.footprint
          ? [
              {
                type: "Feature" as const,
                properties: { part: "footprint" },
                geometry: c.footprint as unknown as Geometry,
              },
            ]
          : []),
        {
          type: "Feature" as const,
          properties: { part: "point" },
          geometry: { type: "Point" as const, coordinates: [c.facility.point.lon, c.facility.point.lat] },
        },
      ],
    };
    const overlay: LayerSpecification[] = [
      {
        id: "facility:territory",
        type: "fill",
        source: "facility",
        filter: ["==", ["get", "part"], "territory"],
        paint: { "fill-color": color(palette, "acc"), "fill-opacity": 0.08 },
      },
      {
        id: "facility:footprint",
        type: "fill",
        source: "facility",
        filter: ["==", ["get", "part"], "footprint"],
        paint: { "fill-color": color(palette, "acc"), "fill-opacity": 0.35 },
      },
      {
        id: "facility:outline",
        type: "line",
        source: "facility",
        filter: ["!=", ["get", "part"], "point"],
        paint: { "line-color": color(palette, "acc"), "line-width": 1.5 },
      },
      {
        id: "facility:point",
        type: "circle",
        source: "facility",
        filter: ["==", ["get", "part"], "point"],
        paint: {
          "circle-radius": 6,
          "circle-color": color(palette, "acc"),
          "circle-stroke-color": color(palette, "panel"),
          "circle-stroke-width": 2,
        },
      },
    ];
    return fullStyle(base, layers, data, { sources: { facility: { type: "geojson", data: shapes } }, layers: overlay });
  }, [basemap.data, catalog.data, theme, palette, c]);

  const latest = useRef(style);
  latest.current = style;
  const ready = style !== null;
  useEffect(() => {
    const st = latest.current;
    if (!ready || !hostRef.current || !st) {
      return;
    }
    const { lon, lat } = c.facility.point;
    setMap(mountMap(hostRef.current, { center: [lon, lat], zoom: 15, style: st }));
    return () => {
      unmountMap();
      setMap(null);
    };
  }, [ready, c.facility.point]);

  useEffect(() => {
    if (!map || !style) {
      return;
    }
    return onStyleReady(map, () => map.setStyle(style, { diff: true }));
  }, [map, style]);

  useEffect(() => {
    map?.fitBounds(bounds(c), { padding: 40, duration: 0, maxZoom: 17 });
  }, [map, c]);

  return <section ref={hostRef} className={s.miniMap} aria-label="Карта объекта" />;
}
