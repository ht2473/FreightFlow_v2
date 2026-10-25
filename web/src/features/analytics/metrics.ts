import { useMemo } from "react";
import { $api, type Schemas } from "@/shared/api/client";
import { num } from "@/shared/format";

export type MetricDef = Schemas["MetricDef"];
export type Level = MetricDef["levels"][number];

export const levelTitles: Record<Level, string> = {
  facility: "Объекты",
  district: "Районы",
  okrug: "Округа",
  segment: "Участки сети",
  city: "Москва",
  h3_8: "Ячейки H3 (≈ 0,7 км²)",
  h3_9: "Ячейки H3 (≈ 0,1 км²)",
};

/** Каталог показателей: по коду. */
export function useMetrics() {
  const q = $api.useQuery("get", "/metrics", {}, { staleTime: 60_000 });
  const byCode = useMemo(() => new Map((q.data ?? []).map((m) => [m.code, m])), [q.data]);
  return { ...q, byCode };
}

/** Значение с единицей: малые величины — с дробной частью. */
export function metricValue(v: number | undefined | null, def?: Pick<MetricDef, "unit">, withUnit = true): string {
  if (v === undefined || v === null || !Number.isFinite(v)) {
    return "—";
  }
  const abs = Math.abs(v);
  const digits = abs >= 100 ? 0 : abs >= 10 ? 1 : 2;
  const unit = withUnit && def?.unit && def.unit !== "0–1" && def.unit !== "в единицах набора" ? ` ${def.unit}` : "";
  return `${num(v, digits)}${unit}`;
}

/** Подпись периода ряда: «10.2026» или «2025». */
export function periodLabel(date: string, yearly: boolean): string {
  const [y, m] = date.split("-");
  return yearly ? (y ?? date) : `${m}.${y}`;
}

/** Ряд показателя годовой (по единице периода — у годовых рядов период — 1 января). */
export function isYearly(points: { period: string }[]): boolean {
  return points.length > 1 && points.every((p) => p.period.endsWith("-01-01"));
}

/** Названия моделей прогноза. */
export const modelTitles: Record<string, string> = {
  seasonal_naive: "сезонная наивная",
  holt_winters: "Холт–Уинтерс",
  regression: "регрессия с календарём",
};
