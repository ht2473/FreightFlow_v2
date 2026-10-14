import { Link, useSearch } from "@tanstack/react-router";
import { GitCompare } from "lucide-react";
import type { ReactNode } from "react";
import { Guard } from "@/app/Guard";
import { $api, problemMessage, type Schemas } from "@/shared/api/client";
import { num } from "@/shared/format";
import { Badge, EmptyState, PageHeader, Section, Skeleton, tableClass } from "@/shared/ui";
import { useCompare } from "./compare";
import s from "./facilities.module.css";
import { area, confidenceLevel, distance, poiLabel, ringLabel } from "./labels";

type Card = Schemas["FacilityCard"];

export function ComparePage() {
  return (
    <Guard permission="map.view">
      <Compare />
    </Guard>
  );
}

const minutes = (c: Card, profile: string, target: string) =>
  c.access.find((a) => a.profile === profile && a.target === target)?.minutes;

const nearest = (c: Card, kind: string) => c.nearby.find((p) => p.kind === kind)?.distanceM;

interface Row {
  title: string;
  /** Числовое значение для выделения лучшего; better — какое лучше. */
  value?: (c: Card) => number | undefined;
  better?: "min" | "max";
  render: (c: Card) => ReactNode;
}

const targetTitle = { mkad: "До МКАД", ttk: "До ТТК", center: "До центра", rail_freight: "До ж/д пункта" } as const;

const rows: Row[] = [
  { title: "Тип", render: (c) => c.facility.typeName },
  { title: "Адрес", render: (c) => c.facility.address ?? "—" },
  {
    title: "Территория",
    render: (c) =>
      [c.facility.okrug, c.facility.district, c.facility.ring && ringLabel[c.facility.ring]]
        .filter(Boolean)
        .join(", ") || "—",
  },
  {
    title: "Площадь застройки",
    value: (c) => c.facility.buildingAreaM2,
    better: "max",
    render: (c) => area(c.facility.buildingAreaM2),
  },
  {
    title: "Общая площадь (оценка)",
    value: (c) => c.facility.floorAreaM2,
    better: "max",
    render: (c) => area(c.facility.floorAreaM2),
  },
  {
    title: "Достоверность",
    value: (c) => c.facility.confidence,
    better: "max",
    render: (c) => `${num(c.facility.confidence * 100)} % · ${confidenceLevel(c.facility.confidence).label}`,
  },
  ...(["mkad", "ttk", "center", "rail_freight"] as const).map(
    (t): Row => ({
      title: `${targetTitle[t]} (20 т), мин`,
      value: (c) => minutes(c, "truck_20", t),
      better: "min",
      render: (c) => {
        const v = minutes(c, "truck_20", t);
        return v === undefined ? "—" : num(v);
      },
    }),
  ),
  {
    title: "Ограничений в 500 м",
    value: (c) => c.restrictions.length,
    better: "min",
    render: (c) => num(c.restrictions.length),
  },
  {
    title: "Дорожных работ рядом",
    value: (c) => c.works.length,
    better: "min",
    render: (c) => num(c.works.length),
  },
  ...(["fuel", "truck_parking", "rail_freight"] as const).map(
    (k): Row => ({
      title: `${poiLabel[k]}, ближайшая`,
      value: (c) => nearest(c, k),
      better: "min",
      render: (c) => {
        const d = nearest(c, k);
        return d === undefined ? "дальше 3 км" : distance(d);
      },
    }),
  ),
  { title: "Разрешение на ввод", render: (c) => (c.facility.hasPermit ? "есть" : "—") },
];

/** Номера объектов с лучшим значением строки (несколько при равенстве; все равны — никого). */
export function best(row: Pick<Row, "value" | "better">, cards: Card[]): Set<number> {
  if (!row.value || !row.better) {
    return new Set();
  }
  const values = cards.map((c) => row.value?.(c));
  const defined = values.filter((v): v is number => v !== undefined);
  if (defined.length < 2) {
    return new Set();
  }
  const target = row.better === "min" ? Math.min(...defined) : Math.max(...defined);
  if (defined.every((v) => v === target)) {
    return new Set();
  }
  return new Set(values.flatMap((v, i) => (v === target ? [i] : [])));
}

function Compare() {
  const search = useSearch({ from: "/facilities/compare" });
  const stored = useCompare((st) => st.ids);
  const ids = (search.ids ? search.ids.split(",") : stored).slice(0, 4);
  const q = $api.useQuery(
    "get",
    "/facilities/compare",
    { params: { query: { id: ids } } },
    { enabled: ids.length >= 2 },
  );
  return (
    <>
      <PageHeader
        title="Сравнение объектов"
        subtitle="Площади, достоверность, доступность и окружение — лучшее значение выделено"
      />
      {ids.length < 2 ? (
        <Section>
          <EmptyState icon={<GitCompare size={20} />} title="Выберите от двух до четырёх объектов">
            В карточке объекта нажмите «Сравнить». <Link to="/facilities">Перейти к реестру</Link>
          </EmptyState>
        </Section>
      ) : q.isPending ? (
        <Section>
          <Skeleton height={240} />
        </Section>
      ) : q.error ? (
        <Section>
          <div className="notice notice-error" role="alert">
            {problemMessage(q.error)}
          </div>
        </Section>
      ) : (
        <Section flush>
          <div className={s.scroll}>
            <table className={`${tableClass} ${s.compare}`}>
              <thead>
                <tr>
                  <th aria-label="Показатель" />
                  {q.data.map((c) => (
                    <th key={c.facility.id} scope="col">
                      <Link to="/facilities" search={{ open: c.facility.id }}>
                        {c.facility.name ?? c.facility.typeName}
                      </Link>
                      <div>
                        <Badge plain>{c.facility.typeName}</Badge>
                      </div>
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {rows.map((row) => {
                  const top = best(row, q.data);
                  return (
                    <tr key={row.title}>
                      <th scope="row">{row.title}</th>
                      {q.data.map((c, i) => (
                        <td key={c.facility.id} className={top.has(i) ? `${s.best} num` : "num"}>
                          {row.render(c)}
                          {top.has(i) && <span className="visually-hidden"> (лучшее)</span>}
                        </td>
                      ))}
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </Section>
      )}
    </>
  );
}
