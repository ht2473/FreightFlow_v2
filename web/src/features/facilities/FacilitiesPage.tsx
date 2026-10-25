import { keepPreviousData } from "@tanstack/react-query";
import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import {
  type ColumnVisibilityState,
  columnVisibilityFeature,
  createColumnHelper,
  rowSortingFeature,
  type SortingState,
  tableFeatures,
  useTable,
} from "@tanstack/react-table";
import { ArrowDown, ArrowUp, Columns3, GitCompare, Map as MapIcon, MessageSquareWarning, X } from "lucide-react";
import { useRef, useState } from "react";
import { Dialog, DialogTrigger, Popover } from "react-aria-components";
import { Guard } from "@/app/Guard";
import { $api, problemMessage, type Schemas } from "@/shared/api/client";
import { can, useMe } from "@/shared/auth";
import { num } from "@/shared/format";
import {
  Badge,
  Button,
  Checkbox,
  EmptyState,
  PageHeader,
  popoverClass,
  Section,
  Skeleton,
  TextField,
} from "@/shared/ui";
import { NumberField } from "@/shared/ui/fields";
import { Pagination, SegmentedControl } from "@/shared/ui/navigation";
import { ViewsMenu } from "@/shared/ViewsMenu";
import { maxCompare, useCompare } from "./compare";
import { ExportMenu } from "./ExportMenu";
import { FacilityDrawer } from "./FacilityDrawer";
import s from "./facilities.module.css";
import { area, confidenceLevel } from "./labels";
import { type FacilitySearch, facilityQuery, formatSort, hasFilters, parseSort, toggleListValue } from "./search";

type Item = Schemas["FacilityItem"];

const pageSize = 50;
const features = tableFeatures({ rowSortingFeature, columnVisibilityFeature });
const helper = createColumnHelper<typeof features, Item>();
const emptyRows: Item[] = [];

const columns = helper.columns([
  helper.accessor((r) => r.name ?? r.typeName, {
    id: "name",
    header: "Объект",
    cell: (c) => (
      <div>
        <div className={s.name}>{c.getValue()}</div>
        {c.row.original.address && <div className={s.muted}>{c.row.original.address}</div>}
      </div>
    ),
  }),
  helper.accessor("typeName", { id: "type", header: "Тип" }),
  helper.accessor((r) => r.okrug ?? "", { id: "okrug", header: "Округ", enableSorting: false }),
  helper.accessor("buildingAreaM2", {
    id: "area",
    header: "Площадь застройки",
    cell: (c) => area(c.getValue()),
    meta: { numeric: true },
  }),
  helper.accessor("floorAreaM2", {
    id: "floorArea",
    header: "Общая площадь (оценка)",
    cell: (c) => area(c.getValue()),
    meta: { numeric: true },
  }),
  helper.accessor("mkadMin", {
    id: "mkad",
    header: "До МКАД, мин",
    cell: (c) => (c.getValue() === undefined ? "—" : num(c.getValue())),
    meta: { numeric: true },
  }),
  helper.accessor("confidence", {
    id: "confidence",
    header: "Достоверность",
    cell: (c) => {
      const l = confidenceLevel(c.getValue());
      return (
        <Badge tone={l.tone}>
          {num(c.getValue() * 100)} % · {l.label}
        </Badge>
      );
    },
  }),
  helper.accessor("hasPermit", {
    id: "permit",
    header: "Разрешение на ввод",
    enableSorting: false,
    cell: (c) => (c.getValue() ? "есть" : ""),
  }),
]);

const columnTitles: Record<string, string> = {
  type: "Тип",
  okrug: "Округ",
  area: "Площадь застройки",
  floorArea: "Общая площадь",
  mkad: "До МКАД",
  confidence: "Достоверность",
  permit: "Разрешение на ввод",
};

const visibilityKey = "ff.facilities.columns";

function loadVisibility(): ColumnVisibilityState {
  try {
    return JSON.parse(localStorage.getItem(visibilityKey) ?? "{}") as ColumnVisibilityState;
  } catch {
    return {};
  }
}

export function FacilitiesPage() {
  return (
    <Guard permission="map.view">
      <Registry />
    </Guard>
  );
}

function Registry() {
  const search = useSearch({ from: "/facilities" });
  const navigate = useNavigate({ from: "/facilities" });
  const set = (patch: Partial<FacilitySearch>) =>
    navigate({ search: (prev: FacilitySearch) => ({ ...prev, ...patch }), replace: true });

  const me = useMe();
  const query = facilityQuery(search);
  const sort = formatSort(parseSort(search.sort));
  const pageKey = JSON.stringify([query, sort]);
  // Курсоры страниц выборки: смещение → курсор (первая страница — без курсора).
  const [pages, setPages] = useState<{ key: string; offset: number; cursors: Record<number, string> }>({
    key: pageKey,
    offset: 0,
    cursors: {},
  });
  const offset = pages.key === pageKey ? pages.offset : 0;
  const cursor = pages.key === pageKey ? pages.cursors[offset] : undefined;

  const list = $api.useQuery(
    "get",
    "/facilities",
    { params: { query: { ...query, sort, cursor, limit: pageSize } } },
    { placeholderData: keepPreviousData },
  );
  const facets = $api.useQuery(
    "get",
    "/facilities/facets",
    { params: { query } },
    { placeholderData: keepPreviousData },
  );

  const sorting: SortingState = parseSort(search.sort);
  const [visibility, setVisibility] = useState<ColumnVisibilityState>(loadVisibility);
  const table = useTable({
    features,
    columns,
    data: list.data?.items ?? emptyRows,
    getRowId: (r) => r.id,
    manualSorting: true,
    state: { sorting, columnVisibility: visibility },
    onSortingChange: (updater) => {
      const next = typeof updater === "function" ? updater(sorting) : updater;
      set({ sort: formatSort(next) });
    },
    onColumnVisibilityChange: (updater) => {
      setVisibility((prev) => {
        const next = typeof updater === "function" ? updater(prev) : updater;
        try {
          localStorage.setItem(visibilityKey, JSON.stringify(next));
        } catch {
          // Без хранилища выбор колонок живёт до перезагрузки.
        }
        return next;
      });
    },
  });

  const total = list.data?.total ?? 0;
  const goTo = (next: number) => {
    const cursors = pages.key === pageKey ? pages.cursors : {};
    if (next > offset && list.data?.next) {
      cursors[next] = list.data.next;
    }
    setPages({ key: pageKey, offset: next, cursors: { ...cursors } });
  };

  return (
    <>
      <PageHeader
        title="Объекты"
        subtitle="Реестр объектов логистики: склады, терминалы, холодильники, ж/д и оптовые площадки"
        actions={
          <>
            {can(me.data, "registry.curate") && <ReportsLink />}
            <ViewsMenu
              page="facilities"
              params={{ ...search, open: undefined }}
              onApply={(p) => navigate({ search: p as FacilitySearch, replace: true })}
            />
            <Link
              to="/map"
              search={{
                l: "facility_areas,facilities",
                ft: search.type,
                fc: query.minConfidence,
                fm: search.mkad,
              }}
              className="btn btn-sm"
            >
              <MapIcon size={14} aria-hidden /> На карте
            </Link>
          </>
        }
      />
      <div className={s.layout}>
        <FacetPanel search={search} set={set} facets={facets.data} />
        <div className={s.main}>
          <CompareBar />
          <SearchBar search={search} set={set} />
          <Section flush>
            <div className={s.tableHead}>
              <span className={s.muted}>{list.isPending ? "Загрузка…" : `Найдено объектов: ${num(total)}`}</span>
              <div className={s.tableTools}>
                {can(me.data, "exports.create") && <ExportMenu filter={query} sort={sort} />}
                <ColumnsMenu visibility={visibility} onToggle={(id, on) => table.getColumn(id)?.toggleVisibility(on)} />
              </div>
            </div>
            {list.error ? (
              <div className="notice notice-error" role="alert">
                {problemMessage(list.error)}
              </div>
            ) : list.isPending ? (
              <div className={s.pad}>
                <Skeleton height={36} />
                <Skeleton height={36} />
                <Skeleton height={36} />
              </div>
            ) : total === 0 ? (
              <EmptyState title="Объектов не найдено">Измените или сбросьте фильтры.</EmptyState>
            ) : (
              <div className={s.scroll}>
                <table className={s.table} aria-label="Реестр объектов" aria-rowcount={total}>
                  <thead>
                    {table.getHeaderGroups().map((g) => (
                      <tr key={g.id}>
                        {g.headers.map((h) => {
                          const sorted = h.column.getIsSorted();
                          const numeric = (h.column.columnDef.meta as { numeric?: boolean } | undefined)?.numeric;
                          return (
                            <th
                              key={h.id}
                              className={numeric ? s.right : undefined}
                              aria-sort={sorted === "asc" ? "ascending" : sorted === "desc" ? "descending" : undefined}
                            >
                              {h.column.getCanSort() ? (
                                <button
                                  type="button"
                                  className={s.sortButton}
                                  onClick={h.column.getToggleSortingHandler()}
                                >
                                  <table.FlexRender header={h} />
                                  {sorted === "asc" && <ArrowUp size={13} aria-hidden />}
                                  {sorted === "desc" && <ArrowDown size={13} aria-hidden />}
                                </button>
                              ) : (
                                <table.FlexRender header={h} />
                              )}
                            </th>
                          );
                        })}
                      </tr>
                    ))}
                  </thead>
                  <tbody>
                    {table.getRowModel().rows.map((row) => (
                      <tr
                        key={row.id}
                        className={search.open === row.id ? s.selected : undefined}
                        onClick={() => set({ open: row.id })}
                      >
                        {row.getVisibleCells().map((cell) => {
                          const numeric = (cell.column.columnDef.meta as { numeric?: boolean } | undefined)?.numeric;
                          return (
                            <td key={cell.id} className={numeric ? `${s.right} num` : undefined}>
                              {cell.column.id === "name" ? (
                                <button type="button" className={s.rowLink} onClick={() => set({ open: row.id })}>
                                  <table.FlexRender cell={cell} />
                                </button>
                              ) : (
                                <table.FlexRender cell={cell} />
                              )}
                            </td>
                          );
                        })}
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}
            {total > 0 && (
              <div className={s.pager}>
                <Pagination offset={offset} limit={pageSize} total={total} onChange={goTo} />
              </div>
            )}
          </Section>
        </div>
      </div>
      <FacilityDrawer id={search.open} onClose={() => set({ open: undefined })} />
    </>
  );
}

function SearchBar({ search, set }: { search: FacilitySearch; set: (p: Partial<FacilitySearch>) => void }) {
  const [text, setText] = useState(search.q ?? "");
  // Текст поиска попадает в адрес после паузы ввода.
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const type = (v: string) => {
    setText(v);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => set({ q: v.trim() || undefined }), 300);
  };
  const chips = activeChips(search);
  return (
    <div className={s.searchBar}>
      <TextField
        label="Поиск по названию или адресу"
        value={text}
        onChange={type}
        placeholder="Например, «Вайлдберриз» или «Подольск»"
      />
      {chips.length > 0 && (
        <ul className={s.chips} aria-label="Активные фильтры">
          {chips.map((c) => (
            <li key={c.key} className={s.chip}>
              {c.label}
              <button type="button" aria-label={`Убрать фильтр «${c.label}»`} onClick={() => set(c.clear)}>
                <X size={12} aria-hidden />
              </button>
            </li>
          ))}
          <li>
            <Button
              size="sm"
              variant="ghost"
              onPress={() => {
                setText("");
                set({
                  q: undefined,
                  type: undefined,
                  okrug: undefined,
                  district: undefined,
                  ring: undefined,
                  conf: undefined,
                  restr: undefined,
                  permit: undefined,
                  mkad: undefined,
                  amin: undefined,
                  amax: undefined,
                  near: undefined,
                });
              }}
            >
              Сбросить все
            </Button>
          </li>
        </ul>
      )}
    </div>
  );
}

/** Подписи активных фильтров (значения списков — числом выбранных). */
function activeChips(sr: FacilitySearch): { key: string; label: string; clear: Partial<FacilitySearch> }[] {
  const out: { key: string; label: string; clear: Partial<FacilitySearch> }[] = [];
  const count = (v: string | undefined) => (v ? v.split(",").length : 0);
  if (sr.type) out.push({ key: "type", label: `Тип: ${count(sr.type)}`, clear: { type: undefined } });
  if (sr.okrug) out.push({ key: "okrug", label: `Округ: ${count(sr.okrug)}`, clear: { okrug: undefined } });
  if (sr.district) out.push({ key: "district", label: `Район: ${count(sr.district)}`, clear: { district: undefined } });
  if (sr.ring) out.push({ key: "ring", label: `Пояс: ${count(sr.ring)}`, clear: { ring: undefined } });
  if (sr.conf)
    out.push({
      key: "conf",
      label: sr.conf === "high" ? "Достоверность высокая" : "Достоверность от средней",
      clear: { conf: undefined },
    });
  if (sr.restr)
    out.push({
      key: "restr",
      label: sr.restr === "yes" ? "Ограничения рядом" : "Без ограничений рядом",
      clear: { restr: undefined },
    });
  if (sr.permit)
    out.push({
      key: "permit",
      label: sr.permit === "yes" ? "Есть разрешение на ввод" : "Без разрешения на ввод",
      clear: { permit: undefined },
    });
  if (sr.mkad) out.push({ key: "mkad", label: `До МКАД ≤ ${sr.mkad} мин`, clear: { mkad: undefined } });
  if (sr.amin !== undefined)
    out.push({ key: "amin", label: `Площадь ≥ ${num(sr.amin)} м²`, clear: { amin: undefined } });
  if (sr.amax !== undefined)
    out.push({ key: "amax", label: `Площадь ≤ ${num(sr.amax)} м²`, clear: { amax: undefined } });
  if (sr.near) {
    const r = Number(sr.near.split(",")[2] ?? 0);
    out.push({ key: "near", label: `В ${num(r / 1000, 1)} км от точки`, clear: { near: undefined } });
  }
  return hasFilters(sr) ? out : [];
}

function FacetPanel({
  search,
  set,
  facets,
}: {
  search: FacilitySearch;
  set: (p: Partial<FacilitySearch>) => void;
  facets: Schemas["FacilityFacets"] | undefined;
}) {
  const selected = (list: string | undefined, v: string) => (list ?? "").split(",").includes(v);
  const group = (title: string, values: Schemas["FacetValue"][] | undefined, key: "type" | "okrug" | "ring") => (
    <fieldset className={s.facet}>
      <legend>{title}</legend>
      {(values ?? []).map((v) => (
        <Checkbox
          key={v.value}
          isSelected={selected(search[key], v.value)}
          onChange={() => set({ [key]: toggleListValue(search[key], v.value) })}
        >
          <span className={s.facetLabel}>{v.label}</span>
          <span className={`${s.muted} num`}>{num(v.count)}</span>
        </Checkbox>
      ))}
    </fieldset>
  );
  return (
    <aside className={s.facets} aria-label="Фильтры">
      {group("Тип объекта", facets?.types, "type")}
      {group("Округ", facets?.okrugs, "okrug")}
      {group("Пояс", facets?.rings, "ring")}
      <fieldset className={s.facet}>
        <legend>Достоверность</legend>
        <SegmentedControl
          label="Достоверность"
          options={[
            { id: "any", label: "любая" },
            { id: "medium", label: "от средней" },
            { id: "high", label: "высокая" },
          ]}
          value={search.conf ?? "any"}
          onChange={(v) => set({ conf: v === "any" ? undefined : (v as "medium" | "high") })}
        />
      </fieldset>
      <fieldset className={s.facet}>
        <legend>Подъезд и документы</legend>
        <SegmentedControl
          label="Ограничения в 300 м"
          options={[
            { id: "any", label: "все" },
            { id: "yes", label: "с ограничениями" },
            { id: "no", label: "без" },
          ]}
          value={search.restr ?? "any"}
          onChange={(v) => set({ restr: v === "any" ? undefined : (v as "yes" | "no") })}
        />
        <Checkbox isSelected={search.permit === "yes"} onChange={(on) => set({ permit: on ? "yes" : undefined })}>
          Есть разрешение на ввод
        </Checkbox>
        <NumberField
          label="До МКАД (20 т) не дольше"
          unit="мин"
          value={search.mkad ?? null}
          minValue={1}
          maxValue={300}
          onChange={(v) => set({ mkad: v ?? undefined })}
        />
      </fieldset>
    </aside>
  );
}

function ColumnsMenu({
  visibility,
  onToggle,
}: {
  visibility: ColumnVisibilityState;
  onToggle: (id: string, on: boolean) => void;
}) {
  return (
    <DialogTrigger>
      <Button size="sm" variant="ghost">
        <Columns3 size={14} aria-hidden /> Колонки
      </Button>
      <Popover className={popoverClass} placement="bottom end">
        <Dialog className={s.columnsDialog} aria-label="Колонки таблицы">
          {Object.entries(columnTitles).map(([id, title]) => (
            <Checkbox key={id} isSelected={visibility[id] !== false} onChange={(on) => onToggle(id, on)}>
              {title}
            </Checkbox>
          ))}
        </Dialog>
      </Popover>
    </DialogTrigger>
  );
}

/** Набор для сравнения: число объектов, переход к сравнению, очистка. */
function CompareBar() {
  const { ids, clear } = useCompare();
  if (ids.length === 0) {
    return null;
  }
  return (
    <div className={s.compareBar} role="status">
      <span>
        В сравнении: {ids.length} из {maxCompare}
        {ids.length < 2 && " — добавьте ещё объект"}
      </span>
      <div>
        {ids.length >= 2 && (
          <Link to="/facilities/compare" search={{ ids: ids.join(",") }} className="btn btn-sm btn-primary">
            <GitCompare size={14} aria-hidden /> Сравнить
          </Link>
        )}
        <Button size="sm" variant="ghost" onPress={clear}>
          Очистить
        </Button>
      </div>
    </div>
  );
}

/** Ссылка на очередь сообщений о неточностях с числом открытых. */
function ReportsLink() {
  const open = $api.useQuery(
    "get",
    "/facility-reports",
    { params: { query: { status: "open" } } },
    { staleTime: 30_000 },
  );
  const n = open.data?.length ?? 0;
  return (
    <Link to="/facilities/reports" className="btn btn-sm">
      <MessageSquareWarning size={14} aria-hidden /> Сообщения о неточностях{n > 0 ? ` (${n})` : ""}
    </Link>
  );
}
