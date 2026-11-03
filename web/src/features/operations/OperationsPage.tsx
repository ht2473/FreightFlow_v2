import { Link } from "@tanstack/react-router";
import { FileText, Map as MapIcon } from "lucide-react";
import { useMemo, useState } from "react";
import { Guard } from "@/app/Guard";
import { $api, problemMessage } from "@/shared/api/client";
import { date, num } from "@/shared/format";
import { Badge, cellClass, EmptyState, PageHeader, Section, Skeleton, TextField, tableClass } from "@/shared/ui";
import { Chart } from "@/shared/ui/Chart";
import { KpiTile } from "@/shared/ui/KpiTile";
import { SegmentedControl } from "@/shared/ui/navigation";
import { workKindLabel } from "../facilities/labels";
import { formatCenter } from "../map/state";
import s from "./operations.module.css";

/** Сводка работ — тот же ответ, что у отчёта «Дорожные работы» (/report-data/roadworks). */
interface Roadworks {
  days: number;
  active: number;
  planned: number;
  closures: number;
  byKind: Record<string, number>;
  works: Work[];
  series: { day: string; value: number }[];
}

interface Work {
  id: string;
  title: string;
  kind: "repair" | "emergency" | "closure";
  place: string;
  district: string;
  state: "active" | "planned";
  closure: boolean;
  start?: string;
  end?: string;
  facilities: number;
  impactKm: number;
  geometry?: { type: string; coordinates: unknown };
}

type StateFilter = "all" | "active" | "planned" | "closure";

const stateFilters: { id: StateFilter; label: string }[] = [
  { id: "all", label: "Все" },
  { id: "active", label: "Идут" },
  { id: "planned", label: "Начнутся" },
  { id: "closure", label: "С перекрытием" },
];

export function OperationsPage() {
  return (
    <Guard permission="map.view">
      <Operations />
    </Guard>
  );
}

function Operations() {
  const days = 14;
  const q = $api.useQuery("get", "/report-data/roadworks", { params: { query: { days } } });
  const d = q.data as Roadworks | undefined;
  const [filter, setFilter] = useState<StateFilter>("all");
  const [text, setText] = useState("");
  const shown = useMemo(() => {
    const needle = text.trim().toLowerCase();
    return (d?.works ?? []).filter(
      (w) =>
        (filter === "all" || (filter === "closure" ? w.closure : w.state === filter)) &&
        (!needle || `${w.title} ${w.place} ${w.district}`.toLowerCase().includes(needle)),
    );
  }, [d, filter, text]);

  return (
    <>
      <PageHeader
        title="Оперативная обстановка"
        subtitle={`Дорожные работы: идущие и начинающиеся в ближайшие ${days} дней, влияние на сеть и объекты`}
        actions={
          <>
            <Link to="/map" search={{ l: "facilities,facility_areas,rings,road_works" }} className="btn btn-sm">
              <MapIcon size={14} aria-hidden /> На карте
            </Link>
            <Link to="/reports" search={{ template: "roadworks" }} className="btn btn-sm">
              <FileText size={14} aria-hidden /> Отчёт
            </Link>
          </>
        }
      />
      {q.isPending ? (
        <Skeleton height={320} />
      ) : q.error || !d ? (
        <div className="notice notice-error" role="alert">
          {problemMessage(q.error)}
        </div>
      ) : (
        <div className={s.page}>
          <div className={s.kpis}>
            <KpiTile label="Идут сейчас" value={d.active} source="Портал открытых данных Москвы" />
            <KpiTile label={`Начнутся за ${days} дней`} value={d.planned} source="Портал открытых данных Москвы" />
            <KpiTile label="С перекрытием проезжей части" value={d.closures} source="Портал открытых данных Москвы" />
            <KpiTile label="Аварийных" value={d.byKind.emergency ?? 0} source="Портал открытых данных Москвы" />
          </div>
          {d.series.length > 1 && (
            <Section title="Идущие работы по дням" description="Москва, ежедневные снимки состояния">
              <Chart
                label="Идущие дорожные работы по дням"
                labels={d.series.map((p) => date(p.day))}
                series={[{ name: "Идущие работы", kind: "line", color: "acc", data: d.series.map((p) => p.value) }]}
                height={220}
                exportName="works-active"
                compact
              />
            </Section>
          )}
          <Section
            title="Работы"
            description="Сначала идущие, с перекрытием и аварийные; затронутая сеть — длина участков в зоне работ"
            flush
          >
            <div className={s.filters}>
              <SegmentedControl
                label="Состояние"
                value={filter}
                onChange={(v) => setFilter(v as StateFilter)}
                options={stateFilters}
              />
              <TextField label="Поиск" placeholder="Улица, район, вид работ" value={text} onChange={setText} />
              <span className={s.count}>Показано: {num(shown.length)}</span>
            </div>
            {shown.length === 0 ? (
              <EmptyState title="Работ не найдено">Измените фильтр или запрос.</EmptyState>
            ) : (
              <div className={s.scroll}>
                <table className={tableClass} aria-label="Дорожные работы">
                  <thead>
                    <tr>
                      <th>Работа</th>
                      <th>Вид</th>
                      <th>Район</th>
                      <th>Сроки</th>
                      <th className={cellClass.right}>Объектов в 500 м</th>
                      <th className={cellClass.right}>Затронуто сети, км</th>
                      <th>
                        <span className="visually-hidden">Действия</span>
                      </th>
                    </tr>
                  </thead>
                  <tbody>
                    {shown.slice(0, 200).map((w) => (
                      <WorkRow key={w.id} w={w} />
                    ))}
                  </tbody>
                </table>
              </div>
            )}
          </Section>
        </div>
      )}
    </>
  );
}

function WorkRow({ w }: { w: Work }) {
  const center = firstPoint(w.geometry);
  return (
    <tr>
      <td>
        <div className={s.title}>{w.title}</div>
        {w.place && <div className={s.muted}>{w.place}</div>}
      </td>
      <td>
        <div className={s.badges}>
          <Badge tone={w.kind === "emergency" ? "crit" : w.kind === "closure" ? "warn" : "neutral"}>
            {workKindLabel[w.kind]}
          </Badge>
          {w.closure && <Badge tone="crit">перекрытие</Badge>}
        </div>
      </td>
      <td>{w.district || "—"}</td>
      <td className={s.nowrap}>
        <div>{w.state === "active" ? "идёт" : "начнётся"}</div>
        <div className={s.muted}>
          {w.start || w.end ? `${w.start ? date(w.start) : "…"} – ${w.end ? date(w.end) : "…"}` : "сроки не указаны"}
        </div>
      </td>
      <td className={`${cellClass.right} num`}>{num(w.facilities)}</td>
      <td className={`${cellClass.right} num`}>{num(w.impactKm, 2)}</td>
      <td className={cellClass.right}>
        {center && (
          <Link
            to="/map"
            search={{
              c: formatCenter(center[0], center[1]),
              z: 15,
              l: "facilities,facility_areas,rings,road_works,restrictions",
              pin: formatCenter(center[0], center[1]),
              pt: w.place || w.title,
            }}
            className={s.mapLink}
          >
            На карте
          </Link>
        )}
      </td>
    </tr>
  );
}

/** Первая точка геометрии работы — центр для перехода на карту. */
function firstPoint(g: Work["geometry"]): [number, number] | null {
  let c: unknown = g?.coordinates;
  while (Array.isArray(c) && Array.isArray(c[0])) {
    c = c[0];
  }
  return Array.isArray(c) && typeof c[0] === "number" && typeof c[1] === "number" ? [c[0], c[1]] : null;
}
