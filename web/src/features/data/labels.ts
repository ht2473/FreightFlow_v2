import type { Schemas } from "@/shared/api/client";

type Tone = "ok" | "warn" | "crit" | "info" | "neutral";
type RunStatus = Schemas["Run"]["status"];
type Freshness = Schemas["SourceInfo"]["freshness"];

export const runStatus: Record<RunStatus, { label: string; tone: Tone }> = {
  queued: { label: "В очереди", tone: "neutral" },
  running: { label: "Выполняется", tone: "info" },
  succeeded: { label: "Успешно", tone: "ok" },
  unchanged: { label: "Без изменений", tone: "ok" },
  failed: { label: "Ошибка", tone: "crit" },
};

export const freshnessLabel: Record<Freshness, { label: string; tone: Tone }> = {
  fresh: { label: "Актуальны", tone: "ok" },
  stale: { label: "Устарели", tone: "warn" },
  never: { label: "Не загружались", tone: "neutral" },
};

export const triggerLabel: Record<Schemas["Run"]["trigger"], string> = {
  schedule: "По расписанию",
  manual: "Вручную",
  cli: "Командой",
};

export const severityLabel: Record<Schemas["QualityCheck"]["severity"], { label: string; tone: Tone }> = {
  info: { label: "Сведения", tone: "info" },
  warning: { label: "Предупреждение", tone: "warn" },
  critical: { label: "Критично", tone: "crit" },
};

/** Домены данных, которые обновляет источник. */
export const domainLabel: Record<string, string> = {
  territory: "территории",
  facility: "объекты",
  infra: "инфраструктура",
  network: "дорожная сеть",
  operations: "оперативная обстановка",
  regulation: "регулирование",
  statistics: "статистика",
  basemap: "подложка карты",
  reference: "справочники",
};

/** Подписи счётчиков загрузки. */
export const statLabel: Record<string, string> = {
  records: "Записей",
  inserted: "Добавлено",
  updated: "Обновлено",
  removed: "Исчезло",
  unchanged: "Без изменений",
  facilities_new: "Новых объектов",
  facilities_gone: "Исчезло объектов",
  regions: "Регионов",
  okrugs: "Округов",
  districts: "Районов и поселений",
  municipal: "Городских округов области",
  rings: "Колец",
  industrial_zones: "Промзон",
  poi: "Точек инфраструктуры",
  roads: "Участков дорог",
  restrictions: "Ограничений",
  bridges: "Мостов",
  // Портал открытых данных Москвы
  addresses: "Адресов",
  addresses_new: "Новых адресов",
  addresses_with_key: "Адресов с ключом для поиска",
  addresses_without_geometry: "Адресов без координат",
  addresses_without_geometry_pct: "Адресов без координат, %",
  assets_active: "Объектов инфраструктуры",
  assets_new: "Новых объектов инфраструктуры",
  assets_missing: "Исчезло объектов инфраструктуры",
  bridges_registry: "Мостов в реестре",
  bridges_matched_osm: "Мостов сопоставлено с OSM",
  bridges_without_geometry: "Мостов без геометрии",
  poi_official: "Официальных точек",
  poi_new: "Новых точек",
  poi_matched_osm: "Точек сопоставлено с OSM",
  signs_active: "Знаков в реестре",
  signs_new: "Новых знаков",
  signs_missing: "Исчезло знаков",
  signs_unrecognized: "Знаков без кода",
  sign_types: "Видов знаков",
  restriction_signs: "Знаков ограничений",
  signs_matched: "Знаков привязано к сети",
  signs_match_pct: "Знаков ограничений привязано, %",
  signs_unmatched: "Знаков не привязано",
  sign_restrictions: "Ограничений по знакам",
  conflicts_sign_not_in_osm: "Знак есть, в OSM нет",
  conflicts_osm_not_signed: "В OSM есть, знака нет",
  sign_works: "Работ на знаках",
  sign_works_new: "Новых работ на знаках",
  works: "Работ",
  works_active: "Работ идёт",
  works_planned: "Работ запланировано",
  works_appeared: "Работ появилось",
  works_changed: "Работ изменилось",
  works_removed: "Работ снято",
  works_high: "Работ высокой значимости",
  works_without_geometry: "Работ без геометрии",
  works_without_geometry_pct: "Работ без геометрии, %",
  state_transitions: "Смен состояния",
  passes: "Пропусков",
  passes_valid_now: "Действующих пропусков",
  series: "Рядов",
  months: "Месяцев",
  permits: "Разрешений",
  permits_new: "Новых разрешений",
  permits_valid_now: "Действующих разрешений",
  permits_located: "Разрешений с местом",
  permits_logistics: "Складских и логистических",
  permits_logistics_located: "Складских с местом",
  permits_matched_facility: "Сопоставлено с объектами",
  zones: "Зон",
  zones_area_ha: "Площадь зон, га",
  // Статистика
  areas: "Территорий",
  areas_matched: "Территорий сопоставлено",
  observations: "Значений",
  indicators: "Показателей",
  first_year: "Первый год",
  last_year: "Последний год",
  moscow_last_year: "Последний год по Москве",
  population_jan1: "Население на 1 января",
  // Дептранс
  streets: "Строк перечня",
  streets_partial: "Строк с участком улицы",
  streets_matched: "Улиц найдено в сети",
  streets_matched_pct: "Улиц найдено в сети, %",
  okrugs_unmatched: "Округов не найдено",
  frame_segments: "Участков каркаса",
  frame_km: "Протяжённость каркаса, км",
  pages: "Страниц",
  pages_changed: "Страниц изменилось после сверки",
  // ДТП
  incidents: "ДТП",
  incidents_fatal: "ДТП с погибшими",
  incidents_freight: "ДТП с грузовым транспортом",
  incidents_heavy_freight: "ДТП с грузовиками (без фургонов)",
  incidents_outside_moscow: "ДТП вне границ Москвы",
  incidents_without_point: "ДТП без координат",
  // Производственный календарь
  years: "Лет",
  days: "Дней",
  days_workday: "Рабочих дней",
  days_dayoff: "Выходных и праздничных",
  days_short: "Сокращённых дней",
  days_nonworking_paid: "Нерабочих оплачиваемых",
};

/** Период расписания по-русски: «каждые 24 ч», «раз в неделю». */
export function scheduleLabel(seconds: number | undefined): string {
  if (!seconds) {
    return "Вручную";
  }
  const h = seconds / 3600;
  if (h === 168) {
    return "Раз в неделю";
  }
  if (h === 24) {
    return "Раз в сутки";
  }
  if (h >= 1 && Number.isInteger(h)) {
    return `Каждые ${h} ч`;
  }
  return `Каждые ${Math.round(seconds / 60)} мин`;
}

/** Длительность запуска: «3 мин 12 с». */
export function duration(from?: string, to?: string): string {
  if (!from || !to) {
    return "—";
  }
  const s = Math.max(0, Math.round((new Date(to).getTime() - new Date(from).getTime()) / 1000));
  if (s < 60) {
    return `${s} с`;
  }
  const m = Math.floor(s / 60);
  if (m < 60) {
    return `${m} мин ${s % 60} с`;
  }
  return `${Math.floor(m / 60)} ч ${m % 60} мин`;
}

export function isActive(status?: RunStatus): boolean {
  return status === "queued" || status === "running";
}
