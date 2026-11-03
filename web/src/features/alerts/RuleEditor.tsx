import { useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import type { Feature, FeatureCollection, LineString, Polygon } from "geojson";
import { ArrowLeft, PenLine, Save, Trash2, X } from "lucide-react";
import type { LayerSpecification, Map as MapLibreMap } from "maplibre-gl";
import { useEffect, useMemo, useRef, useState } from "react";
import { TerraDraw, TerraDrawLineStringMode, TerraDrawPolygonMode } from "terra-draw";
import { TerraDrawMapLibreGLAdapter } from "terra-draw-maplibre-gl-adapter";
import { Guard } from "@/app/Guard";
import { $api, fetchClient, problemMessage, type Schemas } from "@/shared/api/client";
import { color } from "@/shared/map/dataLayers";
import { Button, Checkbox, IconButton, Select, Switch, TextField } from "@/shared/ui";
import { NumberField } from "@/shared/ui/fields";
import { SegmentedControl } from "@/shared/ui/navigation";
import { toast } from "@/shared/ui/toast";
import { PlaceSearch } from "../network/PlaceSearch";
import {
  coordsOf,
  extentOf,
  MainMap,
  type Overlay,
  type Palette,
  usePalette,
  useScenarioStyle,
} from "../scenarios/ScenarioMap";
import sc from "../scenarios/scenario.module.css";
import { CheckGroup } from "./CheckGroup";
import {
  type Channel,
  channels,
  channelTitle,
  type RuleKind,
  type ScopeType,
  type Severity,
  scopeTitle,
  severityTitle,
} from "./labels";

type Params = Schemas["AlertRuleParams"];

interface Facility {
  id: string;
  name: string;
  lon: number;
  lat: number;
}

export function RuleEditorPage() {
  return (
    <Guard permission="alerts.own">
      <RuleEditor />
    </Guard>
  );
}

/** Рисование линии или многоугольника на карте; по завершении — координаты. */
function useDraw(
  map: MapLibreMap | null,
  mode: "linestring" | "polygon" | null,
  onDone: (c: [number, number][]) => void,
) {
  const done = useRef(onDone);
  done.current = onDone;
  useEffect(() => {
    if (!map || !mode) {
      return;
    }
    const draw = new TerraDraw({
      adapter: new TerraDrawMapLibreGLAdapter({ map, prefixId: "ff-alert-draw" }),
      modes: [new TerraDrawLineStringMode(), new TerraDrawPolygonMode()],
    });
    draw.start();
    draw.setMode(mode);
    draw.on("finish", (id) => {
      const g = draw.getSnapshotFeature(id)?.geometry as LineString | Polygon | undefined;
      const coords = g?.type === "Polygon" ? g.coordinates[0] : g?.coordinates;
      if (coords && coords.length >= 2) {
        done.current(coords.map(([lon = 0, lat = 0]) => [lon, lat] as [number, number]));
      }
    });
    return () => {
      draw.stop();
    };
  }, [map, mode]);
}

function editorOverlay(draft: Feature[], saved: FeatureCollection, points: Feature[], p: Palette): Overlay {
  const layers: LayerSpecification[] = [
    {
      id: "ar-area-fill",
      type: "fill",
      source: "ar:area",
      paint: { "fill-color": color(p, "acc"), "fill-opacity": 0.1 },
    },
    {
      id: "ar-area-line",
      type: "line",
      source: "ar:area",
      paint: { "line-color": color(p, "acc"), "line-width": 1.5, "line-dasharray": [2, 1.5] },
    },
    {
      id: "ar-draft-fill",
      type: "fill",
      source: "ar:draft",
      filter: ["==", ["geometry-type"], "Polygon"],
      paint: { "fill-color": color(p, "acc"), "fill-opacity": 0.16 },
    },
    {
      id: "ar-draft-line",
      type: "line",
      source: "ar:draft",
      layout: { "line-cap": "round", "line-join": "round" },
      paint: { "line-color": color(p, "acc"), "line-width": 4 },
    },
    {
      id: "ar-points",
      type: "circle",
      source: "ar:points",
      paint: {
        "circle-radius": 7,
        "circle-color": color(p, "acc"),
        "circle-stroke-color": color(p, "panel"),
        "circle-stroke-width": 2,
      },
    },
  ];
  return {
    sources: {
      "ar:area": { type: "geojson", data: saved },
      "ar:draft": { type: "geojson", data: { type: "FeatureCollection", features: draft } },
      "ar:points": { type: "geojson", data: { type: "FeatureCollection", features: points } },
    },
    layers,
  };
}

const severities: Severity[] = ["info", "warning", "critical"];

function toggle<T>(list: T[] | undefined, v: T, on: boolean): T[] {
  const cur = list ?? [];
  return on ? [...cur.filter((x) => x !== v), v] : cur.filter((x) => x !== v);
}

function RuleEditor() {
  const { ruleId } = useParams({ strict: false }) as { ruleId?: string };
  const navigate = useNavigate();
  const qc = useQueryClient();
  const options = $api.useQuery("get", "/alert-rules/options", {}, { staleTime: 300_000 });
  const existing = $api.useQuery(
    "get",
    "/alert-rules/{ruleId}",
    { params: { path: { ruleId: ruleId ?? "" } } },
    { enabled: !!ruleId },
  );
  const create = $api.useMutation("post", "/alert-rules");
  const update = $api.useMutation("put", "/alert-rules/{ruleId}");

  const [name, setName] = useState("");
  const [kind, setKind] = useState<RuleKind>("roadworks");
  const [scope, setScope] = useState<ScopeType>("corridor");
  const [line, setLine] = useState<[number, number][]>([]);
  const [bufferM, setBufferM] = useState(200);
  const [ring, setRing] = useState<[number, number][]>([]);
  const [facilities, setFacilities] = useState<Facility[]>([]);
  const [radiusM, setRadiusM] = useState(500);
  const [params, setParams] = useState<Params>({});
  const [severity, setSeverity] = useState<Severity>("warning");
  const [chosen, setChosen] = useState<Channel[]>(["in_app"]);
  const [enabled, setEnabled] = useState(true);
  const [drawing, setDrawing] = useState(false);
  const [map, setMap] = useState<MapLibreMap | null>(null);

  // Правило для правки: поля формы из сохранённого; названия объектов — из реестра.
  const loaded = useRef(false);
  useEffect(() => {
    const r = existing.data;
    if (!r || loaded.current) {
      return;
    }
    loaded.current = true;
    setName(r.name);
    setKind(r.kind);
    setScope(r.scope.type);
    setLine(((r.scope.line?.coordinates as [number, number][] | undefined) ?? []).slice());
    setBufferM(r.scope.bufferM ?? 200);
    setRing(((r.scope.polygon?.coordinates as [number, number][][] | undefined)?.[0] ?? []).slice());
    setRadiusM(r.scope.radiusM ?? 500);
    setParams(r.params);
    setSeverity(r.severity);
    setChosen(r.channels);
    setEnabled(r.enabled);
    const ids = r.scope.facilityIds ?? [];
    void Promise.all(
      ids.map((id) =>
        fetchClient.GET("/facilities/{facilityId}", { params: { path: { facilityId: id } } }).then(({ data }) =>
          data
            ? {
                id,
                name: data.facility.name || data.facility.typeName,
                lon: data.facility.point.lon,
                lat: data.facility.point.lat,
              }
            : { id, name: "Объект", lon: 0, lat: 0 },
        ),
      ),
    ).then(setFacilities);
  }, [existing.data]);

  const kinds = options.data?.kinds ?? [];
  const kindInfo = kinds.find((k) => k.code === kind);
  const scopes = kindInfo?.scopes ?? [];
  // Смена вида: область — первая допустимая, если текущая не подходит.
  useEffect(() => {
    if (scopes.length > 0 && !scopes.includes(scope)) {
      setScope(scopes[0] ?? "none");
    }
  }, [scopes, scope]);

  const drawMode = drawing ? (scope === "corridor" ? "linestring" : scope === "area" ? "polygon" : null) : null;
  useDraw(map, drawMode, (coords) => {
    if (scope === "corridor") {
      setLine(coords);
    } else {
      setRing(coords);
    }
    setDrawing(false);
  });

  const palette = usePalette();
  const draft = useMemo<Feature[]>(() => {
    if (scope === "corridor" && line.length >= 2) {
      return [{ type: "Feature", properties: {}, geometry: { type: "LineString", coordinates: line } }];
    }
    if (scope === "area" && ring.length >= 4) {
      return [{ type: "Feature", properties: {}, geometry: { type: "Polygon", coordinates: [ring] } }];
    }
    return [];
  }, [scope, line, ring]);
  const points = useMemo<Feature[]>(
    () =>
      scope === "facilities"
        ? facilities.map((f) => ({
            type: "Feature",
            properties: { name: f.name },
            geometry: { type: "Point", coordinates: [f.lon, f.lat] },
          }))
        : [],
    [scope, facilities],
  );
  const saved = useMemo<FeatureCollection>(() => {
    const area = existing.data?.area;
    return {
      type: "FeatureCollection",
      features: area ? [{ type: "Feature", properties: {}, geometry: area as unknown as Feature["geometry"] }] : [],
    };
  }, [existing.data?.area]);
  const overlay = useMemo(() => editorOverlay(draft, saved, points, palette), [draft, saved, points, palette]);
  const style = useScenarioStyle(
    useMemo(() => new Set(["road_works"]), []),
    overlay,
  );
  // Охват — при загрузке правила и добавлении объектов, не при каждом изменении рисунка.
  // biome-ignore lint/correctness/useExhaustiveDependencies: рисунок не сдвигает карту
  const fit = useMemo(() => extentOf(coordsOf({ features: [...points, ...saved.features] })), [saved, points.length]);

  const scopeBody = (): Schemas["AlertScope"] => {
    switch (scope) {
      case "corridor":
        return { type: scope, line: { type: "LineString", coordinates: line }, bufferM };
      case "area":
        return { type: scope, polygon: { type: "Polygon", coordinates: [ring] } };
      case "facilities":
        return { type: scope, facilityIds: facilities.map((f) => f.id), radiusM };
      default:
        return { type: scope };
    }
  };
  const ready =
    name.trim() !== "" &&
    (scope !== "corridor" || line.length >= 2) &&
    (scope !== "area" || ring.length >= 4) &&
    (scope !== "facilities" || facilities.length > 0) &&
    (kind !== "accessibility" || !!params.profile);

  const save = () => {
    const body: Schemas["AlertRuleInput"] = {
      name: name.trim(),
      kind,
      scope: scopeBody(),
      params,
      severity,
      channels: chosen.length > 0 ? chosen : ["in_app"],
      enabled,
    };
    const done = () => {
      void qc.invalidateQueries({
        predicate: (q) => typeof q.queryKey[1] === "string" && q.queryKey[1].startsWith("/alert-rules"),
      });
      toast.success(ruleId ? "Правило сохранено" : "Правило создано");
      void navigate({ to: "/alerts", search: { tab: "rules" } });
    };
    if (ruleId) {
      update.mutate({ params: { path: { ruleId } }, body }, { onSuccess: done });
    } else {
      create.mutate({ body }, { onSuccess: done });
    }
  };
  const error = create.error ?? update.error;
  const opt = options.data;
  const hint = drawing
    ? scope === "corridor"
      ? "Щёлкайте по карте вдоль маршрута; повторный щелчок в последнюю точку — конец линии"
      : "Щёлкайте по карте по контуру области; щелчок в первую точку — замкнуть"
    : undefined;

  return (
    <div className={sc.workspace}>
      <aside className={sc.panel} aria-label="Правило оповещения">
        <div className={sc.titleRow}>
          <h1 className={sc.title}>{ruleId ? "Правило" : "Новое правило"}</h1>
          <Link to="/alerts" search={{ tab: "rules" }} className={sc.back}>
            <ArrowLeft size={14} aria-hidden /> К правилам
          </Link>
        </div>
        <TextField label="Название" value={name} onChange={setName} maxLength={200} />
        <Select
          label="Что отслеживать"
          options={kinds.map((k) => ({ id: k.code, label: k.title }))}
          value={kind}
          onChange={(v) => {
            setKind(v as RuleKind);
            setParams({});
          }}
          isDisabled={!!ruleId}
        />
        {kindInfo && <p className={sc.muted}>{kindInfo.description}</p>}

        {scopes.length > 1 && (
          <SegmentedControl
            label="Область"
            value={scope}
            onChange={(v) => setScope(v as ScopeType)}
            options={scopes.map((x) => ({ id: x, label: scopeTitle[x] }))}
          />
        )}
        {scope === "corridor" && (
          <section className={sc.block} aria-label="Коридор">
            <Button size="sm" onPress={() => setDrawing((d) => !d)}>
              <PenLine size={14} aria-hidden />{" "}
              {drawing ? "Отменить рисование" : line.length ? "Нарисовать заново" : "Нарисовать линию"}
            </Button>
            <p className={sc.muted}>{line.length >= 2 ? `Линия из ${line.length} точек` : "Линия не задана"}</p>
            <NumberField
              label="Ширина коридора в каждую сторону, м"
              value={bufferM}
              onChange={(v) => setBufferM(v ?? 200)}
              minValue={50}
              maxValue={2000}
              step={50}
            />
          </section>
        )}
        {scope === "area" && (
          <section className={sc.block} aria-label="Область">
            <Button size="sm" onPress={() => setDrawing((d) => !d)}>
              <PenLine size={14} aria-hidden />{" "}
              {drawing ? "Отменить рисование" : ring.length ? "Нарисовать заново" : "Нарисовать область"}
            </Button>
            <p className={sc.muted}>{ring.length >= 4 ? `Контур из ${ring.length - 1} точек` : "Область не задана"}</p>
          </section>
        )}
        {scope === "facilities" && (
          <section className={sc.block} aria-label="Объекты">
            {facilities.length < 50 && (
              <PlaceSearch
                label="Объект реестра"
                onSelect={(p) => {
                  if (p.kind !== "facility") {
                    toast.success("Выберите объект реестра");
                    return;
                  }
                  setFacilities((list) =>
                    list.some((f) => f.id === p.ref)
                      ? list
                      : [...list, { id: p.ref, name: p.title, lon: p.point.lon, lat: p.point.lat }],
                  );
                }}
              />
            )}
            {facilities.length > 0 && (
              <ul className={sc.list} aria-label="Выбранные объекты">
                {facilities.map((f) => (
                  <li key={f.id} className={sc.item}>
                    <span className={sc.itemTitle}>{f.name}</span>
                    <IconButton
                      label={`Убрать объект «${f.name}»`}
                      onPress={() => setFacilities((list) => list.filter((x) => x.id !== f.id))}
                    >
                      <X size={15} />
                    </IconButton>
                  </li>
                ))}
              </ul>
            )}
            {kind !== "accessibility" && (
              <NumberField
                label="Радиус вокруг объекта, м"
                value={radiusM}
                onChange={(v) => setRadiusM(v ?? 500)}
                minValue={100}
                maxValue={5000}
                step={100}
              />
            )}
          </section>
        )}

        {opt && (
          <section className={sc.block} aria-label="Условия">
            <h2>Условия</h2>
            {kind === "roadworks" && (
              <>
                <CheckGroup label="Виды работ">
                  {opt.workKinds.map((o) => (
                    <Checkbox
                      key={o.code}
                      isSelected={params.workKinds?.includes(o.code as never) ?? false}
                      onChange={(on) =>
                        setParams((p) => ({ ...p, workKinds: toggle(p.workKinds, o.code as never, on) }))
                      }
                    >
                      {o.title}
                    </Checkbox>
                  ))}
                </CheckGroup>
                <p className={sc.muted}>Без отметок — все виды работ.</p>
                <CheckGroup label="О каких изменениях сообщать">
                  {opt.changes.map((o) => (
                    <Checkbox
                      key={o.code}
                      isSelected={(params.changes ?? ["appeared", "started"]).includes(o.code as never)}
                      onChange={(on) =>
                        setParams((p) => ({
                          ...p,
                          changes: toggle(p.changes ?? ["appeared", "started"], o.code as never, on),
                        }))
                      }
                    >
                      {o.title}
                    </Checkbox>
                  ))}
                </CheckGroup>
                <Switch
                  isSelected={params.closureOnly ?? false}
                  onChange={(v) => setParams((p) => ({ ...p, closureOnly: v }))}
                >
                  Только с перекрытием движения
                </Switch>
              </>
            )}
            {kind === "restrictions" && (
              <>
                <CheckGroup label="Виды ограничений">
                  {opt.restrictionKinds.map((o) => (
                    <Checkbox
                      key={o.code}
                      isSelected={params.restrictionKinds?.includes(o.code) ?? false}
                      onChange={(on) =>
                        setParams((p) => ({ ...p, restrictionKinds: toggle(p.restrictionKinds, o.code, on) }))
                      }
                    >
                      {o.title}
                    </Checkbox>
                  ))}
                </CheckGroup>
                <p className={sc.muted}>Без отметок — все ограничения. Первая проверка запоминает текущие.</p>
              </>
            )}
            {kind === "accessibility" && (
              <>
                <Select
                  label="Профиль ТС"
                  options={opt.profiles.map((o) => ({ id: o.code, label: o.title }))}
                  value={params.profile ?? null}
                  onChange={(v) => setParams((p) => ({ ...p, profile: v }))}
                />
                <Select
                  label="Цель"
                  options={opt.targets.map((o) => ({ id: o.code, label: o.title }))}
                  value={params.target ?? "mkad"}
                  onChange={(v) => setParams((p) => ({ ...p, target: v as Params["target"] }))}
                />
                <NumberField
                  label="Сообщать о росте времени от, мин"
                  value={params.thresholdMin ?? 5}
                  onChange={(v) => setParams((p) => ({ ...p, thresholdMin: v ?? undefined }))}
                  minValue={1}
                  maxValue={120}
                />
              </>
            )}
            {kind === "facilities" && (
              <CheckGroup label="Типы объектов">
                {opt.facilityTypes.map((o) => (
                  <Checkbox
                    key={o.code}
                    isSelected={params.facilityTypes?.includes(o.code) ?? false}
                    onChange={(on) => setParams((p) => ({ ...p, facilityTypes: toggle(p.facilityTypes, o.code, on) }))}
                  >
                    {o.title}
                  </Checkbox>
                ))}
              </CheckGroup>
            )}
            {kind === "sources" && (
              <>
                <NumberField
                  label="Данные устарели, если не обновлялись дольше, интервалов обновления"
                  value={params.staleFactor ?? 2}
                  onChange={(v) => setParams((p) => ({ ...p, staleFactor: v ?? undefined }))}
                  minValue={1.5}
                  maxValue={10}
                  step={0.5}
                />
                <p className={sc.muted}>Сбои загрузки сообщаются по всем источникам.</p>
              </>
            )}
          </section>
        )}

        <section className={sc.block} aria-label="Оповещение">
          <h2>Оповещение</h2>
          <SegmentedControl
            label="Важность"
            value={severity}
            onChange={(v) => setSeverity(v as Severity)}
            options={severities.map((x) => ({ id: x, label: severityTitle[x] }))}
          />
          <CheckGroup label="Каналы">
            {channels.map((c) => (
              <Checkbox
                key={c}
                isSelected={chosen.includes(c)}
                onChange={(on) => setChosen((cur) => toggle(cur, c, on))}
              >
                {channelTitle[c]}
              </Checkbox>
            ))}
          </CheckGroup>
          <Switch isSelected={enabled} onChange={setEnabled}>
            Правило включено
          </Switch>
        </section>

        {error && (
          <div className="notice notice-error" role="alert">
            {problemMessage(error)}
          </div>
        )}
        <div className={sc.row}>
          <Button variant="primary" onPress={save} isDisabled={!ready || create.isPending || update.isPending}>
            <Save size={15} aria-hidden /> Сохранить
          </Button>
          {(line.length > 0 || ring.length > 0) && (
            <Button
              variant="ghost"
              onPress={() => {
                setLine([]);
                setRing([]);
              }}
            >
              <Trash2 size={15} aria-hidden /> Очистить рисунок
            </Button>
          )}
        </div>
      </aside>
      <div className={sc.maps}>
        <MainMap style={style} fit={fit} hint={hint} label="Область правила" onMap={setMap} />
      </div>
    </div>
  );
}
