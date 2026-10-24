import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { ArrowDown, ArrowUp } from "lucide-react";
import { useMemo } from "react";
import { Guard } from "@/app/Guard";
import { $api, problemMessage, type Schemas } from "@/shared/api/client";
import { num } from "@/shared/format";
import { EmptyState, PageHeader, Section, Skeleton, TextField } from "@/shared/ui";
import { SegmentedControl } from "@/shared/ui/navigation";
import t from "../facilities/facilities.module.css";
import { area } from "../facilities/labels";
import { formatSort, parseSort, sortRows, type TerritorySearch } from "./search";
import s from "./territories.module.css";

type Row = Schemas["TerritoryRow"];

interface Column {
  id: string;
  title: string;
  numeric?: boolean;
  cell: (r: Row) => React.ReactNode;
}

const dash = (v: number | undefined, digits = 0) => (v === undefined ? "—" : num(v, digits));

/** Колонки таблицы территорий; у районов есть колонка округа. */
export function territoryColumns(withParent: boolean): Column[] {
  return [
    {
      id: "name",
      title: "Территория",
      cell: (r) => (
        <Link to="/territories/$territoryId" params={{ territoryId: r.id }} className={t.rowLink}>
          <span className={t.name}>{r.name}</span>
        </Link>
      ),
    },
    ...(withParent ? [{ id: "parent", title: "Округ", cell: (r: Row) => r.parentName ?? "—" }] : []),
    { id: "area", title: "Площадь, км²", numeric: true, cell: (r) => num(r.areaKm2, r.areaKm2 < 10 ? 1 : 0) },
    { id: "population", title: "Население", numeric: true, cell: (r) => dash(r.population) },
    { id: "density", title: "Плотность, чел./км²", numeric: true, cell: (r) => dash(r.density) },
    { id: "facilities", title: "Объектов", numeric: true, cell: (r) => num(r.facilities) },
    { id: "building", title: "Площадь застройки", numeric: true, cell: (r) => area(r.buildingAreaM2) },
    {
      id: "buildingDensity",
      title: "Застройка, м²/км²",
      numeric: true,
      cell: (r) => (r.areaKm2 > 0 ? num(r.buildingAreaM2 / r.areaKm2) : "—"),
    },
    { id: "poi", title: "Инфраструктура", numeric: true, cell: (r) => num(r.poi) },
    { id: "works", title: "Работ идёт", numeric: true, cell: (r) => num(r.worksActive) },
  ];
}

/** Таблица территорий с сортировкой по любой колонке. */
export function TerritoryTable({
  rows,
  label,
  withParent,
  sort,
  onSort,
}: {
  rows: Row[];
  label: string;
  withParent: boolean;
  sort: { id: string; desc: boolean };
  onSort: (next: { id: string; desc: boolean }) => void;
}) {
  const columns = territoryColumns(withParent);
  return (
    <div className={t.scroll}>
      <table className={t.table} aria-label={label}>
        <thead>
          <tr>
            {columns.map((c) => {
              const sorted = sort.id === c.id ? (sort.desc ? "desc" : "asc") : undefined;
              return (
                <th
                  key={c.id}
                  className={c.numeric ? t.right : undefined}
                  aria-sort={sorted === "asc" ? "ascending" : sorted === "desc" ? "descending" : undefined}
                >
                  <button
                    type="button"
                    className={t.sortButton}
                    // Первое нажатие: числа — по убыванию, названия — по алфавиту.
                    onClick={() => onSort({ id: c.id, desc: sorted ? sorted === "asc" : Boolean(c.numeric) })}
                  >
                    {c.title}
                    {sorted === "asc" && <ArrowUp size={13} aria-hidden />}
                    {sorted === "desc" && <ArrowDown size={13} aria-hidden />}
                  </button>
                </th>
              );
            })}
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr key={r.id}>
              {columns.map((c) => (
                <td key={c.id} className={c.numeric ? `${t.right} num` : undefined}>
                  {c.cell(r)}
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}

export function TerritoriesPage() {
  return (
    <Guard permission="map.view">
      <Territories />
    </Guard>
  );
}

function Territories() {
  const search = useSearch({ from: "/territories" });
  const navigate = useNavigate({ from: "/territories" });
  const set = (patch: Partial<TerritorySearch>) =>
    navigate({ search: (prev: TerritorySearch) => ({ ...prev, ...patch }), replace: true });
  const kind = search.kind ?? "district";
  const list = $api.useQuery("get", "/territories", { params: { query: { kind } } }, { staleTime: 60_000 });
  const sort = parseSort(search.sort);
  const rows = useMemo(() => sortRows(list.data ?? [], search.q, sort), [list.data, search.q, sort]);
  const year = list.data?.find((r) => r.populationYear)?.populationYear;

  return (
    <>
      <PageHeader
        title="Территории"
        subtitle="Округа и районы Москвы: население, объекты логистики, инфраструктура, дорожные работы"
      />
      <Section>
        <div className={s.toolbar}>
          <SegmentedControl
            label="Уровень территорий"
            options={[
              { id: "district", label: "Районы" },
              { id: "okrug", label: "Округа" },
            ]}
            value={kind}
            onChange={(k) => set({ kind: k === "district" ? undefined : (k as "okrug") })}
          />
          <TextField
            label="Поиск по названию"
            value={search.q ?? ""}
            onChange={(q) => set({ q: q || undefined })}
            placeholder="Например, «Нижегородский»"
          />
        </div>
        {list.error ? (
          <div className="notice notice-error" role="alert">
            {problemMessage(list.error)}
          </div>
        ) : list.isPending ? (
          <Skeleton height={240} />
        ) : rows.length === 0 ? (
          <EmptyState title="Территорий не найдено">Измените запрос.</EmptyState>
        ) : (
          <>
            <TerritoryTable
              rows={rows}
              label={kind === "okrug" ? "Округа" : "Районы"}
              withParent={kind === "district"}
              sort={sort}
              onSort={(next) => set({ sort: formatSort(next) })}
            />
            <p className={s.note}>
              Население — Росстат, на 1 января {year ?? "последнего года"} (у округа — сумма районов). Объекты — по
              привязке реестра; инфраструктура и работы — в границах территории.
            </p>
          </>
        )}
      </Section>
    </>
  );
}
