import type { Schemas } from "@/shared/api/client";
import { num, plural } from "@/shared/format";

export type Scenario = Schemas["Scenario"];
export type TargetKind = Schemas["ScenarioTargetKind"];
export type Params = Schemas["ScenarioParams"];
export type Closure = Schemas["ScenarioClosure"];

/** Цели доступности в порядке расчёта. */
export const targetKinds: TargetKind[] = ["mkad", "ttk", "center", "rail_freight"];

export const targetTitle: Record<TargetKind, string> = {
  mkad: "До МКАД",
  ttk: "До ТТК",
  center: "До центра",
  rail_freight: "До ж/д грузовых пунктов",
};

export const statusTitle: Record<Scenario["status"], string> = {
  queued: "В очереди",
  running: "Считается",
  done: "Готов",
  failed: "Ошибка",
};

export const statusTone: Record<Scenario["status"], "neutral" | "info" | "ok" | "crit"> = {
  queued: "neutral",
  running: "info",
  done: "ok",
  failed: "crit",
};

export const closureKindTitle: Record<Closure["kind"], string> = {
  segments: "Участки",
  element: "Мост или участок рейтинга",
  area: "Область",
};

export const reachTitle: Record<Schemas["ScenarioReach"], string> = {
  ok: "доступен",
  unreachable: "нет пути",
  blocked: "в закрытой зоне",
};

/** Сценарий ещё считается: результаты обновятся по событию. */
export const isActive = (s: Scenario["status"]) => s === "queued" || s === "running";

/** Время в минутах с одним знаком или статус недоступности. */
export function minutesText(v: number | undefined, status: Schemas["ScenarioReach"]): string {
  return status === "ok" && v !== undefined ? `${num(v, 1)} мин` : reachTitle[status];
}

/** Разность времени: «+3,2 мин», «−1,0 мин», потеря и получение доступа. */
export function deltaText(
  f: Pick<Schemas["ScenarioFacility"], "baseMin" | "scenarioMin" | "baseStatus" | "scenarioStatus">,
) {
  if (f.baseStatus === "ok" && f.scenarioStatus !== "ok") {
    return "потерял доступ";
  }
  if (f.baseStatus !== "ok" && f.scenarioStatus === "ok") {
    return "получил доступ";
  }
  if (f.baseMin === undefined || f.scenarioMin === undefined) {
    return "—";
  }
  const d = f.scenarioMin - f.baseMin;
  return `${d > 0 ? "+" : d < 0 ? "−" : ""}${num(Math.abs(d), 1)} мин`;
}

/** Разность для раскраски: прирост минут, потеря доступа — бесконечность, получение — минус. */
export function deltaValue(
  f: Pick<Schemas["ScenarioFacility"], "baseMin" | "scenarioMin" | "baseStatus" | "scenarioStatus">,
) {
  if (f.baseStatus === "ok" && f.scenarioStatus !== "ok") {
    return Number.POSITIVE_INFINITY;
  }
  if (f.baseStatus !== "ok" && f.scenarioStatus === "ok") {
    return Number.NEGATIVE_INFINITY;
  }
  return (f.scenarioMin ?? 0) - (f.baseMin ?? 0);
}

/** Краткое описание изменений сценария для списка. */
export function paramsSummary(p: Params): string {
  const parts: string[] = [];
  const n = p.closures?.length ?? 0;
  if (n > 0) {
    parts.push(plural(n, "перекрытие", "перекрытия", "перекрытий"));
  }
  const zones = Object.keys(p.zoneOverrides ?? {}).length;
  if (zones > 0) {
    parts.push(`правила ${plural(zones, "зоны", "зон", "зон")}`);
  }
  if (p.newFacility) {
    parts.push("новый объект");
  }
  return parts.join(", ");
}

/** Прирост площади изохроны в процентах (отрицательный — сокращение). */
export function areaChange(base: number, scenario: number): string {
  if (base <= 0) {
    return "—";
  }
  const d = ((scenario - base) / base) * 100;
  return `${d > 0 ? "+" : d < 0 ? "−" : ""}${num(Math.abs(d), 1)} %`;
}
