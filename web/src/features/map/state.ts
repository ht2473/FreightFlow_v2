import type { ExpressionSpecification } from "maplibre-gl";
import { z } from "zod";
import { create } from "zustand";
import type { LayerState, MapLayer } from "@/shared/map/dataLayers";

/**
 * Параметры адреса карты: центр «долгота,широта», масштаб, включённые слои через запятую.
 * Неверное значение отбрасывается, а не ломает страницу.
 */
export const mapSearchSchema = z.object({
  c: z.string().optional().catch(undefined),
  z: z.number().min(0).max(22).optional().catch(undefined),
  l: z.string().optional().catch(undefined),
  // Фильтр объектов из реестра: типы через запятую, достоверность от, время до МКАД не больше.
  ft: z.string().optional().catch(undefined),
  fc: z.number().min(0).max(1).optional().catch(undefined),
  fm: z.number().min(1).max(300).optional().catch(undefined),
});

export type MapSearch = z.infer<typeof mapSearchSchema>;

export function parseCenter(c: string | undefined): [number, number] | null {
  if (!c) {
    return null;
  }
  const [lng, lat] = c.split(",").map(Number);
  if (lng === undefined || lat === undefined || !Number.isFinite(lng) || !Number.isFinite(lat)) {
    return null;
  }
  if (Math.abs(lng) > 180 || Math.abs(lat) > 85) {
    return null;
  }
  return [lng, lat];
}

export function formatCenter(lng: number, lat: number): string {
  return `${lng.toFixed(5)},${lat.toFixed(5)}`;
}

/** Включённые слои: из адреса, а без него — включённые по умолчанию в каталоге. */
export function visibleLayers(catalog: MapLayer[], l: string | undefined): Set<string> {
  if (l === undefined) {
    return new Set(catalog.filter((x) => x.visible).map((x) => x.code));
  }
  const known = new Set(catalog.map((x) => x.code));
  return new Set(l.split(",").filter((code) => known.has(code)));
}

/** Непрозрачность слоёв — до конца сеанса, без записи в адрес. */
export const useOpacity = create<{ opacity: Record<string, number>; set: (code: string, v: number) => void }>(
  (set) => ({
    opacity: {},
    set: (code, v) => set((st) => ({ opacity: { ...st.opacity, [code]: v } })),
  }),
);

export function layerStates(
  catalog: MapLayer[],
  visible: Set<string>,
  opacity: Record<string, number>,
  filters: Record<string, ExpressionSpecification> = {},
): Record<string, LayerState> {
  const out: Record<string, LayerState> = {};
  for (const l of catalog) {
    out[l.code] = { visible: visible.has(l.code), opacity: opacity[l.code] ?? 1, filter: filters[l.code] };
  }
  return out;
}

/** Подходит ли объект под фильтр реестра — то же правило, что у слоёв в facilityFilters. */
export function matchesFacilityFilter(s: MapSearch, p: { type?: unknown; confidence?: unknown; mkad_min?: unknown }) {
  const types = s.ft ? s.ft.split(",").filter(Boolean) : [];
  if (types.length > 0 && !types.includes(String(p.type))) {
    return false;
  }
  if (s.fc !== undefined && (typeof p.confidence === "number" ? p.confidence : 0) < s.fc) {
    return false;
  }
  return !(s.fm !== undefined && (typeof p.mkad_min === "number" ? p.mkad_min : 100000) > s.fm);
}

/** Есть ли фильтр объектов из реестра. */
export function hasFacilityFilter(s: MapSearch): boolean {
  return Boolean(s.ft || s.fc !== undefined || s.fm !== undefined);
}

/**
 * Фильтр слоёв объектов по параметрам реестра (типы, достоверность, время до МКАД); контуры
 * объектов — только по типу (других свойств в их тайлах нет).
 */
export function facilityFilters(s: MapSearch): Record<string, ExpressionSpecification> {
  if (!hasFacilityFilter(s)) {
    return {};
  }
  const types = s.ft ? s.ft.split(",").filter(Boolean) : [];
  const byType: ExpressionSpecification[] = types.length > 0 ? [["in", ["get", "type"], ["literal", types]]] : [];
  const points: ExpressionSpecification[] = [...byType];
  if (s.fc !== undefined) {
    points.push([">=", ["coalesce", ["get", "confidence"], 0], s.fc]);
  }
  if (s.fm !== undefined) {
    points.push(["<=", ["coalesce", ["get", "mkad_min"], 100000], s.fm]);
  }
  const out: Record<string, ExpressionSpecification> = { facilities: ["all", true, ...points] };
  if (byType.length > 0) {
    out.facility_areas = ["all", true, ...byType];
  }
  return out;
}
