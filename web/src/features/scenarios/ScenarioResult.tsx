import { useQueryClient } from "@tanstack/react-query";
import { Link, useNavigate, useParams } from "@tanstack/react-router";
import type { Feature, FeatureCollection } from "geojson";
import { ArrowLeft, Copy, RefreshCw, Trash2 } from "lucide-react";
import type { Map as MapLibreMap } from "maplibre-gl";
import { useMemo, useState } from "react";
import { Guard } from "@/app/Guard";
import { $api, problemMessage, type Schemas } from "@/shared/api/client";
import { dateTime, num, plural } from "@/shared/format";
import { Badge, Button, cellClass, Modal, Skeleton, Switch, tableClass } from "@/shared/ui";
import { Tabs } from "@/shared/ui/navigation";
import { zoneName } from "../network/labels";
import {
  areaChange,
  deltaText,
  isActive,
  minutesText,
  type Scenario,
  statusTitle,
  statusTone,
  type TargetKind,
  targetTitle,
} from "./labels";
import { facilityFeatures, pointFeature, resultOverlay } from "./layers";
import { coordsOf, extentOf, MainMap, SecondMap, usePalette, useScenarioStyle } from "./ScenarioMap";
import s from "./scenario.module.css";
import { useScenarioEvents } from "./useScenarioEvents";

/** Слои каталога под результатом: кольца зон. */
const context = new Set(["rings"]);
/** Объектов в таблице изменений. */
const tableRows = 50;

export function ScenarioResultPage() {
  return (
    <Guard permission="scenarios.manage">
      <ScenarioResult />
    </Guard>
  );
}

function ScenarioResult() {
  useScenarioEvents();
  const { scenarioId } = useParams({ from: "/scenarios/$scenarioId" });
  const path = { params: { path: { scenarioId } } };
  const q = $api.useQuery("get", "/scenarios/{scenarioId}", path);
  const sc = q.data;
  const done = sc?.status === "done";
  const [target, setTarget] = useState<TargetKind | null>(null);
  const kind = target ?? sc?.params.targets[0] ?? "mkad";
  const facilities = $api.useQuery(
    "get",
    "/scenarios/{scenarioId}/facilities",
    { params: { path: { scenarioId }, query: { target: kind, limit: 5000 } } },
    { enabled: done },
  );
  const districts = $api.useQuery(
    "get",
    "/scenarios/{scenarioId}/districts",
    { params: { path: { scenarioId }, query: { target: kind } } },
    { enabled: done },
  );
  const isochrones = $api.useQuery("get", "/scenarios/{scenarioId}/isochrones", path, { enabled: done });
  const closures = $api.useQuery("get", "/scenarios/{scenarioId}/closures", path, { enabled: !!sc });
  const [compare, setCompare] = useState(true);
  const [map, setMap] = useState<MapLibreMap | null>(null);

  const palette = usePalette();
  const isoFC = isochrones.data as FeatureCollection | undefined;
  const closureFC = closures.data as FeatureCollection | undefined;
  const origins = useMemo(() => {
    const out: Feature[] = [];
    const nf = sc?.params.newFacility;
    if (nf) {
      out.push(pointFeature(nf.lon, nf.lat, { kind: "new" }));
    }
    return out;
  }, [sc?.params.newFacility]);
  const facFeatures = useMemo(() => facilityFeatures(facilities.data ?? []), [facilities.data]);
  const scenarioStyle = useScenarioStyle(
    context,
    useMemo(
      () => resultOverlay("scenario", isoFC, closureFC, facFeatures, origins, palette),
      [isoFC, closureFC, facFeatures, origins, palette],
    ),
  );
  const baseStyle = useScenarioStyle(
    context,
    useMemo(() => resultOverlay("base", isoFC, closureFC, [], origins, palette), [isoFC, closureFC, origins, palette]),
  );
  // Охват: перекрытия, изохроны, новый объект и объекты с изменением времени.
  const fit = useMemo(() => {
    const coords = [
      ...coordsOf(closureFC),
      ...coordsOf(isoFC),
      ...coordsOf({ features: origins }),
      ...coordsOf({ features: facFeatures }),
    ];
    return extentOf(coords);
  }, [closureFC, isoFC, origins, facFeatures]);

  if (q.isPending) {
    return (
      <div className={s.workspace}>
        <aside className={s.panel}>
          <Skeleton height={24} />
          <Skeleton height={120} />
        </aside>
      </div>
    );
  }
  if (q.error || !sc) {
    return (
      <div className={s.workspace}>
        <aside className={s.panel}>
          <Link to="/scenarios" className={s.back}>
            <ArrowLeft size={14} aria-hidden /> Все сценарии
          </Link>
          <div className="notice notice-error" role="alert">
            {problemMessage(q.error)}
          </div>
        </aside>
      </div>
    );
  }

  const legend = (
    <>
      <span>
        <span className={s.swatch} style={{ background: "var(--crit)" }} /> потерял доступ или +5 мин и больше
      </span>
      <span>
        <span className={s.swatch} style={{ background: "var(--warn)" }} /> +1–5 мин
      </span>
      <span>
        <span className={s.swatch} style={{ background: "var(--info)" }} /> до +1 мин
      </span>
      <span>
        <span className={s.swatch} style={{ background: "var(--ok)" }} /> быстрее
      </span>
      <span>
        <span className={s.lineSwatch} /> перекрытие
      </span>
    </>
  );

  return (
    <div className={s.workspace}>
      <aside className={s.panel} aria-label="Результаты сценария">
        <div className={s.titleRow}>
          <Link to="/scenarios" className={s.back}>
            <ArrowLeft size={14} aria-hidden /> Все сценарии
          </Link>
          <Badge tone={statusTone[sc.status]}>{statusTitle[sc.status]}</Badge>
        </div>
        <h1 className={s.title}>{sc.name}</h1>
        {sc.description && <p className={s.muted}>{sc.description}</p>}
        <ParamsBlock sc={sc} />
        {isActive(sc.status) && <ProgressBlock sc={sc} />}
        {sc.status === "failed" && (
          <div className="notice notice-error" role="alert">
            {sc.error ?? "Расчёт не удался"}
          </div>
        )}
        {done && sc.summary && (
          <>
            {sc.params.targets.length > 1 && (
              <Tabs
                label="Цель доступности"
                selected={kind}
                onChange={(id) => setTarget(id as TargetKind)}
                items={sc.params.targets.map((t) => ({ id: t, title: targetTitle[t], content: null }))}
              />
            )}
            <TargetBlock summary={sc.summary.targets.find((t) => t.kind === kind)} />
            <NewFacilityBlock sc={sc} />
            <IsoBlock sc={sc} />
            <DistrictsBlock rows={districts.data} />
            <FacilitiesBlock rows={facilities.data} />
            <p className={s.muted}>
              Посчитан {dateTime(sc.finishedAt)} за {num((sc.summary.durationMs ?? 0) / 1000)} с
              {sc.graphBuiltAt ? `, граф маршрутизации от ${dateTime(sc.graphBuiltAt)}` : ""}.
              {sc.summary.baseReused ? " База — показатели доступности на том же графе." : ""}
            </p>
          </>
        )}
        <Actions sc={sc} />
      </aside>
      <div className={s.maps} data-compare={done && compare}>
        {done && compare && <SecondMap style={baseStyle} sync={map} label="База" />}
        <MainMap
          style={done ? scenarioStyle : baseStyle}
          label={done && compare ? "Сценарий" : undefined}
          fit={fit}
          onMap={setMap}
          legend={done ? legend : undefined}
        >
          {done && (
            <div className={s.hint}>
              <Switch isSelected={compare} onChange={setCompare}>
                Сравнение на двух картах
              </Switch>
            </div>
          )}
        </MainMap>
      </div>
    </div>
  );
}

function ParamsBlock({ sc }: { sc: Scenario }) {
  const profiles = $api.useQuery("get", "/routing/profiles", {}, { staleTime: 300_000 });
  const p = sc.params;
  const profile = profiles.data?.find((x) => x.code === p.profile)?.name ?? p.profile;
  // Описание перекрытий — из итога (длины и площади), до расчёта — из параметров.
  const items = sc.summary
    ? sc.summary.closures.map((c, i) => ({
        key: `${i}:${c.kind}:${c.label}`,
        title: c.label || "Перекрытие",
        note:
          c.kind === "area"
            ? `Область ${num(c.areaKm2 ?? 0, 2)} км², дорог ${num(c.lengthKm, 1)} км`
            : `${plural(c.segments, "участок", "участка", "участков")}, ${num(c.lengthKm, 2)} км`,
      }))
    : (p.closures ?? []).map((c, i) => ({
        key: `${i}:${c.kind}:${c.label ?? ""}`,
        title: c.label || "Перекрытие",
        note: "",
      }));
  return (
    <section className={s.block}>
      <h2>Условия</h2>
      <p>
        {profile};{" "}
        {p.zones ? `с правилами зон${p.depart ? `, отправление ${dateTime(p.depart)}` : ""}` : "без правил зон"}
      </p>
      {sc.summary?.zones?.some((z) => z.baseClosed !== z.scenarioClosed) && (
        <ul className={s.list}>
          {sc.summary.zones
            .filter((z) => z.baseClosed !== z.scenarioClosed)
            .map((z) => (
              <li key={z.zone} className={s.muted}>
                {zoneName[z.zone]}: {z.baseClosed ? "закрыта" : "открыта"} → {z.scenarioClosed ? "закрыта" : "открыта"}
              </li>
            ))}
        </ul>
      )}
      {sc.summary?.zones?.every((z) => z.baseClosed === z.scenarioClosed) && p.zoneOverrides && (
        <p className={s.muted}>
          Решения по зонам в сценарии не изменились: закрытая внешняя зона закрывает и внутренние.
        </p>
      )}
      {items.length > 0 && (
        <ul className={s.list}>
          {items.map((c) => (
            <li key={c.key} className={s.item}>
              <span>
                <span className={s.itemTitle}>{c.title}</span>
                {c.note && <span className={s.itemNote}>{c.note}</span>}
              </span>
            </li>
          ))}
        </ul>
      )}
      {p.newFacility && <p>Новый объект: {p.newFacility.name}</p>}
    </section>
  );
}

function ProgressBlock({ sc }: { sc: Scenario }) {
  const { done, total, stage } = sc.progress;
  const pct = total > 0 ? Math.min(100, Math.round((done / total) * 100)) : 0;
  return (
    <section className={s.block} aria-live="polite">
      <h2>Расчёт</h2>
      <div
        className={s.progress}
        role="progressbar"
        aria-label="Ход расчёта"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={pct}
      >
        <span style={{ width: `${pct}%` }} />
      </div>
      <p className={s.muted}>
        {sc.status === "queued" ? "В очереди" : (stage ?? "Считается")}
        {total > 0 ? ` — ${pct} %` : ""}
      </p>
    </section>
  );
}

function Kpi({ label, value }: { label: string; value: string }) {
  return (
    <div className={s.kpi}>
      <span className={s.kpiLabel}>{label}</span>
      <span className={s.kpiValue}>{value}</span>
    </div>
  );
}

function TargetBlock({ summary }: { summary: Schemas["ScenarioTargetSummary"] | undefined }) {
  if (!summary) {
    return null;
  }
  return (
    <section className={s.block}>
      <h2>{targetTitle[summary.kind]}</h2>
      <div className={s.kpis}>
        <Kpi label="Объектов с приростом времени" value={num(summary.affected)} />
        <Kpi label="Суммарный прирост, объекто-мин" value={num(summary.delayMin)} />
        <Kpi label="Средний прирост, мин" value={num(summary.meanDelay, 1)} />
        <Kpi label="Наибольший прирост, мин" value={num(summary.maxDelay, 1)} />
        <Kpi label="Потеряли доступ" value={num(summary.lost)} />
        <Kpi label="Быстрее или получили доступ" value={num(summary.improved + summary.gained)} />
      </div>
      <p className={s.muted}>
        Медиана времени: {num(summary.baseMedian, 1)} → {num(summary.scenarioMedian, 1)} мин
        {summary.blocked > 0 ? `; в закрытой зоне в сценарии — ${num(summary.blocked)} объектов` : ""}.
      </p>
    </section>
  );
}

function NewFacilityBlock({ sc }: { sc: Scenario }) {
  const rows = sc.summary?.newFacility;
  if (!rows?.length) {
    return null;
  }
  return (
    <section className={s.block}>
      <h2>{sc.params.newFacility?.name ?? "Новый объект"}</h2>
      <table className={tableClass}>
        <thead>
          <tr>
            <th scope="col">Цель</th>
            <th scope="col" className={cellClass.right}>
              База
            </th>
            <th scope="col" className={cellClass.right}>
              Сценарий
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.kind}>
              <td>{targetTitle[r.kind]}</td>
              <td className={`${cellClass.right} num`}>{minutesText(r.baseMin, r.baseStatus)}</td>
              <td className={`${cellClass.right} num`}>{minutesText(r.scenarioMin, r.scenarioStatus)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}

function IsoBlock({ sc }: { sc: Scenario }) {
  const rows = sc.summary?.isochrones;
  if (!rows?.length) {
    return null;
  }
  return (
    <section className={s.block}>
      <h2>Изохроны</h2>
      <table className={tableClass}>
        <thead>
          <tr>
            <th scope="col">Объект</th>
            <th scope="col" className={cellClass.right}>
              Мин
            </th>
            <th scope="col" className={cellClass.right}>
              База, км²
            </th>
            <th scope="col" className={cellClass.right}>
              Сценарий, км²
            </th>
            <th scope="col" className={cellClass.right}>
              Изменение
            </th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={`${r.origin}-${r.minutes}`}>
              <td>{r.name}</td>
              <td className={`${cellClass.right} num`}>{r.minutes}</td>
              <td className={`${cellClass.right} num`}>{num(r.baseKm2, 1)}</td>
              <td className={`${cellClass.right} num`}>{num(r.scenarioKm2, 1)}</td>
              <td className={`${cellClass.right} num`}>{areaChange(r.baseKm2, r.scenarioKm2)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}

/** Районов в таблице до «Показать все». */
const districtRows = 15;

function DistrictsBlock({ rows }: { rows: Schemas["ScenarioDistrict"][] | undefined }) {
  const [all, setAll] = useState(false);
  if (!rows?.length) {
    return null;
  }
  return (
    <section className={s.block}>
      <h2>По районам ({rows.length})</h2>
      <div>
        <table className={tableClass}>
          <thead>
            <tr>
              <th scope="col">Район</th>
              <th scope="col" className={cellClass.right}>
                Затронуто
              </th>
              <th scope="col" className={cellClass.right}>
                Прирост, объекто-мин
              </th>
              <th scope="col" className={cellClass.right}>
                Без доступа
              </th>
            </tr>
          </thead>
          <tbody>
            {(all ? rows : rows.slice(0, districtRows)).map((r) => (
              <tr key={r.district}>
                <td>{r.district}</td>
                <td className={`${cellClass.right} num`}>
                  {r.affected} из {r.facilities}
                </td>
                <td className={`${cellClass.right} num`}>{num(r.delayMin, 1)}</td>
                <td className={`${cellClass.right} num`}>{r.lost}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {rows.length > districtRows && (
        <Button size="sm" variant="ghost" onPress={() => setAll(!all)}>
          {all ? `Первые ${districtRows}` : `Показать все ${rows.length}`}
        </Button>
      )}
    </section>
  );
}

function FacilitiesBlock({ rows }: { rows: Schemas["ScenarioFacility"][] | undefined }) {
  const list = (rows ?? []).filter((r) => r.entity !== "new");
  if (list.length === 0) {
    return (
      <section className={s.block}>
        <h2>Объекты</h2>
        <p className={s.muted}>Время объектов реестра до цели не изменилось.</p>
      </section>
    );
  }
  return (
    <section className={s.block}>
      <h2>Объекты с изменениями ({list.length})</h2>
      <div>
        <table className={tableClass}>
          <thead>
            <tr>
              <th scope="col">Объект</th>
              <th scope="col" className={cellClass.right}>
                База
              </th>
              <th scope="col" className={cellClass.right}>
                Изменение
              </th>
            </tr>
          </thead>
          <tbody>
            {list.slice(0, tableRows).map((r) => (
              <tr key={r.entity}>
                <td>
                  <Link to="/facilities/$facilityId" params={{ facilityId: r.entity }}>
                    {r.name || "Без названия"}
                  </Link>
                  {r.district && <span className={s.itemNote}>{r.district}</span>}
                </td>
                <td className={`${cellClass.right} num`}>{minutesText(r.baseMin, r.baseStatus)}</td>
                <td className={`${cellClass.right} num`}>{deltaText(r)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {list.length > tableRows && <p className={s.muted}>Показаны первые {tableRows}; все — на карте.</p>}
    </section>
  );
}

function Actions({ sc }: { sc: Scenario }) {
  const navigate = useNavigate();
  const qc = useQueryClient();
  const refresh = () =>
    qc.invalidateQueries({
      predicate: (q) => typeof q.queryKey[1] === "string" && q.queryKey[1].startsWith("/scenarios"),
    });
  const rerun = $api.useMutation("post", "/scenarios/{scenarioId}/rerun");
  const del = $api.useMutation("delete", "/scenarios/{scenarioId}");
  const [confirm, setConfirm] = useState(false);
  const path = { params: { path: { scenarioId: sc.id } } };
  const busy = isActive(sc.status);
  return (
    <section className={s.block}>
      <div className={s.actions}>
        <Button
          size="sm"
          onPress={() => rerun.mutate(path, { onSuccess: () => void refresh() })}
          isDisabled={busy || rerun.isPending}
        >
          <RefreshCw size={14} aria-hidden /> Пересчитать
        </Button>
        <Button size="sm" onPress={() => void navigate({ to: "/scenarios/new", search: { from: sc.id } })}>
          <Copy size={14} aria-hidden /> Изменить копию
        </Button>
        <Button size="sm" variant="danger" onPress={() => setConfirm(true)} isDisabled={busy}>
          <Trash2 size={14} aria-hidden /> Удалить
        </Button>
      </div>
      {(rerun.error || del.error) && (
        <div className="notice notice-error" role="alert">
          {problemMessage(rerun.error ?? del.error)}
        </div>
      )}
      <Modal
        isOpen={confirm}
        onOpenChange={setConfirm}
        title="Удалить сценарий?"
        description={`«${sc.name}» и его результаты будут удалены.`}
        actions={
          <>
            <Button onPress={() => setConfirm(false)}>Отмена</Button>
            <Button
              variant="danger"
              onPress={() =>
                del.mutate(path, {
                  onSuccess: async () => {
                    await refresh();
                    await navigate({ to: "/scenarios" });
                  },
                  onSettled: () => setConfirm(false),
                })
              }
            >
              Удалить
            </Button>
          </>
        }
      />
    </section>
  );
}
