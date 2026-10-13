import type { Schemas } from "@/shared/api/client";
import { num } from "@/shared/format";

type Zone = Schemas["ZoneVerdict"]["zone"];

export const zoneName: Record<Zone, string> = {
  mkad: "МКАД",
  ttk: "ТТК",
  sk: "Садовое кольцо",
};

/** Зоны пропусков в порядке от внешней к внутренней. */
export const passZones: Zone[] = ["mkad", "ttk", "sk"];

export const passZoneLabel: Record<Zone, string> = { mkad: "«МКАД»", ttk: "«ТТК»", sk: "«СК»" };

export const restrictionLabel: Record<string, string> = {
  hgv_ban: "Запрет грузовых",
  motor_vehicle_ban: "Запрет движения",
  hazmat_ban: "Запрет опасных грузов",
  maxweight: "Масса до",
  maxaxleload: "Нагрузка на ось до",
  maxheight: "Высота до",
  maxwidth: "Ширина до",
  maxlength: "Длина до",
};

const units: Record<string, string> = {
  maxweight: "т",
  maxaxleload: "т",
  maxheight: "м",
  maxwidth: "м",
  maxlength: "м",
};

/** Ограничение для перечня обойдённого: «Высота до 3,5 м». */
export function restrictionText(r: Schemas["AvoidedRestriction"]): string {
  const label = restrictionLabel[r.kind] ?? r.kind;
  return r.value !== undefined && units[r.kind] ? `${label} ${num(r.value, 1)} ${units[r.kind]}` : label;
}

/** Длительность: «1 ч 05 мин», «26 мин». */
export function duration(seconds: number): string {
  const total = Math.round(seconds / 60);
  if (total < 60) {
    return `${total} мин`;
  }
  return `${Math.floor(total / 60)} ч ${String(total % 60).padStart(2, "0")} мин`;
}

/** Разница маршрута грузовика с путём без ограничений: «+8 мин, +7,7 км». */
export function difference(truck: Schemas["RouteLine"], base: Schemas["RouteLine"]): string {
  const dt = Math.round((truck.timeS - base.timeS) / 60);
  const dl = truck.lengthKm - base.lengthKm;
  const sign = (v: number) => (v > 0 ? "+" : v < 0 ? "−" : "");
  return `${sign(dt)}${Math.abs(dt)} мин, ${sign(dl)}${num(Math.abs(dl), 1)} км`;
}

/** Подпись точки маршрута: A, B, затем промежуточные цифрами. */
export function pointLabel(index: number, count: number): string {
  if (index === 0) {
    return "A";
  }
  if (index === count - 1) {
    return "B";
  }
  return String(index);
}

/** Правило, закрывшее зону: что нужно, чтобы проехать. */
export function ruleHint(r: Schemas["ZoneRuleUse"]): string {
  if (r.note === "eco_unknown") {
    return `${r.title} — экологический класс не указан`;
  }
  return r.title;
}
