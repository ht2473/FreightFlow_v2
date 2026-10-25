import {
  BookMarked,
  Bookmark,
  Building2,
  Construction,
  Database,
  FileText,
  Landmark,
  type LucideIcon,
  MapPin,
  Route,
  Scale,
} from "lucide-react";
import type { Schemas } from "@/shared/api/client";

export type Hit = Schemas["SearchHit"];
export type Kind = Hit["kind"];
export type Parsed = Schemas["ParsedQuery"];

/** Подписи групп результатов в порядке показа. */
export const kindTitles: Record<Kind, string> = {
  facility: "Объекты",
  territory: "Территории",
  street: "Улицы",
  address: "Адреса",
  road_work: "Дорожные работы",
  legal_act: "Правовые акты",
  saved_view: "Сохранённые виды",
  dataset: "Наборы данных",
  source: "Источники",
  help: "Справка",
  report: "Отчёты",
};

export const kindIcons: Record<Kind, LucideIcon> = {
  facility: Building2,
  territory: Landmark,
  street: Route,
  address: MapPin,
  road_work: Construction,
  legal_act: Scale,
  saved_view: Bookmark,
  dataset: Database,
  source: Database,
  help: BookMarked,
  report: FileText,
};

export const kindOrder = Object.keys(kindTitles) as Kind[];

/** Документы по видам в порядке показа. */
export function groupHits(hits: Hit[]): { kind: Kind; hits: Hit[] }[] {
  return kindOrder
    .map((kind) => ({ kind, hits: hits.filter((h) => h.kind === kind) }))
    .filter((g) => g.hits.length > 0);
}

/** Переход к найденному: адрес страницы и параметры. Сохранённый вид открывается отдельно. */
export type Target =
  | { to: "/facilities/$facilityId"; params: { facilityId: string } }
  | { to: "/territories/$territoryId"; params: { territoryId: string } }
  | { to: "/map"; search: { c: string; z: number; pin: string; pt: string; l?: string } }
  | { to: "/regulation" }
  | { to: "/data"; search: { source: string } };

const lnglat = (h: Hit) => (h.point ? `${h.point.lon.toFixed(5)},${h.point.lat.toFixed(5)}` : undefined);

export function hitTarget(h: Hit): Target | undefined {
  switch (h.kind) {
    case "facility":
      return { to: "/facilities/$facilityId", params: { facilityId: h.ref } };
    case "territory":
      return { to: "/territories/$territoryId", params: { territoryId: h.ref } };
    case "address":
    case "street":
    case "road_work": {
      const c = lnglat(h);
      if (!c) {
        return undefined;
      }
      const z = h.kind === "street" ? 15 : 17;
      const base = { c, z, pin: c, pt: h.title };
      return { to: "/map", search: h.kind === "road_work" ? { ...base, l: "road_works,facilities" } : base };
    }
    case "legal_act":
      return { to: "/regulation" };
    case "dataset":
    case "source":
      return { to: "/data", search: { source: h.ref } };
    default:
      return undefined;
  }
}

/** Параметры страницы реестра по разобранному запросу (и точке «рядом с …»). */
export function registrySearch(p: Parsed, near?: Hit) {
  const list = (v: string[]) => (v.length > 0 ? v.join(",") : undefined);
  return {
    q: p.text || undefined,
    type: list(p.types),
    okrug: list(p.okrugs),
    district: list(p.districts),
    ring: list(p.rings),
    conf: p.minConfidence !== undefined ? ("high" as const) : undefined,
    restr: p.nearRestrictions === undefined ? undefined : p.nearRestrictions ? ("yes" as const) : ("no" as const),
    permit: p.hasPermit ? ("yes" as const) : undefined,
    mkad: p.maxMkadMin,
    amin: p.minArea,
    amax: p.maxArea,
    near:
      near?.point && p.nearKm
        ? `${near.point.lon.toFixed(5)},${near.point.lat.toFixed(5)},${Math.round(p.nearKm * 1000)}`
        : undefined,
  };
}

/** Недавнее в палитре: открытые результаты и запросы (только в этом браузере). */
export interface Recent {
  q?: string;
  hit?: Hit;
}

const recentKey = "ff.search.recent";

export function loadRecent(): Recent[] {
  try {
    return (JSON.parse(localStorage.getItem(recentKey) ?? "[]") as Recent[]).slice(0, 8);
  } catch {
    return [];
  }
}

export function pushRecent(r: Recent) {
  const same = (a: Recent) => (r.hit ? a.hit?.kind === r.hit.kind && a.hit?.ref === r.hit.ref : a.q === r.q);
  try {
    localStorage.setItem(recentKey, JSON.stringify([r, ...loadRecent().filter((x) => !same(x))].slice(0, 8)));
  } catch {
    // Без хранилища недавнее не запоминается.
  }
}
