import { z } from "zod";
import type { Schemas } from "@/shared/api/client";

/** Список территорий в адресе: уровень, подстрока названия, сортировка («-population»). */
export const territorySearchSchema = z.object({
  kind: z.enum(["district", "okrug"]).optional().catch(undefined),
  q: z.string().optional().catch(undefined),
  sort: z.string().optional().catch(undefined),
});

export type TerritorySearch = z.infer<typeof territorySearchSchema>;

type Row = Schemas["TerritoryRow"];

/** Показатели для сортировки и колонок; пустое значение — в конце списка при любом порядке. */
export const metrics: Record<string, (r: Row) => number | string | undefined> = {
  name: (r) => r.name,
  parent: (r) => r.parentName,
  area: (r) => r.areaKm2,
  population: (r) => r.population,
  density: (r) => r.density,
  facilities: (r) => r.facilities,
  building: (r) => r.buildingAreaM2,
  buildingDensity: (r) => (r.areaKm2 > 0 ? r.buildingAreaM2 / r.areaKm2 : undefined),
  poi: (r) => r.poi,
  works: (r) => r.worksActive,
};

export const defaultSort = "-facilities";

/** Сортировка из адреса: поле и направление; неизвестное поле — по умолчанию. */
export function parseSort(sort: string | undefined): { id: string; desc: boolean } {
  const v = sort ?? defaultSort;
  const id = v.startsWith("-") ? v.slice(1) : v;
  return id in metrics ? { id, desc: v.startsWith("-") } : parseSort(defaultSort);
}

export function formatSort(s: { id: string; desc: boolean }): string | undefined {
  const v = s.desc ? `-${s.id}` : s.id;
  return v === defaultSort ? undefined : v;
}

/** Строки по подстроке названия и сортировке. */
export function sortRows(rows: Row[], q: string | undefined, sort: { id: string; desc: boolean }): Row[] {
  const get = metrics[sort.id] ?? metrics.facilities;
  const needle = q?.trim().toLowerCase();
  const out = needle
    ? rows.filter((r) => r.name.toLowerCase().includes(needle) || r.shortName?.toLowerCase().includes(needle))
    : [...rows];
  return out.sort((a, b) => {
    const x = get?.(a);
    const y = get?.(b);
    if (x === undefined || y === undefined) {
      return x === y ? 0 : x === undefined ? 1 : -1;
    }
    const c = typeof x === "string" ? x.localeCompare(String(y), "ru") : x - Number(y);
    return sort.desc ? -c : c;
  });
}
