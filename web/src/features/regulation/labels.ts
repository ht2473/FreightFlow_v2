import type { Schemas } from "@/shared/api/client";
import { date, num } from "@/shared/format";

type Rule = Schemas["ZoneRule"];
type Doc = Schemas["LegalDocument"];
type Tone = "ok" | "warn" | "crit" | "info" | "neutral";

export const zoneTitle: Record<Rule["zone"], string> = {
  sk: "Садовое кольцо",
  ttk: "Третье транспортное кольцо",
  mkad: "МКАД",
};

/** Пояснение зоны: правило действует внутри кольца и на нём самом. */
export const zoneScope: Record<Rule["zone"], string> = {
  sk: "В пределах Садового кольца и на нём",
  ttk: "В пределах ТТК и на ТТК",
  mkad: "В пределах МКАД и на МКАД",
};

export const effectLabel: Record<Rule["effect"], { label: string; tone: Tone }> = {
  prohibited: { label: "Запрет въезда", tone: "crit" },
  pass_required: { label: "Нужен пропуск", tone: "warn" },
  frame_only: { label: "Только по каркасу", tone: "info" },
};

const passZone: Record<string, string> = { mkad: "«МКАД»", ttk: "«ТТК»", sk: "«СК»" };

function orList(items: string[]): string {
  if (items.length <= 1) {
    return items.join("");
  }
  return `${items.slice(0, -1).join(", ")} или ${items[items.length - 1]}`;
}

/** Кого и когда касается правило: «РММ более 3,5 т · 07:00–23:00». */
export function ruleSubject(r: Rule): string {
  const parts: string[] = [];
  if (r.massOverT !== undefined) {
    parts.push(`${r.massBasis === "payload" ? "грузоподъёмность" : "РММ"} более ${num(r.massOverT, 1)} т`);
  }
  if (r.ecoClassMin !== undefined) {
    parts.push(`экологический класс ниже ${r.ecoClassMin}`);
  }
  parts.push(r.timeFrom && r.timeTo ? `${r.timeFrom}–${r.timeTo}` : "круглосуточно");
  return parts.join(" · ");
}

/** Что требуется: вид пропуска и зоны, дающие право проезда. */
export function ruleRequirement(r: Rule): string | null {
  if (r.effect !== "pass_required") {
    return null;
  }
  const kind = r.passKind === "night" ? "ночной" : "дневной";
  const zones = orList(r.passZones.map((z) => passZone[z] ?? z));
  return `${kind} пропуск зоны ${zones}`;
}

/** Правовое основание: «379-ПП, п. 2.4 (ред. 399-ПП)». */
export function ruleBasis(r: Rule, docs: Map<string, Doc>): string {
  const d = docs.get(r.document);
  const name = d?.number ?? d?.title ?? r.document;
  return r.clause ? `${name}, ${r.clause}` : name;
}

/** Короткое имя документа: номер акта или адрес сайта официальной страницы. */
export function docShort(d: Doc): string {
  if (d.number) {
    return d.number;
  }
  if (d.url) {
    try {
      return new URL(d.url).hostname;
    } catch {
      // Адрес не разобран — название документа.
    }
  }
  return d.title;
}

/** Реквизиты документа: «№ 379-ПП от 22.08.2011». */
export function docRequisites(d: Doc): string {
  const parts: string[] = [];
  if (d.number) {
    parts.push(`№ ${d.number}`);
  }
  if (d.signedOn) {
    parts.push(`от ${date(d.signedOn)}`);
  }
  return parts.join(" ") || "официальная страница";
}

export const docStatus: Record<Doc["status"], { label: string; tone: Tone }> = {
  in_force: { label: "действует", tone: "ok" },
  repealed: { label: "утратил силу", tone: "neutral" },
  info: { label: "разъяснение", tone: "info" },
};

/** Месяц для оси графика: «05.2021». */
export function monthLabel(iso: string): string {
  const [y, m] = iso.split("-");
  return `${m}.${y}`;
}

/** Все месяцы от первого до последнего включительно (ISO-даты первого числа). */
export function monthRange(first: string, last: string): string[] {
  const out: string[] = [];
  let [y, m] = first.split("-").map(Number) as [number, number];
  const [ly, lm] = last.split("-").map(Number) as [number, number];
  while (y < ly || (y === ly && m <= lm)) {
    out.push(`${y}-${String(m).padStart(2, "0")}-01`);
    m += 1;
    if (m > 12) {
      m = 1;
      y += 1;
    }
  }
  return out;
}

/** Виды ограничений по знакам и OSM. */
export const restrictionKind: Record<string, string> = {
  hgv_ban: "запрет грузовых",
  motor_vehicle_ban: "запрет движения",
  hazmat_ban: "опасные грузы",
  maxweight: "масса",
  maxaxleload: "нагрузка на ось",
  maxheight: "высота",
  maxwidth: "ширина",
  maxlength: "длина",
};

export const conflictLabel: Record<Schemas["ConflictCount"]["type"], string> = {
  sign_not_in_osm: "Знак есть, в OSM ограничения нет",
  osm_not_signed: "В OSM есть, знака рядом нет",
};
