import { z } from "zod";
import { parseCenter } from "../map/state";

/**
 * Параметры страницы маршрутов: режим, начальная точка «долгота,широта» и её название
 * (переход из карточки объекта: «Маршрут отсюда», «Изохроны»).
 */
export const networkSearchSchema = z.object({
  mode: z.enum(["route", "isochrone"]).optional().catch(undefined),
  from: z.string().optional().catch(undefined),
  name: z.string().max(300).optional().catch(undefined),
});

export type NetworkSearch = z.infer<typeof networkSearchSchema>;

/** Начальная точка из адреса или null. */
export function startPoint(s: NetworkSearch): [number, number] | null {
  return parseCenter(s.from);
}
