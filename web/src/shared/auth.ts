import { $api, type Schemas } from "./api/client";

export type Me = Schemas["Me"];

/** Права системы (совпадают с internal/identity/permission.go). */
export type Permission =
  | "map.view"
  | "analytics.view"
  | "reports.view"
  | "routing.use"
  | "views.manage"
  | "exports.create"
  | "reports.create"
  | "scenarios.manage"
  | "alerts.own"
  | "alerts.manage"
  | "registry.curate"
  | "data.manage"
  | "tokens.own"
  | "users.manage"
  | "settings.manage"
  | "audit.view";

/** Текущий пользователь. Данные кешируются на время сеанса. */
export function useMe() {
  return $api.useQuery("get", "/me", {}, { staleTime: 5 * 60_000 });
}

/** Есть ли у пользователя право. */
export function can(me: Me | undefined, perm: Permission): boolean {
  return Boolean(me?.permissions.includes(perm));
}

/** Инициалы для аватара. */
export function initials(name: string): string {
  const parts = name.trim().split(/\s+/);
  const first = parts[0]?.[0] ?? "";
  const second = parts[1]?.[0] ?? "";
  return (first + second).toUpperCase() || "?";
}
