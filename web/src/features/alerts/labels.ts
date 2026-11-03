import type { Schemas } from "@/shared/api/client";

export type Alert = Schemas["Alert"];
export type AlertRule = Schemas["AlertRule"];
export type RuleKind = Schemas["AlertRuleKind"];
export type Severity = Schemas["AlertSeverity"];
export type Status = Schemas["AlertStatus"];
export type Channel = Schemas["AlertChannel"];
export type ScopeType = Schemas["AlertScope"]["type"];

export const severityTitle: Record<Severity, string> = {
  info: "Информация",
  warning: "Предупреждение",
  critical: "Критично",
};

export const severityTone: Record<Severity, "info" | "warn" | "crit"> = {
  info: "info",
  warning: "warn",
  critical: "crit",
};

export const statusTitle: Record<Status, string> = {
  active: "Активен",
  acknowledged: "Подтверждён",
  closed: "Закрыт",
};

export const statusTone: Record<Status, "crit" | "warn" | "neutral"> = {
  active: "crit",
  acknowledged: "warn",
  closed: "neutral",
};

export const kindTitle: Record<RuleKind, string> = {
  roadworks: "Дорожные работы",
  restrictions: "Ограничения",
  accessibility: "Доступность",
  facilities: "Новые объекты",
  sources: "Источники данных",
};

export const scopeTitle: Record<ScopeType, string> = {
  corridor: "Коридор",
  facilities: "У объектов",
  area: "Область",
  city: "Вся Москва",
  none: "Без области",
};

export const channelTitle: Record<Channel, string> = {
  in_app: "В системе",
  email: "Почта",
  telegram: "Telegram",
  webhook: "Вебхук",
};

export const channels: Channel[] = ["in_app", "email", "telegram", "webhook"];

export const actionTitle: Record<Schemas["AlertLogEntry"]["action"], string> = {
  created: "Создан",
  repeated: "Повторное срабатывание",
  acknowledged: "Подтверждён",
  closed: "Закрыт",
  reopened: "Открыт заново",
  assigned: "Назначен",
  commented: "Комментарий",
  resolved: "Закрыт автоматически: причина исчезла",
};

/** Описание области правила для списка. */
export function scopeSummary(r: AlertRule): string {
  const sc = r.scope;
  switch (sc.type) {
    case "corridor":
      return `Коридор ±${sc.bufferM ?? 200} м`;
    case "facilities":
      return `${sc.facilityIds?.length ?? 0} об. в радиусе ${sc.radiusM ?? 500} м`;
    default:
      return scopeTitle[sc.type];
  }
}
