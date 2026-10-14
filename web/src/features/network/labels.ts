import type { Schemas } from "@/shared/api/client";
import { num } from "@/shared/format";
import { formatCenter } from "../map/state";

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

/** Предел узкого места: «Высота до 3,5 м»; без величины — «Высота». */
export function limitText(kind: string, value: number | undefined): string {
  const label = restrictionLabel[kind] ?? kind;
  return value !== undefined && units[kind] ? `${label} ${num(value, 1)} ${units[kind]}` : label.replace(/ до$/, "");
}

/** Дополнительное время объезда: «+4,5 мин», «+12 мин»; без значения — объезда нет. */
export function detourText(minutes: number | undefined): string {
  if (minutes === undefined) {
    return "нет объезда";
  }
  if (minutes < 0.5) {
    return "менее 1 мин";
  }
  return `+${num(minutes, minutes < 10 ? 1 : 0)} мин`;
}

/** Точность оценки значимости для подписи. */
export function accuracyText(a: Schemas["CentralityAccuracy"], nodes: number, prefix = "Оценка"): string {
  if (a.exact) {
    return `${prefix}: точный расчёт по всем ${num(nodes)} узлам.`;
  }
  const parts = [`${prefix}: ${num(a.sources)} узлов-источников из ${num(nodes)}`];
  if (a.rse !== undefined) {
    parts.push(`медиана относительной ошибки верхней сотни участков — ${num(a.rse * 100, 1)} %`);
  }
  if (a.splitHalf !== undefined) {
    parts.push(`согласованность рейтингов двух половин выборки (Спирмен) — ${num(a.splitHalf, 2)}`);
  }
  return `${parts.join("; ")}.`;
}

/** Центр карты для ссылки «на карте». */
export function mapCenter(p: Schemas["LngLat"]): string {
  return formatCenter(p.lon, p.lat);
}

const elementKind: Record<Schemas["CriticalElement"]["kind"], string> = {
  bridge: "мост, путепровод",
  segment: "участок дороги",
};

/** Вид элемента и число элементов той же дороги с тем же эффектом: «мост, путепровод; таких 4». */
export function elementLabel(kind: Schemas["CriticalElement"]["kind"], same: number): string {
  return same > 1 ? `${elementKind[kind]}; таких ${num(same)}` : elementKind[kind];
}

/** Число линий улицы с тем же пределом: «3 участка». */
export function segmentsLabel(n: number): string {
  return `${num(n)} ${plural(n, "участок", "участка", "участков")}`;
}

/** Итог проверки элемента матрицами: «12 объектов, +340 объекто-мин» или «без роста времени». */
export function damageText(d: Schemas["ElementDamage"]): string {
  const parts: string[] = [];
  if (d.facilities > 0) {
    parts.push(
      `${num(d.facilities)} ${plural(d.facilities, "объект", "объекта", "объектов")}, +${num(d.minutes)} объекто-мин`,
    );
  }
  if (d.disconnected > 0) {
    parts.push(`без доступа ${num(d.disconnected)}`);
  }
  return parts.length > 0 ? parts.join("; ") : "без роста времени";
}

function plural(n: number, one: string, few: string, many: string): string {
  const m10 = n % 10;
  const m100 = n % 100;
  if (m10 === 1 && m100 !== 11) {
    return one;
  }
  if (m10 >= 2 && m10 <= 4 && (m100 < 12 || m100 > 14)) {
    return few;
  }
  return many;
}
