import { z } from "zod";

/**
 * Фильтры реестра в адресе страницы: ссылка воспроизводит выборку. Списки — через запятую;
 * неверное значение отбрасывается.
 */
export const facilitySearchSchema = z.object({
  q: z.string().optional().catch(undefined),
  type: z.string().optional().catch(undefined),
  okrug: z.string().optional().catch(undefined),
  district: z.string().optional().catch(undefined),
  ring: z.string().optional().catch(undefined),
  conf: z.enum(["medium", "high"]).optional().catch(undefined),
  restr: z.enum(["yes", "no"]).optional().catch(undefined),
  permit: z.enum(["yes", "no"]).optional().catch(undefined),
  mkad: z.number().min(1).max(300).optional().catch(undefined),
  // Площадь застройки от и до, м²; окрестность «долгота,широта,радиус м» (из разбора поиска).
  amin: z.number().min(0).optional().catch(undefined),
  amax: z.number().min(0).optional().catch(undefined),
  near: z
    .string()
    .regex(/^-?\d+(\.\d+)?,-?\d+(\.\d+)?,\d+(\.\d+)?$/)
    .optional()
    .catch(undefined),
  sort: z.string().optional().catch(undefined),
  open: z.string().uuid().optional().catch(undefined),
});

export type FacilitySearch = z.infer<typeof facilitySearchSchema>;

const list = (v: string | undefined) => (v ? v.split(",").filter(Boolean) : []);

/** Изменение списка в адресе: значение добавляется или убирается. */
export function toggleListValue(current: string | undefined, value: string): string | undefined {
  const items = list(current);
  const next = items.includes(value) ? items.filter((x) => x !== value) : [...items, value];
  return next.length > 0 ? next.join(",") : undefined;
}

export const minConfidence = { medium: 0.4, high: 0.7 } as const;

type Ring = "sk" | "ttk" | "mkad" | "ckad" | "outside";

/** Параметры запроса к API реестра (без сортировки и страницы). */
export function facilityQuery(s: FacilitySearch) {
  return {
    q: s.q?.trim() || undefined,
    type: list(s.type),
    okrug: list(s.okrug),
    district: list(s.district),
    ring: list(s.ring) as Ring[],
    minConfidence: s.conf ? minConfidence[s.conf] : undefined,
    nearRestrictions: s.restr ? s.restr === "yes" : undefined,
    hasPermit: s.permit ? s.permit === "yes" : undefined,
    maxMkadMin: s.mkad,
    minArea: s.amin,
    maxArea: s.amax,
    near: s.near ? s.near.split(",").map(Number) : undefined,
  };
}

/** Есть ли в выборке хоть один фильтр. */
export function hasFilters(s: FacilitySearch): boolean {
  return Boolean(
    s.q ||
      s.type ||
      s.okrug ||
      s.district ||
      s.ring ||
      s.conf ||
      s.restr ||
      s.permit ||
      s.mkad ||
      s.amin !== undefined ||
      s.amax !== undefined ||
      s.near,
  );
}

export interface SortItem {
  id: string;
  desc: boolean;
}

export const defaultSort = "-confidence,name";

/** Сортировка API («-confidence,name») в состояние таблицы. */
export function parseSort(sort: string | undefined): SortItem[] {
  return list(sort ?? defaultSort).map((f) =>
    f.startsWith("-") ? { id: f.slice(1), desc: true } : { id: f, desc: false },
  );
}

/** Состояние сортировки таблицы в параметр API; пустое — сортировка по умолчанию. */
export function formatSort(items: SortItem[]): string | undefined {
  const s = items.map((x) => (x.desc ? `-${x.id}` : x.id)).join(",");
  return s && s !== defaultSort ? s : undefined;
}

/** Сравнение: номера объектов через запятую (без них — набор из карточек). */
export const compareSearchSchema = z.object({
  ids: z
    .string()
    .regex(/^[0-9a-f-]{36}(,[0-9a-f-]{36}){0,3}$/)
    .optional()
    .catch(undefined),
});
