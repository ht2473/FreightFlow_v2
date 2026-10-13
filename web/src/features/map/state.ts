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
