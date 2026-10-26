import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import type { Feature } from "geojson";
import { ArrowLeft, Building2, MapPinned, PenLine, Play, Route as RouteIcon, X } from "lucide-react";
import type { Map as MapLibreMap } from "maplibre-gl";
import { useEffect, useMemo, useState } from "react";
import { Guard } from "@/app/Guard";
import { $api, fetchClient, problemMessage, type Schemas } from "@/shared/api/client";
import { num } from "@/shared/format";
import { Button, Checkbox, IconButton, Select, Switch, TextArea, TextField } from "@/shared/ui";
import { SuggestField } from "@/shared/ui/fields";
import { SegmentedControl } from "@/shared/ui/navigation";
import { toast } from "@/shared/ui/toast";
import { useDebounced } from "@/shared/useDebounced";
import { passZoneLabel, passZones, zoneName } from "../network/labels";
import { PlaceSearch } from "../network/PlaceSearch";
import { type Closure, closureKindTitle, type TargetKind, targetKinds, targetTitle } from "./labels";
import { builderOverlay, geoFeature, pointFeature } from "./layers";
import { coordsOf, extentOf, MainMap, useDrawPolygon, usePalette, useScenarioStyle } from "./ScenarioMap";
import s from "./scenario.module.css";

type Zone = Schemas["VehiclePass"]["zone"];
type ZoneChoice = "keep" | "open" | "closed";
type Tool = "segment" | "area" | "facility" | null;

/** Перекрытие конструктора: параметры и геометрия для карты. */
interface DraftClosure {
  key: string;
  closure: Closure;
  title: string;
  note: string;
  feature: Feature | null;
}

interface IsoFacility {
  id: string;
  name: string;
  lon: number;
  lat: number;
}

const isoChoices = [15, 30, 45, 60];
const maxClosures = 20;
const maxIsoFacilities = 5;
/** Слои каталога под конструктором: кольца зон и критичные элементы сети. */
const context = new Set(["rings", "critical_elements"]);

export function ScenarioBuilderPage() {
  return (
    <Guard permission="scenarios.manage">
      <ScenarioBuilder />
    </Guard>
  );
}

function nowLocal(): string {
  const d = new Date();
  d.setMinutes(d.getMinutes() - d.getTimezoneOffset());
  return d.toISOString().slice(0, 16);
}

function toLocal(iso: string): string {
  const d = new Date(iso);
  d.setMinutes(d.getMinutes() - d.getTimezoneOffset());
  return d.toISOString().slice(0, 16);
}

let seq = 0;
const nextKey = () => `c${++seq}`;

function ScenarioBuilder() {
  const { from, element } = useSearch({ from: "/scenarios/new" });
  const navigate = useNavigate();
  const profiles = $api.useQuery("get", "/routing/profiles");
  const create = $api.useMutation("post", "/scenarios");
  const source = $api.useQuery(
    "get",
    "/scenarios/{scenarioId}",
    { params: { path: { scenarioId: from ?? "" } } },
    {
      enabled: !!from,
    },
  );
  const sourceClosures = $api.useQuery(
    "get",
    "/scenarios/{scenarioId}/closures",
    { params: { path: { scenarioId: from ?? "" } } },
    { enabled: !!from },
  );

  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [profile, setProfile] = useState<string | null>(null);
  const [zones, setZones] = useState(false);
  const [depart, setDepart] = useState(nowLocal);
  const [passes, setPasses] = useState<Set<string>>(new Set());
  const [overrides, setOverrides] = useState<Record<Zone, ZoneChoice>>({ mkad: "keep", ttk: "keep", sk: "keep" });
  const [closures, setClosures] = useState<DraftClosure[]>([]);
  const [facility, setFacility] = useState<{ name: string; lon: number; lat: number } | null>(null);
  const [targets, setTargets] = useState<Set<TargetKind>>(new Set(["mkad"]));
  const [isoMinutes, setIsoMinutes] = useState<Set<number>>(new Set([15, 30]));
  const [isoFacilities, setIsoFacilities] = useState<IsoFacility[]>([]);
  const [tool, setTool] = useState<Tool>(null);
  const [map, setMap] = useState<MapLibreMap | null>(null);
  const [fit, setFit] = useState<ReturnType<typeof extentOf>>(null);

  // Профиль по умолчанию — грузовик 12 т (как в сетевом анализе) или первый в списке.
  useEffect(() => {
    const list = profiles.data ?? [];
    if (profile || list.length === 0 || from) {
      return;
    }
    setProfile((list.find((p) => p.code === "truck_12") ?? list[0])?.code ?? null);
  }, [profiles.data, profile, from]);

  // Переход из рейтинга устойчивости: перекрытие выбранным элементом.
  const elements = $api.useQuery(
    "get",
    "/scenarios/elements",
    { params: { query: { limit: 200 } } },
    { enabled: element !== undefined },
  );
  const [elementAdded, setElementAdded] = useState(false);
  useEffect(() => {
    const e = elements.data?.find((x) => x.id === element);
    if (elementAdded || !e) {
      return;
    }
    setElementAdded(true);
    setName(`Выпадение: ${e.name || "элемент сети"}`);
    setClosures([
      {
        key: nextKey(),
        closure: { kind: "element", element: e.id, label: e.name },
        title: e.name || "Без названия",
        note: `${e.kind === "bridge" ? "Мост" : "Участок"}, место ${e.rank} в рейтинге критичности`,
        feature: geoFeature(e.geometry, {}),
      },
    ]);
    setTargets(new Set(["mkad", "ttk", "rail_freight"]));
    const box = extentOf(coordsOf({ features: [geoFeature(e.geometry, {})] }));
    if (box) {
      setFit(box);
    }
  }, [elements.data, element, elementAdded]);

  // Копия существующего сценария: параметры и геометрия перекрытий.
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    const sc = source.data;
    if (copied || !sc || (sc.params.closures?.length && !sourceClosures.data)) {
      return;
    }
    const p = sc.params;
    const feats = (sourceClosures.data as { features?: Feature[] } | undefined)?.features ?? [];
    setName(`${sc.name} (копия)`);
    setDescription(sc.description);
    setProfile(p.profile);
    setZones(p.zones);
    if (p.depart) {
      setDepart(toLocal(p.depart));
    }
    setPasses(new Set((p.passes ?? []).map((x) => `${x.zone}:${x.kind}`)));
    const ov: Record<Zone, ZoneChoice> = { mkad: "keep", ttk: "keep", sk: "keep" };
    for (const [z, open] of Object.entries(p.zoneOverrides ?? {})) {
      ov[z as Zone] = open ? "open" : "closed";
    }
    setOverrides(ov);
    setClosures(
      (p.closures ?? []).map((c, i) => ({
        key: nextKey(),
        closure: c,
        title: c.label || closureKindTitle[c.kind],
        note: closureKindTitle[c.kind],
        feature: feats.find((f) => f.properties?.index === i) ?? null,
      })),
    );
    setFacility(
      p.newFacility
        ? { name: p.newFacility.name ?? "Новый объект", lon: p.newFacility.lon, lat: p.newFacility.lat }
        : null,
    );
    setTargets(new Set(p.targets));
    setIsoMinutes(new Set(p.isochroneMinutes ?? [15, 30]));
    setCopied(true);
    const box = extentOf(coordsOf({ features: feats }));
    if (box) {
      setFit(box);
    }
  }, [copied, source.data, sourceClosures.data]);

  const addClosure = (c: Omit<DraftClosure, "key">) => {
    if (closures.length >= maxClosures) {
      toast.error(`Перекрытий не больше ${maxClosures}`);
      return;
    }
    setClosures((list) => [...list, { ...c, key: nextKey() }]);
  };

  const onMapClick = async ([lon, lat]: [number, number]) => {
    if (tool === "facility") {
      setFacility((f) => ({ name: f?.name ?? "Новый объект", lon, lat }));
      setTool(null);
      return;
    }
    if (tool !== "segment") {
      return;
    }
    const { data, error } = await fetchClient.GET("/scenarios/segment-at", { params: { query: { lon, lat } } });
    if (error || !data) {
      toast.error(problemMessage(error));
      return;
    }
    if (closures.some((c) => c.closure.kind === "segments" && c.closure.segments?.includes(data.id))) {
      toast.success("Участок уже перекрыт");
      return;
    }
    addClosure({
      closure: { kind: "segments", segments: [data.id], label: data.name || undefined },
      title: data.name || "Участок без названия",
      note: `Участок, ${num(data.lengthM)} м`,
      feature: geoFeature(data.geometry, {}),
    });
  };

  useDrawPolygon(map, tool === "area", (ring) => {
    addClosure({
      closure: {
        kind: "area",
        polygon: ring,
        label: `Область ${closures.filter((c) => c.closure.kind === "area").length + 1}`,
      },
      title: `Область ${closures.filter((c) => c.closure.kind === "area").length + 1}`,
      note: "Перекрыты все дороги внутри",
      feature: { type: "Feature", properties: {}, geometry: { type: "Polygon", coordinates: [ring] } },
    });
    setTool(null);
  });

  const palette = usePalette();
  const overlay = useMemo(() => {
    const points: Feature[] = [];
    if (facility) {
      points.push(pointFeature(facility.lon, facility.lat, { kind: "new" }));
    }
    for (const f of isoFacilities) {
      points.push(pointFeature(f.lon, f.lat, { kind: "facility" }));
    }
    return builderOverlay(
      closures.flatMap((c) => (c.feature ? [c.feature] : [])),
      points,
      palette,
    );
  }, [closures, facility, isoFacilities, palette]);
  const style = useScenarioStyle(context, overlay);

  const zoneOverrides = Object.fromEntries(
    Object.entries(overrides)
      .filter(([, v]) => v !== "keep")
      .map(([z, v]) => [z, v === "open"]),
  );
  const changes = closures.length > 0 || facility !== null || (zones && Object.keys(zoneOverrides).length > 0);
  const canRun = !!profile && name.trim() !== "" && changes && targets.size > 0 && !create.isPending;

  const run = () => {
    if (!profile || !canRun) {
      return;
    }
    const params: Schemas["ScenarioParams"] = {
      profile,
      zones,
      targets: targetKinds.filter((t) => targets.has(t)),
      closures: closures.map((c) => c.closure),
    };
    if (zones) {
      params.depart = new Date(depart).toISOString();
      params.passes = [...passes].map((p) => {
        const [zone, kind] = p.split(":") as [Zone, Schemas["VehiclePass"]["kind"]];
        return { zone, kind };
      });
      params.zoneOverrides = zoneOverrides;
    }
    if (facility) {
      params.newFacility = facility;
    }
    if (facility || isoFacilities.length > 0) {
      params.isochroneMinutes = [...isoMinutes].sort((a, b) => a - b);
      params.isochroneFacilities = isoFacilities.map((f) => f.id);
    }
    create.mutate(
      { body: { name: name.trim(), description: description.trim() || undefined, params } },
      {
        onSuccess: (sc) => {
          void navigate({ to: "/scenarios/$scenarioId", params: { scenarioId: sc.id } });
        },
      },
    );
  };

  const hint =
    tool === "segment"
      ? "Щёлкните по дороге — участок будет перекрыт"
      : tool === "area"
        ? "Щёлкайте вершины области; замкните щелчком в первую вершину"
        : tool === "facility"
          ? "Щёлкните на карте — место нового объекта"
          : undefined;

  return (
    <div className={s.workspace}>
      <aside className={s.panel} aria-label="Конструктор сценария">
        <div className={s.titleRow}>
          <h1 className={s.title}>Новый сценарий</h1>
          <Link to="/scenarios" className={s.back}>
            <ArrowLeft size={14} aria-hidden /> Все сценарии
          </Link>
        </div>
        <TextField label="Название" value={name} onChange={setName} isRequired />
        <TextArea label="Описание" value={description} onChange={setDescription} />

        <section className={s.block}>
          <h2>Транспорт и правила</h2>
          <Select
            label="Профиль ТС"
            options={(profiles.data ?? []).map((p) => ({ id: p.code, label: p.name }))}
            value={profile}
            onChange={(v) => setProfile(v)}
          />
          <Switch isSelected={zones} onChange={setZones}>
            Учитывать правила зон пропусков
          </Switch>
          {!zones && (
            <p className={s.muted}>
              Без правил зон — физическая доступность по сети с ограничениями участков, как в показателях доступности.
            </p>
          )}
          {zones && (
            <>
              <TextField label="Время отправления" type="datetime-local" value={depart} onChange={setDepart} />
              <fieldset className={s.block} style={{ border: 0, margin: 0, padding: 0 }}>
                <legend className={s.muted}>Пропуска (в базе и сценарии)</legend>
                <div className={s.checks}>
                  {passZones.flatMap((z) =>
                    (["day", "night"] as const).map((k) => (
                      <Checkbox
                        key={`${z}:${k}`}
                        isSelected={passes.has(`${z}:${k}`)}
                        onChange={(on) =>
                          setPasses((ps) => {
                            const next = new Set(ps);
                            if (on) {
                              next.add(`${z}:${k}`);
                            } else {
                              next.delete(`${z}:${k}`);
                            }
                            return next;
                          })
                        }
                      >
                        {passZoneLabel[z]} {k === "day" ? "дневной" : "ночной"}
                      </Checkbox>
                    )),
                  )}
                </div>
              </fieldset>
              <div className={s.block}>
                <span className={s.muted}>Правила зон в сценарии</span>
                {passZones.map((z) => (
                  <div key={z} className={s.zoneRow}>
                    <span>{zoneName[z]}</span>
                    <SegmentedControl
                      label={`Зона ${zoneName[z]} в сценарии`}
                      value={overrides[z]}
                      onChange={(v) => setOverrides((o) => ({ ...o, [z]: v as ZoneChoice }))}
                      options={[
                        { id: "keep", label: "как есть" },
                        { id: "open", label: "открыть" },
                        { id: "closed", label: "закрыть" },
                      ]}
                    />
                  </div>
                ))}
                <p className={s.muted}>
                  Закрытая внешняя зона закрывает и внутренние: открывать ТТК имеет смысл, если МКАД открыт.
                </p>
              </div>
            </>
          )}
        </section>

        <section className={s.block}>
          <h2>Перекрытия</h2>
          <div className={s.row}>
            <Button
              size="sm"
              variant={tool === "segment" ? "primary" : "default"}
              onPress={() => setTool(tool === "segment" ? null : "segment")}
            >
              <RouteIcon size={14} aria-hidden /> Участок на карте
            </Button>
            <Button
              size="sm"
              variant={tool === "area" ? "primary" : "default"}
              onPress={() => setTool(tool === "area" ? null : "area")}
            >
              <PenLine size={14} aria-hidden /> Область
            </Button>
          </div>
          <ElementSearch
            onSelect={(e) =>
              addClosure({
                closure: { kind: "element", element: e.id, label: e.name },
                title: e.name || "Без названия",
                note: `${e.kind === "bridge" ? "Мост" : "Участок"}, место ${e.rank} в рейтинге критичности`,
                feature: geoFeature(e.geometry, {}),
              })
            }
          />
          {closures.length === 0 ? (
            <p className={s.muted}>Перекрытий нет.</p>
          ) : (
            <ul className={s.list}>
              {closures.map((c) => (
                <li key={c.key} className={s.item}>
                  <span>
                    <span className={s.itemTitle}>{c.title}</span>
                    <span className={s.itemNote}>{c.note}</span>
                  </span>
                  <IconButton
                    label={`Убрать перекрытие «${c.title}»`}
                    onPress={() => setClosures((list) => list.filter((x) => x.key !== c.key))}
                  >
                    <X size={15} />
                  </IconButton>
                </li>
              ))}
            </ul>
          )}
        </section>

        <section className={s.block}>
          <h2>Новый объект</h2>
          {facility ? (
            <>
              <TextField
                label="Название объекта"
                value={facility.name}
                onChange={(v) => setFacility({ ...facility, name: v })}
              />
              <div className={s.row}>
                <span className={`${s.muted} num`}>
                  {num(facility.lat, 5)}, {num(facility.lon, 5)}
                </span>
                <Button size="sm" variant="ghost" onPress={() => setTool("facility")}>
                  <MapPinned size={14} aria-hidden /> Переставить
                </Button>
                <Button size="sm" variant="ghost" onPress={() => setFacility(null)}>
                  <X size={14} aria-hidden /> Убрать
                </Button>
              </div>
            </>
          ) : (
            <Button
              size="sm"
              variant={tool === "facility" ? "primary" : "default"}
              onPress={() => setTool(tool === "facility" ? null : "facility")}
            >
              <Building2 size={14} aria-hidden /> Поставить на карте
            </Button>
          )}
        </section>

        <section className={s.block}>
          <h2>Что считать</h2>
          <div className={s.checks}>
            {targetKinds.map((t) => (
              <Checkbox
                key={t}
                isSelected={targets.has(t)}
                onChange={(on) =>
                  setTargets((ts) => {
                    const next = new Set(ts);
                    if (on) {
                      next.add(t);
                    } else {
                      next.delete(t);
                    }
                    return next;
                  })
                }
              >
                {targetTitle[t]}
              </Checkbox>
            ))}
          </div>
          <span className={s.muted}>Изохроны (от нового объекта и выбранных объектов)</span>
          <div className={s.checks}>
            {isoChoices.map((m) => (
              <Checkbox
                key={m}
                isSelected={isoMinutes.has(m)}
                onChange={(on) =>
                  setIsoMinutes((ms) => {
                    const next = new Set(ms);
                    if (on) {
                      next.add(m);
                    } else {
                      next.delete(m);
                    }
                    return next;
                  })
                }
              >
                {m} мин
              </Checkbox>
            ))}
          </div>
          {isoFacilities.length < maxIsoFacilities && (
            <PlaceSearch
              label="Объект реестра для изохрон"
              onSelect={(p) => {
                if (p.kind !== "facility") {
                  toast.success("Изохроны строятся от объектов реестра");
                  return;
                }
                setIsoFacilities((list) =>
                  list.some((f) => f.id === p.ref)
                    ? list
                    : [...list, { id: p.ref, name: p.title, lon: p.point.lon, lat: p.point.lat }],
                );
              }}
            />
          )}
          {isoFacilities.length > 0 && (
            <ul className={s.list}>
              {isoFacilities.map((f) => (
                <li key={f.id} className={s.item}>
                  <span className={s.itemTitle}>{f.name}</span>
                  <IconButton
                    label={`Убрать объект «${f.name}»`}
                    onPress={() => setIsoFacilities((list) => list.filter((x) => x.id !== f.id))}
                  >
                    <X size={15} />
                  </IconButton>
                </li>
              ))}
            </ul>
          )}
        </section>

        {create.error && (
          <div className="notice notice-error" role="alert">
            {problemMessage(create.error)}
          </div>
        )}
        {!changes && <p className={s.muted}>Задайте перекрытие, новый объект или изменение правил зон.</p>}
        <Button variant="primary" onPress={run} isDisabled={!canRun}>
          <Play size={15} aria-hidden /> {create.isPending ? "Ставим в расчёт…" : "Рассчитать"}
        </Button>
      </aside>
      <div className={s.maps}>
        <MainMap
          style={style}
          hint={hint}
          fit={fit}
          cursor={tool ? "crosshair" : undefined}
          onMap={setMap}
          onClick={(p) => void onMapClick(p)}
          legend={
            <>
              <span>
                <span className={s.lineSwatch} /> перекрытие
              </span>
              <span>
                <span className={s.swatch} style={{ background: "var(--acc)" }} /> новый объект
              </span>
              <span>
                <span className={s.swatch} style={{ background: "var(--cat2)" }} /> изохроны от объекта
              </span>
            </>
          }
        />
      </div>
    </div>
  );
}

/** Поиск моста или участка в рейтинге критичности. */
function ElementSearch({ onSelect }: { onSelect: (e: Schemas["ScenarioElement"]) => void }) {
  const [text, setText] = useState("");
  const q = useDebounced(text.trim(), 200);
  // Как в поиске мест: без текста список не запрашивается, иначе после выбора (поле очищается)
  // подсказки открылись бы снова.
  const enabled = q.length >= 2;
  const list = $api.useQuery(
    "get",
    "/scenarios/elements",
    { params: { query: { q, limit: 30 } } },
    { enabled, staleTime: 60_000 },
  );
  const items = enabled ? (list.data ?? []) : [];
  return (
    <SuggestField
      label="Мост или участок из рейтинга критичности"
      placeholder="Название дороги или моста"
      text={text}
      onTextChange={setText}
      pending={enabled && (list.isFetching || q !== text.trim())}
      suggestions={items.map((e) => ({
        id: String(e.id),
        label: e.name || "Без названия",
        description: `${e.kind === "bridge" ? "мост" : "участок"} · место ${e.rank} · ущерб ${num(e.damageMin)} объекто-мин`,
      }))}
      onSelect={(id) => {
        const e = items.find((x) => String(x.id) === id);
        if (e) {
          onSelect(e);
        }
      }}
    />
  );
}
