import type { Schemas } from "@/shared/api/client";
import { num } from "@/shared/format";

export const ringLabel: Record<string, string> = {
  sk: "внутри Садового кольца",
  ttk: "Садовое — ТТК",
  mkad: "ТТК — МКАД",
  ckad: "МКАД — ЦКАД",
  outside: "за ЦКАД",
};

/** Короткие подписи целей для заголовков таблицы. */
export const targetShort: Record<Schemas["FacilityAccess"]["target"], string> = {
  mkad: "МКАД",
  ttk: "ТТК",
  center: "Центр",
  rail_freight: "Ж/д",
};

export const poiLabel: Record<string, string> = {
  fuel: "АЗС",
  truck_parking: "Стоянка грузовиков",
  rest_area: "Площадка отдыха",
  marketplace: "Рынок",
  rail_station: "Вокзал",
  rail_freight: "Ж/д грузовой пункт",
  customs: "Таможня",
  port: "Порт",
};

export const workKindLabel: Record<Schemas["NearbyWork"]["kind"], string> = {
  repair: "Плановый ремонт",
  emergency: "Аварийный ремонт",
  closure: "Ограничение движения",
};

export const sourceLabel: Record<Schemas["FacilitySource"]["relation"], string> = {
  address: "Адресный реестр Москвы",
  permit: "Разрешение на ввод в эксплуатацию",
};

/** Площадь: «12 400 м²», от 10 га — в гектарах. */
export function area(m2: number | undefined): string {
  if (m2 === undefined) {
    return "—";
  }
  return m2 >= 100_000 ? `${num(m2 / 10_000, 1)} га` : `${num(m2)} м²`;
}

/** Расстояние: «350 м», «2,4 км». */
export function distance(m: number): string {
  return m < 1000 ? `${num(Math.round(m / 10) * 10)} м` : `${num(m / 1000, 1)} км`;
}

/** Достоверность записи — уровень по порогам фасета. */
export function confidenceLevel(c: number): { label: string; tone: "ok" | "warn" | "neutral" } {
  if (c >= 0.7) {
    return { label: "высокая", tone: "ok" };
  }
  if (c >= 0.4) {
    return { label: "средняя", tone: "warn" };
  }
  return { label: "низкая", tone: "neutral" };
}

/** Ссылка на объект в OpenStreetMap. */
export function osmUrl(type: Schemas["FacilityCard"]["osmType"], id: number): string {
  const kind = { N: "node", W: "way", R: "relation" }[type];
  return `https://www.openstreetmap.org/${kind}/${id}`;
}
