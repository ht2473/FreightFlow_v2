import type { Feature, FeatureCollection } from "geojson";
import { useMemo } from "react";
import { $api, type Schemas } from "@/shared/api/client";
import { date, num } from "@/shared/format";
import { Chart } from "@/shared/ui/Chart";
import { metricValue, useMetrics } from "../analytics/metrics";
import { poiLabel, workKindLabel } from "../facilities/labels";
import { restrictionKind } from "../regulation/labels";
import { targetTitle } from "../scenarios/labels";
import {
  boundsOf,
  Facts,
  freightflowSource,
  mosSource,
  osmSource,
  PrintDoc,
  PrintMap,
  PrintSection,
  type Sheet,
  SheetTable,
  type Source,
} from "./print";

type P = Record<string, string | undefined>;

const fc = (features: Feature[]): FeatureCollection => ({ type: "FeatureCollection", features });
const feature = (geometry: unknown, tone: string, props: Record<string, unknown> = {}): Feature => ({
  type: "Feature",
  properties: { tone, ...props },
  geometry: geometry as Feature["geometry"],
});
const point = (lon: number, lat: number, tone: string) => feature({ type: "Point", coordinates: [lon, lat] }, tone);
const area = (m2?: number) => (m2 === undefined ? null : Math.round(m2));

function metricSheet(
  values: Schemas["MetricValue"][] | undefined,
  defs: ReturnType<typeof useMetrics>["byCode"],
): Sheet {
  return {
    name: "Показатели",
    columns: ["Показатель", "Значение", "Единица", "Место", "Из"],
    rows: (values ?? []).map((m) => {
      const d = defs.get(m.code);
      return [
        d?.title ?? m.code,
        Number(metricValue(m.value, d, false).replace(/\s/g, "").replace(",", ".")),
        d?.unit ?? "",
        m.rank,
        m.of,
      ];
    }),
  };
}

/** Паспорт объекта: характеристики, доступность, ограничения и работы рядом, показатели, карта. */
export function FacilityReport({ params }: { params: P }) {
  const id = params.facilityId ?? "";
  const card = $api.useQuery("get", "/facilities/{facilityId}", { params: { path: { facilityId: id } } });
  const metrics = $api.useQuery("get", "/metrics/entity", { params: { query: { level: "facility", entity: id } } });
  const defs = useMetrics();
  const c = card.data;
  const f = c?.facility;
  const sheets = useMemo<Sheet[]>(() => {
    if (!c || !f) {
      return [];
    }
    return [
      {
        name: "Характеристики",
        columns: ["Поле", "Значение"],
        rows: [
          ["Название", f.name ?? ""],
          ["Тип", f.typeName],
          ["Адрес", f.address ?? ""],
          ["Округ", f.okrug ?? ""],
          ["Район", f.district ?? ""],
          ["Площадь застройки, м²", area(f.buildingAreaM2)],
          ["Общая площадь (оценка), м²", area(f.floorAreaM2)],
          ["Площадь территории, м²", area(f.territoryAreaM2)],
          ["Зданий", f.buildings],
          ["Достоверность записи", Math.round(f.confidence * 100) / 100],
          ["До МКАД (грузовик 20 т), мин", f.mkadMin ?? null],
        ],
      },
      {
        name: "Доступность",
        columns: ["Профиль ТС", "Цель", "Время, мин", "Расстояние, км"],
        rows: c.access.map((a) => [a.profileName, a.targetName, a.minutes ?? null, a.km ?? null]),
      },
      {
        name: "Ограничения рядом",
        columns: ["Вид", "Значение", "Условие", "Улица", "Расстояние, м"],
        rows: c.restrictions.map((r) => [
          restrictionKind[r.kind] ?? r.kind,
          r.value ?? null,
          r.condition ?? "",
          r.street ?? "",
          Math.round(r.distanceM),
        ]),
      },
      {
        name: "Работы рядом",
        columns: ["Работа", "Вид", "Состояние", "Перекрытие", "Начало", "Окончание", "Расстояние, м"],
        rows: c.works.map((w) => [
          w.title,
          workKindLabel[w.kind],
          w.state === "active" ? "идёт" : "запланирована",
          w.closure ? "да" : "нет",
          w.starts ? date(w.starts) : "",
          w.ends ? date(w.ends) : "",
          Math.round(w.distanceM),
        ]),
      },
      {
        name: "Инфраструктура рядом",
        columns: ["Вид", "Название", "Расстояние, м"],
        rows: c.nearby.map((n) => [poiLabel[n.kind] ?? n.kind, n.name ?? "", Math.round(n.distanceM)]),
      },
      metricSheet(metrics.data, defs.byCode),
    ];
  }, [c, f, metrics.data, defs.byCode]);
  const mapData = useMemo(() => {
    if (!c || !f) {
      return fc([]);
    }
    const out: Feature[] = [];
    if (c.territory) {
      out.push(feature(c.territory, "acc"));
    }
    if (c.footprint) {
      out.push(feature(c.footprint, "acc"));
    }
    for (const n of c.nearby) {
      out.push(point(n.point.lon, n.point.lat, "cat2"));
    }
    out.push(point(f.point.lon, f.point.lat, "crit"));
    return fc(out);
  }, [c, f]);
  const bounds = useMemo(() => {
    const b = boundsOf(mapData);
    return b && f
      ? ([
          Math.min(b[0], f.point.lon - 0.01),
          Math.min(b[1], f.point.lat - 0.006),
          Math.max(b[2], f.point.lon + 0.01),
          Math.max(b[3], f.point.lat + 0.006),
        ] as [number, number, number, number])
      : b;
  }, [mapData, f]);
  const sources: Source[] = [osmSource, mosSource, freightflowSource];
  return (
    <PrintDoc
      title={`Паспорт объекта: ${f?.name || f?.typeName || "объект"}`}
      subtitle={[f?.typeName, f?.address].filter(Boolean).join(" · ")}
      loading={card.isPending || metrics.isPending || defs.isPending}
      error={card.error ?? metrics.error}
      sheets={sheets}
      sources={sources}
    >
      {f && c && (
        <>
          <Facts
            items={[
              ["Тип", f.typeName],
              ["Округ, район", [f.okrug, f.district].filter(Boolean).join(", ") || "—"],
              ["Общая площадь", f.floorAreaM2 ? `${num(f.floorAreaM2)} м²` : "—"],
              ["Площадь застройки", f.buildingAreaM2 ? `${num(f.buildingAreaM2)} м²` : "—"],
              ["До МКАД", f.mkadMin !== undefined ? `${num(f.mkadMin, 1)} мин` : "—"],
              ["Достоверность", num(f.confidence, 2)],
            ]}
          />
          <PrintSection title="Расположение">
            <PrintMap data={mapData} bounds={bounds} label="Объект, территория и инфраструктура рядом" height={300} />
          </PrintSection>
          {sheets.slice(1).map((sh) => (
            <PrintSection key={sh.name} title={sh.name}>
              <SheetTable sheet={sh} limit={25} />
            </PrintSection>
          ))}
        </>
      )}
    </PrintDoc>
  );
}

/** Сводка по округу или району: показатели с местом, районы, крупные объекты, граница. */
export function TerritoryReport({ params }: { params: P }) {
  const id = params.territoryId ?? "";
  const prof = $api.useQuery("get", "/territories/{territoryId}", { params: { path: { territoryId: id } } });
  const t = prof.data;
  const level = t?.kind ?? "district";
  const metrics = $api.useQuery(
    "get",
    "/metrics/entity",
    { params: { query: { level, entity: id } } },
    { enabled: !!t },
  );
  const facilities = $api.useQuery(
    "get",
    "/facilities",
    { params: { query: { ...(level === "okrug" ? { okrug: [id] } : { district: [id] }), sort: "-area", limit: 50 } } },
    { enabled: !!t },
  );
  const defs = useMetrics();
  const sheets = useMemo<Sheet[]>(() => {
    if (!t) {
      return [];
    }
    const out: Sheet[] = [
      {
        name: "Территория",
        columns: ["Поле", "Значение"],
        rows: [
          ["Название", t.name],
          ["Площадь, км²", Math.round(t.areaKm2 * 10) / 10],
          ["Население", t.population ?? null],
          ["Плотность, чел./км²", t.density !== undefined ? Math.round(t.density) : null],
          ["Объектов логистики", t.facilities],
          ["Точек инфраструктуры", t.poi],
          ["Идущих работ", t.worksActive],
        ],
      },
      metricSheet(metrics.data, defs.byCode),
      {
        name: "Крупные объекты",
        columns: ["Объект", "Тип", "Общая площадь, м²", "До МКАД, мин"],
        rows: (facilities.data?.items ?? []).map((f) => [
          f.name || f.typeName,
          f.typeName,
          area(f.floorAreaM2),
          f.mkadMin ?? null,
        ]),
      },
    ];
    if (t.children.length > 0) {
      out.push({
        name: "Районы",
        columns: ["Район", "Площадь, км²", "Население", "Объектов", "Работ"],
        rows: t.children.map((c) => [
          c.name,
          Math.round(c.areaKm2 * 10) / 10,
          c.population ?? null,
          c.facilities,
          c.worksActive,
        ]),
      });
    }
    return out;
  }, [t, metrics.data, facilities.data, defs.byCode]);
  const mapData = useMemo(
    () =>
      fc([
        ...(t ? [feature(t.geometry, "acc")] : []),
        ...(facilities.data?.items ?? []).map((f) => point(f.point.lon, f.point.lat, "crit")),
      ]),
    [t, facilities.data],
  );
  return (
    <PrintDoc
      title={`Сводка: ${t?.name ?? "территория"}`}
      subtitle={t?.kind === "okrug" ? "Административный округ Москвы" : t?.parentName}
      loading={prof.isPending || (!!t && (metrics.isPending || facilities.isPending)) || defs.isPending}
      error={prof.error ?? metrics.error ?? facilities.error}
      sheets={sheets}
      sources={[
        osmSource,
        mosSource,
        { title: "Росстат", attribution: "Росстат, база показателей муниципальных образований", license: "" },
        freightflowSource,
      ]}
    >
      {t && (
        <>
          <Facts
            items={[
              ["Площадь", `${num(t.areaKm2, 1)} км²`],
              ["Население", t.population ? num(t.population) : "—"],
              ["Плотность", t.density ? `${num(t.density)} чел./км²` : "—"],
              ["Объектов логистики", num(t.facilities)],
              ["Инфраструктура", num(t.poi)],
              ["Идущих работ", num(t.worksActive)],
            ]}
          />
          <PrintSection title="Граница и крупные объекты">
            <PrintMap
              data={mapData}
              bounds={t.bbox as [number, number, number, number]}
              label={`Граница: ${t.name}`}
              height={320}
            />
          </PrintSection>
          {sheets.slice(1).map((sh) => (
            <PrintSection key={sh.name} title={sh.name}>
              <SheetTable sheet={sh} limit={30} />
            </PrintSection>
          ))}
        </>
      )}
    </PrintDoc>
  );
}

interface PassabilityData {
  territory?: { name: string; bbox: [number, number, number, number] };
  profileName: string;
  networkKm: number;
  blockedKm: number;
  kinds: { kind: string; count: number; lengthKm: number; blocking: number; blockedKm: number }[];
  blockers: { name: string; kind: string; value?: number; unit?: string; lengthM: number; geometry: unknown }[];
}

/** Проходимость и ограничения для профиля ТС на территории. */
export function PassabilityReport({ params }: { params: P }) {
  const q = $api.useQuery("get", "/report-data/passability", {
    params: { query: { territoryId: params.territoryId, profile: params.profile ?? "" } },
  });
  const d = q.data as PassabilityData | undefined;
  const sheets = useMemo<Sheet[]>(
    () =>
      d
        ? [
            {
              name: "По видам",
              columns: ["Вид ограничения", "Участков", "Протяжённость, км", "Не проходит профиль", "Из них, км"],
              rows: d.kinds.map((k) => [
                restrictionKind[k.kind] ?? k.kind,
                k.count,
                Math.round(k.lengthKm * 10) / 10,
                k.blocking,
                Math.round(k.blockedKm * 10) / 10,
              ]),
            },
            {
              name: "Непроходимые участки",
              columns: ["Улица", "Ограничение", "Значение", "Длина, м"],
              rows: d.blockers.map((b) => [
                b.name,
                restrictionKind[b.kind] ?? b.kind,
                b.value !== undefined && b.value !== null ? `${b.value}${b.unit ? ` ${b.unit}` : ""}` : "",
                Math.round(b.lengthM),
              ]),
            },
          ]
        : [],
    [d],
  );
  const mapData = useMemo(() => fc((d?.blockers ?? []).map((b) => feature(b.geometry, "crit"))), [d]);
  const where = d?.territory?.name ?? "Москва";
  return (
    <PrintDoc
      title={`Проходимость: ${where}`}
      subtitle={d ? `Профиль ТС: ${d.profileName}` : undefined}
      loading={q.isPending}
      error={q.error}
      sheets={sheets}
      sources={[osmSource, mosSource]}
    >
      {d && (
        <>
          <Facts
            items={[
              ["Дорожная сеть", `${num(d.networkKm)} км`],
              ["Недоступно профилю", `${num(d.blockedKm, 1)} км`],
              ["Доля недоступной сети", d.networkKm > 0 ? `${num((d.blockedKm / d.networkKm) * 100, 1)} %` : "—"],
            ]}
          />
          <PrintSection title="Участки, которые профиль не проходит">
            <PrintMap
              data={mapData}
              bounds={d.territory?.bbox ?? boundsOf(mapData)}
              label={`Непроходимые участки: ${where}`}
              height={320}
            />
          </PrintSection>
          {sheets.map((sh) => (
            <PrintSection key={sh.name} title={sh.name}>
              <SheetTable sheet={sh} limit={35} empty="Ограничений, которые не проходит профиль, нет." />
            </PrintSection>
          ))}
        </>
      )}
    </PrintDoc>
  );
}

interface RoadworksData {
  territory?: { name: string; bbox: [number, number, number, number] };
  days: number;
  active: number;
  planned: number;
  closures: number;
  byKind: Record<string, number>;
  works: {
    title: string;
    kind: "repair" | "emergency" | "closure";
    place: string;
    district: string;
    state: string;
    closure: boolean;
    start?: string;
    end?: string;
    facilities: number;
    impactKm: number;
    geometry?: unknown;
  }[];
  series: { day: string; value: number }[];
}

/** Дорожные работы и влияние: идущие и ближайшие, перекрытия, объекты рядом, динамика. */
export function RoadworksReport({ params }: { params: P }) {
  const days = Number(params.days ?? 14) || 14;
  const q = $api.useQuery("get", "/report-data/roadworks", {
    params: { query: { territoryId: params.territoryId, days } },
  });
  const d = q.data as RoadworksData | undefined;
  const sheets = useMemo<Sheet[]>(
    () =>
      d
        ? [
            {
              name: "Работы",
              columns: [
                "Работа",
                "Вид",
                "Место",
                "Район",
                "Состояние",
                "Перекрытие",
                "Начало",
                "Окончание",
                "Объектов рядом",
                "Затронуто сети, км",
              ],
              rows: d.works.map((w) => [
                w.title,
                workKindLabel[w.kind] ?? w.kind,
                w.place,
                w.district,
                w.state === "active" ? "идёт" : "начнётся",
                w.closure ? "да" : "нет",
                w.start ? date(w.start) : "",
                w.end ? date(w.end) : "",
                w.facilities,
                Math.round(w.impactKm * 100) / 100,
              ]),
            },
          ]
        : [],
    [d],
  );
  const mapData = useMemo(
    () =>
      fc(
        (d?.works ?? [])
          .filter((w) => w.geometry)
          .map((w) => feature(w.geometry, w.closure || w.kind === "emergency" ? "crit" : "warn")),
      ),
    [d],
  );
  const where = d?.territory?.name ?? "Москва";
  return (
    <PrintDoc
      title={`Дорожные работы: ${where}`}
      subtitle={`Идущие и начинающиеся в ближайшие ${days} дн.`}
      loading={q.isPending}
      error={q.error}
      sheets={sheets}
      sources={[mosSource, osmSource]}
    >
      {d && (
        <>
          <Facts
            items={[
              ["Идут", num(d.active)],
              ["Начнутся", num(d.planned)],
              ["С перекрытием", num(d.closures)],
              ["Аварийных", num(d.byKind.emergency ?? 0)],
              ["Плановых", num(d.byKind.repair ?? 0)],
              ["Перекрытий", num(d.byKind.closure ?? 0)],
            ]}
          />
          <PrintSection title="Карта работ">
            <PrintMap
              data={mapData}
              bounds={d.territory?.bbox ?? boundsOf(mapData)}
              label={`Дорожные работы: ${where}`}
              height={320}
            />
          </PrintSection>
          {d.series.length > 1 && (
            <PrintSection title="Идущие работы по дням (Москва)">
              <Chart
                label="Идущие дорожные работы по дням"
                labels={d.series.map((p) => date(p.day))}
                series={[{ name: "Идущие работы", kind: "line", color: "acc", data: d.series.map((p) => p.value) }]}
                height={200}
                compact
              />
            </PrintSection>
          )}
          <PrintSection title="Работы">
            <SheetTable
              sheet={sheets[0] ?? { name: "", columns: [], rows: [] }}
              limit={40}
              empty="Работ нет."
              show={[0, 1, 2, 4, 5, 7, 8, 9]}
            />
          </PrintSection>
        </>
      )}
    </PrintDoc>
  );
}

/** Результаты сценария «что если»: итог по целям, районы, объекты с изменением доступности. */
export function ScenarioReport({ params }: { params: P }) {
  const id = params.scenarioId ?? "";
  const sc = $api.useQuery("get", "/scenarios/{scenarioId}", { params: { path: { scenarioId: id } } });
  const sum = sc.data?.summary;
  const target = sum?.targets[0]?.kind ?? "mkad";
  const districts = $api.useQuery(
    "get",
    "/scenarios/{scenarioId}/districts",
    { params: { path: { scenarioId: id }, query: { target } } },
    { enabled: !!sum },
  );
  const facilities = $api.useQuery(
    "get",
    "/scenarios/{scenarioId}/facilities",
    { params: { path: { scenarioId: id }, query: { target, limit: 200 } } },
    { enabled: !!sum },
  );
  const sheets = useMemo<Sheet[]>(() => {
    if (!sum) {
      return [];
    }
    return [
      {
        name: "Итог по целям",
        columns: [
          "Цель",
          "Затронуто объектов",
          "Потеряли доступ",
          "Улучшилось",
          "Суммарный прирост, объекто-мин",
          "Средний прирост, мин",
          "Медиана база, мин",
          "Медиана сценарий, мин",
        ],
        rows: sum.targets.map((t) => [
          targetTitle[t.kind],
          t.affected,
          t.lost,
          t.improved,
          Math.round(t.delayMin),
          Math.round(t.meanDelay * 10) / 10,
          Math.round(t.baseMedian * 10) / 10,
          Math.round(t.scenarioMedian * 10) / 10,
        ]),
      },
      {
        name: "Перекрытия",
        columns: ["Перекрытие", "Участков", "Протяжённость, км"],
        rows: sum.closures.map((c) => [c.label, c.segments, Math.round(c.lengthKm * 10) / 10]),
      },
      {
        name: "Районы",
        columns: [
          "Район",
          "Объектов",
          "Затронуто",
          "Потеряли доступ",
          "Прирост, объекто-мин",
          "Наибольший прирост, мин",
        ],
        rows: (districts.data ?? []).map((r) => [
          r.district,
          r.facilities,
          r.affected,
          r.lost,
          Math.round(r.delayMin),
          Math.round(r.maxDelay * 10) / 10,
        ]),
      },
      {
        name: "Объекты",
        columns: ["Объект", "Район", "База, мин", "Сценарий, мин", "Изменение, мин"],
        rows: (facilities.data ?? []).map((f) => [
          f.name,
          f.district,
          f.baseMin ?? null,
          f.scenarioMin ?? null,
          f.baseMin !== undefined && f.scenarioMin !== undefined
            ? Math.round((f.scenarioMin - f.baseMin) * 10) / 10
            : null,
        ]),
      },
    ];
  }, [sum, districts.data, facilities.data]);
  const mapData = useMemo(
    () =>
      fc(
        (facilities.data ?? [])
          .filter((f) => f.baseMin !== f.scenarioMin)
          .map((f) => point(f.point.lon, f.point.lat, f.scenarioStatus !== "ok" ? "crit" : "warn")),
      ),
    [facilities.data],
  );
  return (
    <PrintDoc
      title={`Сценарий: ${sc.data?.name ?? ""}`}
      subtitle={sc.data?.description || undefined}
      loading={sc.isPending || (!!sum && (districts.isPending || facilities.isPending))}
      error={
        sc.error ??
        districts.error ??
        facilities.error ??
        (sc.data && !sum ? new Error("Сценарий ещё не рассчитан") : undefined)
      }
      sheets={sheets}
      sources={[osmSource, mosSource, freightflowSource]}
    >
      {sum && (
        <>
          <Facts
            items={[
              ["Объектов в расчёте", num(sum.facilities)],
              ["Затронуто (до МКАД и др.)", num(sum.targets.reduce((a, t) => Math.max(a, t.affected), 0))],
              ["Потеряли доступ", num(sum.targets.reduce((a, t) => Math.max(a, t.lost), 0))],
            ]}
          />
          <PrintSection title={`Объекты с изменением времени до цели «${targetTitle[target]}»`}>
            <PrintMap
              data={mapData}
              bounds={boundsOf(mapData, 0.02)}
              label="Объекты с изменением доступности"
              height={300}
            />
          </PrintSection>
          {sheets.map((sh) => (
            <PrintSection key={sh.name} title={sh.name}>
              <SheetTable sheet={sh} limit={30} />
            </PrintSection>
          ))}
        </>
      )}
    </PrintDoc>
  );
}

/** Оценка локации: веса критериев, лучшие кандидаты TOPSIS, карта. */
export function SiteReport({ params }: { params: P }) {
  const id = params.siteRunId ?? "";
  const run = $api.useQuery("get", "/site-selection/runs/{runId}", { params: { path: { runId: id } } });
  const catalog = $api.useQuery("get", "/site-selection/catalog");
  const r = run.data;
  const titles = useMemo(() => new Map((catalog.data?.criteria ?? []).map((c) => [c.metric, c])), [catalog.data]);
  const sheets = useMemo<Sheet[]>(() => {
    if (!r) {
      return [];
    }
    const crit = r.params.criteria;
    return [
      {
        name: "Критерии",
        columns: ["Критерий", "Направление", "Вес"],
        rows: crit.map((c, i) => [
          titles.get(c.metric)?.title ?? c.metric,
          c.benefit ? "больше — лучше" : "меньше — лучше",
          Math.round((r.weights[i] ?? c.weight) * 1000) / 1000,
        ]),
      },
      {
        name: "Кандидаты",
        columns: [
          "Место",
          "Оценка TOPSIS",
          "Район",
          "Долгота",
          "Широта",
          ...crit.map((c) => titles.get(c.metric)?.title ?? c.metric),
        ],
        rows: r.top.map((c) => [
          c.rank,
          Math.round(c.score * 1000) / 1000,
          c.district ?? "",
          c.lon,
          c.lat,
          ...c.values.map((v) => v ?? null),
        ]),
      },
    ];
  }, [r, titles]);
  const mapData = useMemo(() => fc((r?.top ?? []).map((c) => point(c.lon, c.lat, c.rank <= 3 ? "crit" : "acc"))), [r]);
  return (
    <PrintDoc
      title={`Оценка локации: ${r?.name ?? ""}`}
      subtitle={
        r
          ? `Ячейки H3, разрешение ${r.params.resolution ?? 8}; веса — ${r.params.weighting === "ahp" ? "МАИ" : "заданы напрямую"}`
          : undefined
      }
      loading={run.isPending || catalog.isPending}
      error={run.error ?? catalog.error}
      sheets={sheets}
      sources={[osmSource, mosSource, freightflowSource]}
    >
      {r && (
        <>
          <PrintSection title="Лучшие кандидаты">
            <PrintMap data={mapData} bounds={boundsOf(mapData, 0.03)} label="Кандидаты оценки локации" height={300} />
          </PrintSection>
          {sheets.map((sh) => (
            <PrintSection key={sh.name} title={sh.name}>
              <SheetTable sheet={sh} limit={30} />
            </PrintSection>
          ))}
          {r.ahp && (
            <p className="muted">
              Согласованность матрицы МАИ: CR = {num(r.ahp.cr, 3)}
              {r.ahp.cr <= 0.1 ? " (приемлемо)" : " (выше 0,1 — пересмотрите сравнения)"}.
            </p>
          )}
        </>
      )}
    </PrintDoc>
  );
}

interface QualityData {
  sources: {
    code: string;
    title: string;
    license: string;
    intervalS?: number;
    lastStatus?: string;
    lastRunAt?: string;
    lastSuccess?: string;
    stale: boolean;
    failedRuns30d: number;
    checksFailed: number;
  }[];
  facilities: number;
  withAddress: number;
  withFloorArea: number;
  avgConfidence: number;
  confidenceBands: Record<string, number>;
}

const runStatus: Record<string, string> = {
  succeeded: "успешно",
  unchanged: "без изменений",
  failed: "ошибка",
  running: "идёт",
  queued: "в очереди",
};

/** Качество данных: свежесть источников, неудачные загрузки, полнота реестра. */
export function QualityReport() {
  const q = $api.useQuery("get", "/report-data/quality", {});
  const d = q.data as QualityData | undefined;
  const sheets = useMemo<Sheet[]>(
    () =>
      d
        ? [
            {
              name: "Источники",
              columns: [
                "Источник",
                "Лицензия",
                "Интервал, дн.",
                "Последний запуск",
                "Результат",
                "Последняя успешная",
                "Устарел",
                "Ошибок за 30 дн.",
                "Не пройдено проверок",
              ],
              rows: d.sources.map((s) => [
                s.title,
                s.license,
                s.intervalS ? Math.round((s.intervalS / 86400) * 10) / 10 : null,
                s.lastRunAt ? date(s.lastRunAt) : "",
                s.lastStatus ? (runStatus[s.lastStatus] ?? s.lastStatus) : "",
                s.lastSuccess ? date(s.lastSuccess) : "",
                s.stale ? "да" : "нет",
                s.failedRuns30d,
                s.checksFailed,
              ]),
            },
          ]
        : [],
    [d],
  );
  const stale = d?.sources.filter((s) => s.stale).length ?? 0;
  const pct = (n: number) => (d && d.facilities > 0 ? `${num((n / d.facilities) * 100, 1)} %` : "—");
  return (
    <PrintDoc
      title="Качество данных"
      subtitle="Свежесть источников, загрузки и полнота реестра объектов"
      loading={q.isPending}
      error={q.error}
      sheets={sheets}
      sources={(d?.sources ?? []).map((s) => ({ title: s.title, attribution: s.title, license: s.license }))}
    >
      {d && (
        <>
          <Facts
            items={[
              ["Источников", num(d.sources.length)],
              ["Устарели", num(stale)],
              ["Объектов в реестре", num(d.facilities)],
              ["С адресом", pct(d.withAddress)],
              ["С общей площадью", pct(d.withFloorArea)],
              ["Средняя достоверность", num(d.avgConfidence, 2)],
            ]}
          />
          <PrintSection title="Достоверность записей реестра">
            <Facts
              items={[
                ["Высокая (≥ 0,7)", num(d.confidenceBands.high ?? 0)],
                ["Средняя (0,4–0,7)", num(d.confidenceBands.medium ?? 0)],
                ["Низкая (< 0,4)", num(d.confidenceBands.low ?? 0)],
              ]}
            />
          </PrintSection>
          <PrintSection title="Источники">
            <SheetTable sheet={sheets[0] ?? { name: "", columns: [], rows: [] }} limit={60} />
          </PrintSection>
        </>
      )}
    </PrintDoc>
  );
}
