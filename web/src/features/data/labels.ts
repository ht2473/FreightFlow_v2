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
