import { Link } from "@tanstack/react-router";
import type { Geometry } from "geojson";
import { Download, Minus, Network, Plus, Route, Trash2, X } from "lucide-react";
import type {
  ExpressionSpecification,
  GeoJSONSourceSpecification,
  LayerSpecification,
  Map as MapLibreMap,
} from "maplibre-gl";
import { useEffect, useMemo, useRef, useState } from "react";
import { Guard } from "@/app/Guard";
import { $api, problemMessage, type Schemas } from "@/shared/api/client";
import { num } from "@/shared/format";
import { basemapStyle } from "@/shared/map/basemap";
import { buildDataLayers, color, readPalette } from "@/shared/map/dataLayers";
import { DEFAULT_VIEW, mountMap, onStyleReady, unmountMap } from "@/shared/map/instance";
import { emptyStyle, fullStyle, useBasemapAvailable } from "@/shared/map/style";
import { Badge, Button, Checkbox, IconButton, Select, Switch, TextField } from "@/shared/ui";
import { Tabs } from "@/shared/ui/navigation";
import { useResolvedTheme } from "@/shared/useResolvedTheme";
import { layerStates } from "../map/state";
import { routeGeoJSON, routeGPX, saveFile } from "./exportRoute";
import { passabilityFilter } from "./filters";
import {
  difference,
  duration,
  passZoneLabel,
  passZones,
  pointLabel,
  restrictionText,
  ruleHint,
  zoneName,
} from "./labels";
import { PlaceSearch } from "./PlaceSearch";
import s from "./route.module.css";

type Result = Schemas["RouteResult"];
type IsoResult = Schemas["IsochroneResult"];
type Mode = "route" | "isochrone";

/** Время изохрон на выбор, минуты. */
const isoMinutes = [15, 30, 45, 60];
type Vehicle = Schemas["VehicleParams"];

/** Слои каталога под маршрутом: кольца зон и ограничения для грузовиков. */
const contextLayers = new Set(["rings", "restrictions"]);
const maxPoints = 25;

export function RoutePage() {
  return (
    <Guard permission="routing.use">
      <RouteWorkspace />
    </Guard>
  );
}

/** Значение поля ввода по-русски («3,5») в число. */
function parseNum(v: string): number | undefined {
  const n = Number(v.replace(",", ".").trim());
  return v.trim() !== "" && Number.isFinite(n) ? n : undefined;
}

function nowLocal(): string {
  const d = new Date();
  d.setMinutes(d.getMinutes() - d.getTimezoneOffset());
  return d.toISOString().slice(0, 16);
}

function RouteWorkspace() {
  const theme = useResolvedTheme();
  const basemap = useBasemapAvailable();
  const catalogQuery = $api.useQuery("get", "/layers", {}, { staleTime: 60_000 });
  const profiles = $api.useQuery("get", "/routing/profiles");
  const status = $api.useQuery("get", "/routing/status", {}, { staleTime: 30_000 });
  const build = $api.useMutation("post", "/routing/route");
  const iso = $api.useMutation("post", "/routing/isochrone");
  const [mode, setMode] = useState<Mode>("route");
  const [minutes, setMinutes] = useState<Set<number>>(new Set([15, 30, 60]));
  const [isoResult, setIsoResult] = useState<IsoResult | null>(null);
  const [onlyBlocking, setOnlyBlocking] = useState(true);

  const [points, setPoints] = useState<[number, number][]>([]);
  // Названия точек, найденных поиском: «долгота,широта» → адрес или объект.
  const [pointNames, setPointNames] = useState<Record<string, string>>({});
  const [focus, setFocus] = useState<[number, number] | null>(null);
  const [profileCode, setProfileCode] = useState<string | null>(null);
  const [fields, setFields] = useState<Record<keyof Vehicle, string>>({
    grossT: "",
    payloadT: "",
    axleLoadT: "",
    heightM: "",
    widthM: "",
    lengthM: "",
    ecoClass: "",
    hazmat: "",
  });
  const [passes, setPasses] = useState<Set<string>>(new Set());
  const [depart, setDepart] = useState(nowLocal);
  const [result, setResult] = useState<Result | null>(null);

  // Профиль по умолчанию — «Грузовик 20 т» или первый в списке.
  useEffect(() => {
    const list = profiles.data ?? [];
    if (profileCode || list.length === 0) {
      return;
    }
    const p = list.find((x) => x.code === "truck_20") ?? list[0];
    if (p) {
      selectProfile(p);
    }
  });

  function addPoint(p: [number, number]) {
    setPoints((ps) => (mode === "isochrone" ? [p] : ps.length >= maxPoints ? ps : [...ps, p]));
    reset();
  }

  function selectProfile(p: Schemas["VehicleProfile"]) {
    setProfileCode(p.code);
    setFields({
      grossT: String(p.grossT).replace(".", ","),
      payloadT: String(p.payloadT).replace(".", ","),
      axleLoadT: String(p.axleLoadT).replace(".", ","),
      heightM: String(p.heightM).replace(".", ","),
      widthM: String(p.widthM).replace(".", ","),
      lengthM: String(p.lengthM).replace(".", ","),
      ecoClass: String(p.ecoClass),
      hazmat: p.hazmat ? "1" : "",
    });
  }

  const vehicle: Vehicle | null = useMemo(() => {
    const gross = parseNum(fields.grossT);
    const height = parseNum(fields.heightM);
    const width = parseNum(fields.widthM);
    const length = parseNum(fields.lengthM);
    if (gross === undefined || height === undefined || width === undefined || length === undefined) {
      return null;
    }
    return {
      grossT: gross,
      heightM: height,
      widthM: width,
      lengthM: length,
      payloadT: parseNum(fields.payloadT),
      axleLoadT: parseNum(fields.axleLoadT),
      ecoClass: parseNum(fields.ecoClass),
      hazmat: fields.hazmat === "1",
    };
  }, [fields]);

  const passList = () =>
    [...passes].map((p) => {
      const [zone, kind] = p.split(":") as [Schemas["VehiclePass"]["zone"], Schemas["VehiclePass"]["kind"]];
      return { zone, kind };
    });
  const needed = mode === "route" ? 2 : 1;

  const run = () => {
    const first = points[0];
    if (!vehicle || !first || points.length < needed) {
      return;
    }
    if (mode === "isochrone") {
      iso.mutate(
        {
          body: {
            origin: { lon: first[0], lat: first[1] },
            vehicle,
            passes: passList(),
            depart: new Date(depart).toISOString(),
            minutes: [...minutes].sort((a, b) => a - b),
          },
        },
        { onSuccess: setIsoResult, onError: () => setIsoResult(null) },
      );
      return;
    }
    build.mutate(
      {
        body: {
          points: points.map(([lon, lat]) => ({ lon, lat })),
          vehicle,
          passes: passList(),
          depart: new Date(depart).toISOString(),
          alternates: 1,
        },
      },
      { onSuccess: setResult, onError: () => setResult(null) },
    );
  };

  // Новая точка или профиль — прежний результат больше не соответствует запросу.
  const reset = () => {
    setResult(null);
    setIsoResult(null);
    build.reset();
    iso.reset();
  };

  return (
    <div className={s.workspace}>
      <aside className={s.panel} aria-label="Построение маршрута">
        <div className={s.titleRow}>
          <h1 className={s.title}>{mode === "route" ? "Маршрут грузовика" : "Изохроны доступности"}</h1>
          <Link to="/network/analysis" className={s.sectionLink}>
            <Network size={14} aria-hidden /> Анализ сети
          </Link>
        </div>
        <Tabs
          label="Режим"
          selected={mode}
          onChange={(id) => {
            setMode(id as Mode);
            setPoints((ps) => (id === "isochrone" ? ps.slice(0, 1) : ps));
            reset();
          }}
          items={[
            { id: "route", title: "Маршрут", content: null },
            { id: "isochrone", title: "Изохроны", content: null },
          ]}
        />
        {status.data && !status.data.available && (
          <div className="notice notice-error" role="status">
            Служба маршрутизации не запущена: граф собирается командой task routing:build, служба — task routing:up.
          </div>
        )}
        <section className={s.block}>
          <h2>Точки</h2>
          {points.length === 0 ? (
            <p className={s.muted}>
              {mode === "route"
                ? "Щёлкните на карте: первая точка — отправление, следующие — промежуточные и назначение."
                : "Щёлкните на карте — точка отправления."}
            </p>
          ) : (
            <ol className={s.points}>
              {points.map(([lon, lat], i) => (
                <li key={`${lon},${lat}`}>
                  <span className={s.pointMark}>{pointLabel(i, points.length)}</span>
                  {pointNames[`${lon},${lat}`] ? (
                    <span className={s.pointName}>{pointNames[`${lon},${lat}`]}</span>
                  ) : (
                    <span className="num">
                      {num(lat, 5)}, {num(lon, 5)}
                    </span>
                  )}
                  <IconButton
                    label={`Убрать точку ${pointLabel(i, points.length)}`}
                    onPress={() => {
                      setPoints((ps) => ps.filter((_, j) => j !== i));
                      reset();
                    }}
                  >
                    <X size={15} />
                  </IconButton>
                </li>
              ))}
            </ol>
          )}
          <PlaceSearch
            label={mode === "route" ? "Добавить точку по адресу или объекту" : "Точка по адресу или объекту"}
            onSelect={(p) => {
              const pt: [number, number] = [p.point.lon, p.point.lat];
              setPointNames((n) => ({ ...n, [`${pt[0]},${pt[1]}`]: p.title }));
              addPoint(pt);
              setFocus(pt);
            }}
          />
          {points.length > 0 && (
            <Button
              size="sm"
              variant="ghost"
              onPress={() => {
                setPoints([]);
                reset();
              }}
            >
              <Trash2 size={14} aria-hidden /> Очистить
            </Button>
          )}
        </section>

        <section className={s.block}>
          <h2>Транспортное средство</h2>
          <Select
            label="Профиль"
            options={(profiles.data ?? []).map((p) => ({ id: p.code, label: p.name }))}
            value={profileCode}
            onChange={(code) => {
              const p = profiles.data?.find((x) => x.code === code);
              if (p) {
                selectProfile(p);
                reset();
              }
            }}
          />
          <div className={s.grid}>
            <TextField
              label="Масса (РММ), т"
              value={fields.grossT}
              onChange={(v) => setFields({ ...fields, grossT: v })}
            />
            <TextField
              label="Грузоподъёмность, т"
              value={fields.payloadT}
              onChange={(v) => setFields({ ...fields, payloadT: v })}
            />
            <TextField
              label="Высота, м"
              value={fields.heightM}
              onChange={(v) => setFields({ ...fields, heightM: v })}
            />
            <TextField label="Ширина, м" value={fields.widthM} onChange={(v) => setFields({ ...fields, widthM: v })} />
            <TextField label="Длина, м" value={fields.lengthM} onChange={(v) => setFields({ ...fields, lengthM: v })} />
            <TextField
              label="Нагрузка на ось, т"
              value={fields.axleLoadT}
              onChange={(v) => setFields({ ...fields, axleLoadT: v })}
            />
            <TextField
              label="Экокласс"
              value={fields.ecoClass}
              onChange={(v) => setFields({ ...fields, ecoClass: v })}
            />
          </div>
          <Checkbox isSelected={fields.hazmat === "1"} onChange={(v) => setFields({ ...fields, hazmat: v ? "1" : "" })}>
            Опасный груз
          </Checkbox>
          <Switch isSelected={onlyBlocking} onChange={setOnlyBlocking}>
            На карте — только ограничения, непроходимые для этого ТС
          </Switch>
        </section>

        <section className={s.block}>
          <h2>Пропуска</h2>
          <fieldset className={s.passes}>
            <legend className="visually-hidden">Пропуска по зонам</legend>
            {passZones.map((z) => (
              <div key={z} className={s.passRow}>
                <span>{passZoneLabel[z]}</span>
                {(["day", "night"] as const).map((k) => {
                  const id = `${z}:${k}`;
                  return (
                    <Checkbox
                      key={id}
                      isSelected={passes.has(id)}
                      onChange={(on) => {
                        const next = new Set(passes);
                        if (on) {
                          next.add(id);
                        } else {
                          next.delete(id);
                        }
                        setPasses(next);
                      }}
                    >
                      {k === "day" ? "дневной" : "ночной"}
                      <span className="visually-hidden"> пропуск зоны {passZoneLabel[z]}</span>
                    </Checkbox>
                  );
                })}
              </div>
            ))}
          </fieldset>
          <TextField label="Отправление" type="datetime-local" value={depart} onChange={setDepart} />
        </section>

        {mode === "isochrone" && (
          <section className={s.block}>
            <h2>Время в пути</h2>
            <fieldset className={s.minutes}>
              <legend className="visually-hidden">Контуры, минут</legend>
              {isoMinutes.map((mn) => (
                <Checkbox
                  key={mn}
                  isSelected={minutes.has(mn)}
                  onChange={(on) => {
                    const next = new Set(minutes);
                    if (on) {
                      next.add(mn);
                    } else if (next.size > 1) {
                      next.delete(mn);
                    }
                    setMinutes(next);
                    setIsoResult(null);
                  }}
                >
                  {mn} мин
                </Checkbox>
              ))}
            </fieldset>
          </section>
        )}

        <Button
          variant="primary"
          block
          isDisabled={!vehicle || points.length < needed || build.isPending || iso.isPending}
          onPress={run}
        >
          <Route size={15} aria-hidden />{" "}
          {build.isPending || iso.isPending
            ? "Строится…"
            : mode === "route"
              ? "Построить маршрут"
              : "Построить изохроны"}
        </Button>
        {(build.error || iso.error) && (
          <div className="notice notice-error" role="alert">
            {problemMessage(build.error ?? iso.error)}
          </div>
        )}
        {mode === "route" && result && <Explanation result={result} points={points.length} />}
        {mode === "isochrone" && isoResult && <IsochroneExplanation result={isoResult} />}
      </aside>
      <RouteMap
        theme={theme}
        basemap={basemap.data}
        catalog={catalogQuery.data?.layers ?? []}
        points={points}
        result={mode === "route" ? result : null}
        isochrones={mode === "isochrone" ? isoResult : null}
        restrictionFilter={onlyBlocking && vehicle ? passabilityFilter(vehicle) : undefined}
        focus={focus}
        onAdd={addPoint}
      />
    </div>
  );
}

function Explanation({ result, points }: { result: Result; points: number }) {
  const main = result.routes[0];
  const closed = result.zones.filter((z) => z.forbidden);
  return (
    <section className={s.result} aria-label="Результат">
      {result.blocked.length > 0 && (
        <div className="notice notice-error" role="alert">
          <div>
            {result.blocked.map((b) => (
              <p key={b.index}>
                Точка {pointLabel(b.index, points)} в зоне «{zoneName[b.zone]}», закрытой для этого ТС.
              </p>
            ))}
            <p>Нужен пропуск соответствующей зоны или ТС, на которое ограничение не распространяется.</p>
          </div>
        </div>
      )}
      {main && (
        <div className={s.summary}>
          <div>
            <div className={s.metricLabel}>Маршрут грузовика</div>
            <div className={s.metric}>
              {duration(main.timeS)} · <span className="num">{num(main.lengthKm, 1)}</span> км
            </div>
          </div>
          {result.baseline && (
            <div>
              <div className={s.metricLabel}>Без ограничений (легковой)</div>
              <div className={s.metricSecondary}>
                {duration(result.baseline.timeS)} · <span className="num">{num(result.baseline.lengthKm, 1)}</span> км
              </div>
              <div className={s.muted}>разница {difference(main, result.baseline)}</div>
            </div>
          )}
          {result.routes.length > 1 && (
            <div className={s.muted}>Альтернатив: {result.routes.length - 1} (на карте — тоньше)</div>
          )}
        </div>
      )}

      <h2>Зоны</h2>
      <ul className={s.zones}>
        {result.zones
          .slice()
          .sort((a, b) => passZones.indexOf(a.zone) - passZones.indexOf(b.zone))
          .map((z) => (
            <li key={z.zone}>
              <div className={s.zoneHead}>
                <span>{zoneName[z.zone]}</span>
                {z.forbidden ? <Badge tone="crit">закрыта</Badge> : <Badge tone="ok">открыта</Badge>}
                {z.unconfirmed && <Badge tone="warn">правило не подтверждено</Badge>}
              </div>
              {z.rules
                .filter((r) => r.blocking || r.note === "eco_unknown")
                .map((r) => (
                  <div key={r.code} className={s.muted}>
                    {ruleHint(r)}
                    {r.basis && <> · {r.basis}</>}
                  </div>
                ))}
            </li>
          ))}
      </ul>

      {(result.avoidedZones.length > 0 || result.avoided.length > 0) && (
        <>
          <h2>Что обойдено</h2>
          {result.avoidedZones.length > 0 && (
            <p>
              Путь без ограничений проходит через закрытые зоны:{" "}
              {result.avoidedZones.map((z) => `«${zoneName[z]}»`).join(", ")}.
            </p>
          )}
          {result.avoided.length > 0 && (
            <ul className={s.avoided}>
              {result.avoided.map((a) => (
                <li key={`${a.kind}-${a.street}-${a.value ?? ""}`}>
                  <b>{restrictionText(a)}</b> — {a.street || "улица без названия"}
                  <span className={s.muted}>
                    {" "}
                    · {num(a.lengthM)} м ·{" "}
                    {a.origins.map((o) => (o === "sign" ? "знак" : o === "osm" ? "OSM" : o)).join(", ")}
                  </span>
                </li>
              ))}
            </ul>
          )}
        </>
      )}
      {closed.length === 0 && result.avoided.length === 0 && main && (
        <p className={s.muted}>Ограничений и закрытых зон на пути нет.</p>
      )}
      {main && (
        <div className={s.exports}>
          <Button size="sm" onPress={() => saveFile(routeGeoJSON(result), "route.geojson", "application/geo+json")}>
            <Download size={14} aria-hidden /> GeoJSON
          </Button>
          <Button size="sm" onPress={() => saveFile(routeGPX(result), "route.gpx", "application/gpx+xml")}>
            <Download size={14} aria-hidden /> GPX
          </Button>
        </div>
      )}
    </section>
  );
}

function IsochroneExplanation({ result }: { result: IsoResult }) {
  const closed = result.zones.filter((z) => z.forbidden);
  return (
    <section className={s.result} aria-label="Результат">
      {result.blocked ? (
        <div className="notice notice-error" role="alert">
          Точка отправления в зоне «{zoneName[result.blocked.zone]}», закрытой для этого ТС.
        </div>
      ) : (
        <ul className={s.isoLegend}>
          {result.contours
            .slice()
            .sort((a, b) => a.minutes - b.minutes)
            .map((c) => (
              <li key={c.minutes}>
                <span className={s.isoSwatch} style={{ opacity: swatchOpacity(c.minutes, result.contours) }} /> до{" "}
                {c.minutes} мин
              </li>
            ))}
        </ul>
      )}
      <p className={s.muted}>
        {closed.length > 0
          ? `Закрытые зоны исключены: ${closed.map((z) => `«${zoneName[z.zone]}»`).join(", ")}.`
          : "Закрытых зон для этого ТС нет."}
      </p>
    </section>
  );
}

/** Плотность образца в легенде: сколько контуров накрывает зону этого контура. */
function swatchOpacity(minutes: number, all: IsoResult["contours"]): number {
  const layers = all.filter((c) => c.minutes >= minutes).length;
  return 1 - (1 - isoOpacity(minutes)) ** layers;
}

/** Прозрачность контура: ближние — плотнее. */
function isoOpacity(minutes: number): number {
  // Контуры вложены: у каждого одинаковая прозрачность, ближние зоны темнее за счёт наложения.
  void minutes;
  return 0.12;
}

/** Охват координат линии или многоугольника: [запад, юг, восток, север]. */
function extent(coords: number[][]): [number, number, number, number] | null {
  if (coords.length < 2) {
    return null;
  }
  let [w, sth, e, n] = [180, 90, -180, -90];
  for (const [lon = 0, lat = 0] of coords) {
    w = Math.min(w, lon);
    e = Math.max(e, lon);
    sth = Math.min(sth, lat);
    n = Math.max(n, lat);
  }
  return [w, sth, e, n];
}

function RouteMap({
  theme,
  basemap,
  catalog,
  points,
  result,
  isochrones,
  restrictionFilter,
  focus,
  onAdd,
}: {
  theme: "light" | "dark";
  basemap: boolean | undefined;
  catalog: Schemas["MapLayer"][];
  points: [number, number][];
  result: Result | null;
  isochrones: IsoResult | null;
  restrictionFilter: ExpressionSpecification | undefined;
  focus: [number, number] | null;
  onAdd: (p: [number, number]) => void;
}) {
  const hostRef = useRef<HTMLDivElement>(null);
  const [map, setMap] = useState<MapLibreMap | null>(null);
  const palette = useMemo(() => {
    void theme;
    return readPalette();
  }, [theme]);

  const style = useMemo(() => {
    if (basemap === undefined) {
      return null;
    }
    const base = basemap ? basemapStyle(theme) : emptyStyle(theme, palette);
    const filters: Record<string, ExpressionSpecification> = restrictionFilter
      ? { restrictions: restrictionFilter }
      : {};
    const data = buildDataLayers(catalog, layerStates(catalog, contextLayers, {}, filters), palette);
    return fullStyle(base, catalog, data, overlay(points, result, isochrones, palette));
  }, [basemap, theme, palette, catalog, points, result, isochrones, restrictionFilter]);

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

  const addRef = useRef(onAdd);
  addRef.current = onAdd;
  useEffect(() => {
    if (!map) {
      return;
    }
    const onClick = (e: { lngLat: { lng: number; lat: number } }) => addRef.current([e.lngLat.lng, e.lngLat.lat]);
    map.on("click", onClick);
    map.getCanvas().style.cursor = "crosshair";
    return () => {
      map.off("click", onClick);
      map.getCanvas().style.cursor = "";
    };
  }, [map]);

  // Точка, найденная поиском, — в центр карты.
  useEffect(() => {
    if (map && focus) {
      map.flyTo({ center: focus, zoom: Math.max(map.getZoom(), 14), duration: 600 });
    }
  }, [map, focus]);

  // Новый маршрут или изохроны — в видимую область (изохроны — по самому дальнему контуру).
  useEffect(() => {
    const far = isochrones?.contours.reduce<IsoResult["contours"][number] | undefined>(
      (a, c) => (!a || c.minutes > a.minutes ? c : a),
      undefined,
    );
    const ring = (far?.geometry as unknown as { coordinates?: number[][][] } | undefined)?.coordinates?.[0];
    const box = extent(ring ?? result?.routes[0]?.coordinates ?? []);
    if (!map || !box) {
      return;
    }
    map.fitBounds(
      [
        [box[0], box[1]],
        [box[2], box[3]],
      ],
      { padding: 60, duration: 0 },
    );
  }, [map, result, isochrones]);

  return (
    <section className={s.mapArea} aria-label="Карта маршрута">
      <div ref={hostRef} className={s.mapHost} />
      <div className={s.zoom}>
        <IconButton label="Приблизить" onPress={() => map?.zoomIn()}>
          <Plus size={17} />
        </IconButton>
        <IconButton label="Отдалить" onPress={() => map?.zoomOut()}>
          <Minus size={17} />
        </IconButton>
      </div>
      {isochrones ? (
        <div className={s.legend} aria-hidden>
          <span className={s.isoSwatch} style={{ opacity: 0.4 }} /> ближе{" "}
          <span className={s.isoSwatch} style={{ opacity: 0.15 }} /> дальше
        </div>
      ) : (
        <div className={s.legend} aria-hidden>
          <span className={s.legendTruck} /> грузовик <span className={s.legendBase} /> без ограничений
        </div>
      )}
    </section>
  );
}

/** Источники и слои: изохроны, путь без ограничений, альтернативы, маршрут грузовика, точки. */
function overlay(
  points: [number, number][],
  result: Result | null,
  isochrones: IsoResult | null,
  p: ReturnType<typeof readPalette>,
) {
  const contours = {
    type: "FeatureCollection" as const,
    // Дальние контуры снизу, ближние поверх.
    features: (isochrones?.contours ?? [])
      .slice()
      .sort((a, b) => b.minutes - a.minutes)
      .map((c) => ({
        type: "Feature" as const,
        properties: { minutes: c.minutes, opacity: isoOpacity(c.minutes) },
        geometry: c.geometry as unknown as Geometry,
      })),
  };
  const lines = {
    type: "FeatureCollection" as const,
    features: [
      ...(result?.baseline
        ? [
            {
              type: "Feature" as const,
              properties: { kind: "baseline" },
              geometry: { type: "LineString" as const, coordinates: result.baseline.coordinates },
            },
          ]
        : []),
      ...(result?.routes ?? []).map((r, i) => ({
        type: "Feature" as const,
        properties: { kind: i === 0 ? "truck" : "alternate" },
        geometry: { type: "LineString" as const, coordinates: r.coordinates },
      })),
    ],
  };
  const marks = {
    type: "FeatureCollection" as const,
    features: points.map((pt, i) => ({
      type: "Feature" as const,
      properties: { label: pointLabel(i, points.length) },
      geometry: { type: "Point" as const, coordinates: pt },
    })),
  };
  const sources: Record<string, GeoJSONSourceSpecification> = {
    "route:iso": { type: "geojson", data: contours },
    "route:lines": { type: "geojson", data: lines },
    "route:points": { type: "geojson", data: marks },
  };
  const layers: LayerSpecification[] = [
    {
      id: "route:iso-fill",
      type: "fill",
      source: "route:iso",
      paint: { "fill-color": color(p, "acc"), "fill-opacity": ["get", "opacity"] },
    },
    {
      id: "route:iso-line",
      type: "line",
      source: "route:iso",
      paint: { "line-color": color(p, "acc"), "line-width": 1.2, "line-opacity": 0.8 },
    },
    {
      id: "route:baseline",
      type: "line",
      source: "route:lines",
      filter: ["==", ["get", "kind"], "baseline"],
      paint: { "line-color": color(p, "n9"), "line-width": 3, "line-dasharray": [2, 1.5] },
    },
    {
      id: "route:alternate",
      type: "line",
      source: "route:lines",
      filter: ["==", ["get", "kind"], "alternate"],
      paint: { "line-color": color(p, "acc"), "line-width": 3, "line-opacity": 0.45 },
    },
    {
      id: "route:casing",
      type: "line",
      source: "route:lines",
      filter: ["==", ["get", "kind"], "truck"],
      layout: { "line-join": "round", "line-cap": "round" },
      paint: { "line-color": color(p, "panel"), "line-width": 8 },
    },
    {
      id: "route:truck",
      type: "line",
      source: "route:lines",
      filter: ["==", ["get", "kind"], "truck"],
      layout: { "line-join": "round", "line-cap": "round" },
      paint: { "line-color": color(p, "acc"), "line-width": 5 },
    },
    {
      id: "route:points",
      type: "circle",
      source: "route:points",
      paint: {
        "circle-radius": 11,
        "circle-color": color(p, "acc"),
        "circle-stroke-color": color(p, "panel"),
        "circle-stroke-width": 2,
      },
    },
    {
      id: "route:labels",
      type: "symbol",
      source: "route:points",
      layout: {
        "text-field": ["get", "label"],
        "text-font": ["Noto Sans Medium"],
        "text-size": 12,
        "text-allow-overlap": true,
      },
      paint: { "text-color": color(p, "acc-on") },
    },
  ];
  return { sources, layers };
}
