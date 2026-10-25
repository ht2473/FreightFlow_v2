import { useNavigate, useRouter, useSearch } from "@tanstack/react-router";
import type { Feature, FeatureCollection, Point } from "geojson";
import { ImageDown, Minus, Plus, X } from "lucide-react";
import type { ExpressionSpecification, GeoJSONSource, MapGeoJSONFeature, Map as MapLibreMap } from "maplibre-gl";
import { useEffect, useMemo, useRef, useState } from "react";
import { Guard } from "@/app/Guard";
import { $api, type Schemas } from "@/shared/api/client";
import { num } from "@/shared/format";
import { basemapStyle } from "@/shared/map/basemap";
import {
  buildDataLayers,
  CLUSTER_UNTIL,
  codeOf,
  color,
  dayNumber,
  interactiveLayerIds,
  isClusterLayer,
  layerId,
  type MapLayer,
  type Palette,
  POINTS_SOURCE,
  readPalette,
  workDayFilter,
} from "@/shared/map/dataLayers";
import { DEFAULT_VIEW, mountMap, onStyleReady, unmountMap } from "@/shared/map/instance";
import { emptyStyle, fullStyle, useBasemapAvailable } from "@/shared/map/style";
import { IconButton } from "@/shared/ui";
import { Tabs } from "@/shared/ui/navigation";
import { toast } from "@/shared/ui/toast";
import { useResolvedTheme } from "@/shared/useResolvedTheme";
import { ViewsMenu } from "@/shared/ViewsMenu";
import { FacilityDrawer } from "../facilities/FacilityDrawer";
import { AreaSummary } from "./AreaSummary";
import { AreaTool, type DrawMode, type Drawn, isPolygon, lineLength } from "./AreaTool";
import { mapImage } from "./exportImage";
import { FeatureCard, type Selection } from "./FeatureCard";
import { LayersPanel } from "./LayersPanel";
import { Legend } from "./Legend";
import s from "./map.module.css";
import { m } from "./messages";
import { type ObjectItem, ObjectsPanel } from "./ObjectsPanel";
import {
  facilityFilters,
  formatCenter,
  hasFacilityFilter,
  layerStates,
  type MapSearch,
  matchesFacilityFilter,
  parseCenter,
  useOpacity,
  visibleLayers,
} from "./state";
import { WhatsHere } from "./WhatsHere";
import { WorkDate } from "./WorkDate";

export function MapPage() {
  return (
    <Guard permission="map.view">
      <MapWorkspace />
    </Guard>
  );
}

function MapWorkspace() {
  const search = useSearch({ from: "/map" });
  const navigate = useNavigate({ from: "/map" });
  const router = useRouter();
  const theme = useResolvedTheme();
  const catalogQuery = $api.useQuery("get", "/layers", {}, { staleTime: 60_000 });
  const basemap = useBasemapAvailable();
  const opacity = useOpacity((st) => st.opacity);

  const hostRef = useRef<HTMLDivElement>(null);
  const [map, setMap] = useState<MapLibreMap | null>(null);
  const [zoom, setZoom] = useState(search.z ?? DEFAULT_VIEW.zoom);
  const [tab, setTab] = useState("objects");
  const [selection, setSelection] = useState<Selection | null>(null);
  const [hover, setHover] = useState<{ x: number; y: number; title: string; subtitle: string } | null>(null);
  const [objects, setObjects] = useState<ObjectItem[]>([]);
  const [here, setHere] = useState<[number, number] | null>(null);
  // Шкала времени работ: смещение в днях от сегодняшнего дня.
  const [workOffset, setWorkOffset] = useState(0);
  // Рисование: режим инструмента, нарисованное, объект из сводки области.
  const [drawMode, setDrawMode] = useState<DrawMode | null>(null);
  const [drawn, setDrawn] = useState<Drawn | null>(null);
  const [areaFacility, setAreaFacility] = useState<string | undefined>();
  const drawing = useRef(false);
  drawing.current = drawMode !== null;
  // Замыкающий щелчок рисования не должен выбирать объект под ним.
  const drawnAt = useRef(0);

  const catalog = useMemo(() => catalogQuery.data?.layers ?? [], [catalogQuery.data]);
  const visible = useMemo(() => visibleLayers(catalog, search.l), [catalog, search.l]);
  // Цвета — из токенов действующей темы: к моменту отрисовки тема уже применена к документу,
  // поэтому палитра перечитывается при её смене.
  const palette = useMemo(() => {
    void theme;
    return readPalette();
  }, [theme]);

  // Точки объектов для кластеров на мелких масштабах.
  const pointsQuery = $api.useQuery("get", "/facilities/points", {}, { staleTime: 300_000 });
  const points = useMemo(() => (pointsQuery.data ? toPoints(pointsQuery.data) : undefined), [pointsQuery.data]);
  const pointsRef = useRef(points);
  pointsRef.current = points;
  const searchRef = useRef(search);
  searchRef.current = search;

  const data = useMemo(() => {
    const day = new Date();
    day.setDate(day.getDate() + workOffset);
    const filters = { road_works: workDayFilter(dayNumber(day)), ...facilityFilters(search) };
    return buildDataLayers(catalog, layerStates(catalog, visible, opacity, filters), palette, undefined, points);
  }, [catalog, visible, opacity, palette, workOffset, search, points]);
  const style = useMemo(() => {
    if (basemap.data === undefined) {
      return null;
    }
    const base = basemap.data ? basemapStyle(theme) : emptyStyle(theme, palette);
    return fullStyle(base, catalog, data, drawnOverlay(drawn, palette, parseCenter(search.pin), search.pt));
  }, [basemap.data, theme, palette, catalog, data, drawn, search.pin, search.pt]);

  const interactive = useRef<string[]>([]);
  interactive.current = interactiveLayerIds(data.layers);
  const catalogRef = useRef(catalog);
  catalogRef.current = catalog;

  // Создание карты при первом стиле; один экземпляр на рабочее пространство. Вид из адреса
  // берётся один раз, дальнейшие изменения стиля и вида — в эффектах ниже.
  const initialView = useRef({
    center: parseCenter(search.c) ?? DEFAULT_VIEW.center,
    zoom: search.z ?? DEFAULT_VIEW.zoom,
  });
  const latestStyle = useRef(style);
  latestStyle.current = style;
  const ready = style !== null;
  useEffect(() => {
    const st = latestStyle.current;
    if (!ready || !hostRef.current || !st) {
      return;
    }
    setMap(mountMap(hostRef.current, { ...initialView.current, style: st }));
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

  // Вид карты — в адрес (без записи в историю).
  useEffect(() => {
    if (!map) {
      return;
    }
    const onMove = () => {
      // Конец движения может прийти уже после перехода на другую страницу (анимация не
      // закончилась) — тогда адрес не трогается, иначе переход отменился бы.
      if (router.state.location.pathname !== "/map") {
        return;
      }
      const c = map.getCenter();
      const z = Math.round(map.getZoom() * 100) / 100;
      setZoom(z);
      void navigate({ search: (prev: MapSearch) => ({ ...prev, c: formatCenter(c.lng, c.lat), z }), replace: true });
    };
    map.on("moveend", onMove);
    return () => {
      map.off("moveend", onMove);
    };
  }, [map, navigate, router]);

  // Список объектов видимой области.
  useEffect(() => {
    if (!map) {
      return;
    }
    const collect = () => {
      const id = layerId("facilities", "circle");
      if (!map.getLayer(id)) {
        setObjects([]);
        return;
      }
      // На мелких масштабах точки собраны в кластеры — список строится по самим точкам.
      const pts = pointsRef.current;
      if (pts && map.getZoom() < CLUSTER_UNTIL) {
        const b = map.getBounds();
        const items = pts.features
          .filter((f) => b.contains(f.geometry.coordinates as [number, number]))
          .filter((f) => matchesFacilityFilter(searchRef.current, f.properties ?? {}))
          .map((f) => toObject({ ...f, id: f.id, source: POINTS_SOURCE } as unknown as MapGeoJSONFeature));
        setObjects(items);
        return;
      }
      const seen = new Map<string, ObjectItem>();
      for (const f of map.queryRenderedFeatures({ layers: [id] })) {
        const key = String(f.id);
        if (!seen.has(key)) {
          seen.set(key, toObject(f));
        }
      }
      setObjects([...seen.values()].sort((a, b) => (b.area ?? 0) - (a.area ?? 0)));
    };
    map.on("idle", collect);
    return () => {
      map.off("idle", collect);
    };
  }, [map]);

  // Наведение и выбор.
  useEffect(() => {
    if (!map) {
      return;
    }
    let hovered: MapGeoJSONFeature | null = null;
    const setState = (f: MapGeoJSONFeature | null, state: Record<string, boolean>) => {
      if (f?.id !== undefined && f.source && map.getSource(f.source)) {
        map.setFeatureState({ source: f.source, sourceLayer: f.sourceLayer, id: f.id }, state);
      }
    };
    const pick = (point: { x: number; y: number }, includeBoundaries: boolean) => {
      const layers = interactive.current.filter((id) => map.getLayer(id));
      if (layers.length === 0) {
        return null;
      }
      const found = map.queryRenderedFeatures([point.x, point.y], { layers });
      return found.find((f) => includeBoundaries || codeOf(f.layer.id) !== "boundaries") ?? null;
    };
    const onMove = (e: { point: { x: number; y: number } }) => {
      const f = pick(e.point, false);
      if (hovered && (!f || f.id !== hovered.id || f.source !== hovered.source)) {
        setState(hovered, { hover: false });
        hovered = null;
      }
      map.getCanvas().style.cursor = f ? "pointer" : "";
      if (!f) {
        setHover(null);
        return;
      }
      hovered = f;
      setState(f, { hover: true });
      if (isClusterLayer(f.layer.id)) {
        const n = Number((f.properties as Record<string, unknown>).point_count ?? 0);
        setHover({ x: e.point.x, y: e.point.y, title: `Объектов: ${num(n)}`, subtitle: "Щёлкните, чтобы приблизить" });
        return;
      }
      const layer = catalogRef.current.find((l) => l.code === codeOf(f.layer.id));
      setHover({ x: e.point.x, y: e.point.y, ...describeFeature(f, layer) });
    };
    const onLeave = () => {
      setState(hovered, { hover: false });
      hovered = null;
      setHover(null);
    };
    const onClick = (e: { point: { x: number; y: number }; lngLat: { lng: number; lat: number } }) => {
      // Во время рисования щелчки — вершины, а не выбор объекта.
      if (drawing.current || Date.now() - drawnAt.current < 500) {
        return;
      }
      const f = pick(e.point, true);
      // Кластер — приблизить до масштаба, где он распадается.
      if (f && isClusterLayer(f.layer.id)) {
        const src = map.getSource<GeoJSONSource>(POINTS_SOURCE);
        const clusterId = Number((f.properties as Record<string, unknown>).cluster_id);
        const center = f.geometry.type === "Point" ? (f.geometry.coordinates as [number, number]) : undefined;
        void src?.getClusterExpansionZoom(clusterId).then((z) => map.easeTo({ center, zoom: z + 0.1, duration: 500 }));
        return;
      }
      const layer = f ? catalogRef.current.find((l) => l.code === codeOf(f.layer.id)) : undefined;
      if (!f || !layer) {
        // Пустое место — «что здесь»: адрес и район по обратному геокодеру.
        setSelection(null);
        setHere([e.lngLat.lng, e.lngLat.lat]);
        return;
      }
      setHere(null);
      setSelection({ layer, feature: f, lngLat: [e.lngLat.lng, e.lngLat.lat] });
    };
    map.on("mousemove", onMove);
    map.on("mouseout", onLeave);
    map.on("click", onClick);
    return () => {
      map.off("mousemove", onMove);
      map.off("mouseout", onLeave);
      map.off("click", onClick);
    };
  }, [map]);

  // Подсветка выбранного объекта.
  useEffect(() => {
    const f = selection?.feature;
    if (!map || !f || f.id === undefined || !map.getSource(f.source)) {
      return;
    }
    const target = { source: f.source, sourceLayer: f.sourceLayer, id: f.id };
    map.setFeatureState(target, { selected: true });
    return () => {
      if (map.getSource(f.source)) {
        map.setFeatureState(target, { selected: false });
      }
    };
  }, [map, selection]);

  const toggleLayer = (code: string, on: boolean) => {
    const next = new Set(visible);
    if (on) {
      next.add(code);
    } else {
      next.delete(code);
    }
    const l = catalog.filter((x) => next.has(x.code)).map((x) => x.code);
    void navigate({ search: (prev: MapSearch) => ({ ...prev, l: l.join(",") }), replace: true });
  };

  // Изображение карты с легендой и источниками.
  const [saving, setSaving] = useState(false);
  const saveImage = async () => {
    if (!map) {
      return;
    }
    setSaving(true);
    try {
      const day = new Date();
      const layers = [...catalog].reverse().filter((l) => visible.has(l.code));
      const blob = await mapImage(map, {
        title: `FreightFlow · Карта Москвы · ${day.toLocaleDateString("ru-RU")}`,
        layers,
        palette,
        basemap: Boolean(basemap.data),
      });
      const url = URL.createObjectURL(blob);
      const a = document.createElement("a");
      a.href = url;
      a.download = `map-${day.toISOString().slice(0, 10)}.png`;
      a.click();
      setTimeout(() => URL.revokeObjectURL(url), 1000);
    } catch {
      toast.error("Не удалось сохранить изображение карты");
    } finally {
      setSaving(false);
    }
  };

  const focusObject = (o: ObjectItem) => {
    if (!map) {
      return;
    }
    map.flyTo({ center: o.lngLat, zoom: Math.max(map.getZoom(), 14), duration: 600 });
    const layer = catalog.find((l) => l.code === "facilities");
    if (layer) {
      setSelection({ layer, feature: o.feature, lngLat: o.lngLat });
    }
  };

  return (
    <div className={s.workspace}>
      <aside className={s.panel} aria-label={m.panelTabs}>
        <div className={s.viewsRow}>
          {hasFacilityFilter(search) && (
            <span className={s.registryChip}>
              Объекты — по фильтру реестра
              <button
                type="button"
                aria-label="Снять фильтр реестра"
                onClick={() =>
                  void navigate({
                    search: (prev: MapSearch) => ({ ...prev, ft: undefined, fc: undefined, fm: undefined }),
                    replace: true,
                  })
                }
              >
                <X size={12} aria-hidden />
              </button>
            </span>
          )}
          <ViewsMenu
            page="map"
            params={{ c: search.c, z: search.z, l: search.l, ft: search.ft, fc: search.fc, fm: search.fm }}
            onApply={(p) => {
              const c = parseCenter(typeof p.c === "string" ? p.c : undefined);
              const z = typeof p.z === "number" ? p.z : undefined;
              if (map && c) {
                map.jumpTo({ center: c, zoom: z ?? map.getZoom() });
              }
              void navigate({
                search: {
                  c: typeof p.c === "string" ? p.c : undefined,
                  z,
                  l: typeof p.l === "string" ? p.l : undefined,
                  ft: typeof p.ft === "string" ? p.ft : undefined,
                  fc: typeof p.fc === "number" ? p.fc : undefined,
                  fm: typeof p.fm === "number" ? p.fm : undefined,
                },
                replace: true,
              });
            }}
          />
        </div>
        <Tabs
          label={m.panelTabs}
          selected={tab}
          onChange={setTab}
          items={[
            {
              id: "objects",
              title: m.tabObjects,
              content: (
                <ObjectsPanel
                  items={objects}
                  layerOn={visible.has("facilities")}
                  zoom={zoom}
                  selectedId={selection?.layer.code === "facilities" ? selection.feature.id : undefined}
                  onSelect={focusObject}
                />
              ),
            },
            {
              id: "layers",
              title: m.tabLayers,
              content: (
                <LayersPanel
                  catalog={catalogQuery.data}
                  isError={catalogQuery.isError}
                  visible={visible}
                  zoom={zoom}
                  onToggle={toggleLayer}
                />
              ),
            },
          ]}
        />
      </aside>
      <section className={s.mapArea} aria-label={m.mapLabel}>
        <div ref={hostRef} className={s.mapHost} />
        {basemap.data === false && (
          <div className={`${s.float} ${s.notice}`} role="status">
            <b>{m.noBasemap}</b>
            <span>{m.noBasemapHint}</span>
          </div>
        )}
        {visible.has("road_works") && <WorkDate offset={workOffset} onChange={setWorkOffset} />}
        <div className={`${s.float} ${s.zoom}`}>
          <IconButton label={m.zoomIn} onPress={() => map?.zoomIn()}>
            <Plus size={17} />
          </IconButton>
          <IconButton label={m.zoomOut} onPress={() => map?.zoomOut()}>
            <Minus size={17} />
          </IconButton>
        </div>
        <div className={`${s.float} ${s.exportTool}`}>
          <IconButton label="Сохранить изображение карты" onPress={() => void saveImage()} isDisabled={saving || !map}>
            <ImageDown size={17} />
          </IconButton>
        </div>
        <AreaTool
          map={map}
          mode={drawMode}
          onMode={setDrawMode}
          onDrawn={(d) => {
            drawnAt.current = Date.now();
            setDrawn(d);
          }}
        />
        {drawn && drawn.mode === "measure" && drawn.geometry.type === "LineString" && (
          <div className={`${s.float} ${s.measure}`} role="status">
            Расстояние: {distanceText(lineLength(drawn.geometry.coordinates))}
            <IconButton label="Убрать измерение" onPress={() => setDrawn(null)}>
              <X size={14} />
            </IconButton>
          </div>
        )}
        {drawn && drawn.mode === "area" && isPolygon(drawn.geometry) && (
          <AreaSummary polygon={drawn.geometry} onOpen={setAreaFacility} onClose={() => setDrawn(null)} />
        )}
        <Legend catalog={catalog} visible={visible} palette={palette} />
        <div className={s.attribution}>
          {basemap.data && (
            <>
              <a href="https://protomaps.com" target="_blank" rel="noopener noreferrer">
                Protomaps
              </a>{" "}
              ·{" "}
            </>
          )}
          <a href="https://www.openstreetmap.org/copyright" target="_blank" rel="noopener noreferrer">
            © участники OpenStreetMap
          </a>
          {" · "}
          <a href="https://maplibre.org" target="_blank" rel="noopener noreferrer">
            MapLibre
          </a>
        </div>
        {here && <WhatsHere point={here} onClose={() => setHere(null)} />}
        {hover && (
          <div className={s.hoverTip} style={{ left: hover.x + 14, top: hover.y + 14 }} aria-hidden>
            <b>{hover.title}</b>
            <span>{hover.subtitle}</span>
          </div>
        )}
      </section>
      {areaFacility && <FacilityDrawer id={areaFacility} onClose={() => setAreaFacility(undefined)} />}
      {selection &&
        (facilityId(selection) ? (
          <FacilityDrawer id={facilityId(selection)} onClose={() => setSelection(null)} />
        ) : (
          <FeatureCard selection={selection} onClose={() => setSelection(null)} />
        ))}
    </div>
  );
}

function toObject(f: MapGeoJSONFeature): ObjectItem {
  const p = f.properties as Record<string, unknown>;
  const coords = f.geometry.type === "Point" ? (f.geometry.coordinates as [number, number]) : DEFAULT_VIEW.center;
  return {
    id: f.id,
    name: typeof p.name === "string" ? p.name : undefined,
    address: typeof p.address === "string" ? p.address : undefined,
    type: String(p.type ?? ""),
    area: typeof p.area === "number" ? p.area : undefined,
    lngLat: coords,
    feature: f,
  };
}

/** Подпись объекта для подсказки: название или вид, ниже — слой. */
function describeFeature(f: MapGeoJSONFeature, layer: MapLayer | undefined): { title: string; subtitle: string } {
  const p = f.properties as Record<string, unknown>;
  const enumProp = layer?.properties.find((x) => x.kind === "enum" && p[x.name] !== undefined);
  const enumLabel = enumProp ? enumProp.labels?.[String(p[enumProp.name])] : undefined;
  const label = typeof p.name === "string" && p.name ? p.name : typeof p.address === "string" ? p.address : undefined;
  const name = label ?? enumLabel ?? layer?.title ?? "";
  return { title: name, subtitle: layer?.title ?? "" };
}

/** Объект реестра под выбранным элементом карты — карточка та же, что в реестре. */
function facilityId(sel: Selection): string | undefined {
  const id = (sel.feature.properties as Record<string, unknown>).id;
  return (sel.layer.code === "facilities" || sel.layer.code === "facility_areas") && typeof id === "string"
    ? id
    : undefined;
}

/** Расстояние измерения: «850 м», «12,4 км». */
function distanceText(m: number): string {
  return m < 1000 ? `${num(Math.round(m))} м` : `${num(m / 1000, m < 10_000 ? 2 : 1)} км`;
}

/** Точки объектов — GeoJSON со свойствами тех же имён, что в тайлах слоя (для общего отбора). */
function toPoints(list: Schemas["FacilityPoint"][]): FeatureCollection<Point> {
  return {
    type: "FeatureCollection",
    features: list.map(
      (p): Feature<Point> => ({
        type: "Feature",
        id: p.id,
        geometry: { type: "Point", coordinates: [p.lon, p.lat] },
        properties: {
          id: p.id,
          type: p.type,
          name: p.name,
          area: p.area,
          confidence: p.confidence,
          mkad_min: p.mkadMin,
        },
      }),
    ),
  };
}

/** Нарисованное и метка найденного места — слоями страницы поверх данных. */
function drawnOverlay(d: Drawn | null, p: Palette, pin: [number, number] | null, pinLabel: string | undefined) {
  const data = {
    type: "FeatureCollection" as const,
    features: d ? [{ type: "Feature" as const, properties: {}, geometry: d.geometry }] : [],
  };
  const pins = {
    type: "FeatureCollection" as const,
    features: pin
      ? [
          {
            type: "Feature" as const,
            properties: { label: pinLabel ?? "" },
            geometry: { type: "Point" as const, coordinates: pin },
          },
        ]
      : [],
  };
  return {
    sources: {
      "draw:result": { type: "geojson" as const, data },
      "search:pin": { type: "geojson" as const, data: pins },
    },
    layers: [
      {
        id: "draw:fill",
        type: "fill" as const,
        source: "draw:result",
        filter: ["==", ["geometry-type"], "Polygon"] as ExpressionSpecification,
        paint: { "fill-color": color(p, "acc"), "fill-opacity": 0.1 },
      },
      {
        id: "draw:line",
        type: "line" as const,
        source: "draw:result",
        paint: { "line-color": color(p, "acc"), "line-width": 2, "line-dasharray": [2, 1] },
      },
      {
        id: "search:pin",
        type: "circle" as const,
        source: "search:pin",
        paint: {
          "circle-radius": 8,
          "circle-color": color(p, "crit"),
          "circle-stroke-color": color(p, "panel"),
          "circle-stroke-width": 3,
        },
      },
      {
        id: "search:pin-label",
        type: "symbol" as const,
        source: "search:pin",
        layout: {
          "text-field": ["get", "label"] as ExpressionSpecification,
          "text-font": ["Noto Sans Medium"],
          "text-size": 13,
          "text-offset": [0, 1.4] as [number, number],
          "text-anchor": "top" as const,
          "text-max-width": 16,
        },
        paint: { "text-color": color(p, "tx"), "text-halo-color": color(p, "panel"), "text-halo-width": 1.5 },
      },
    ],
  };
}
