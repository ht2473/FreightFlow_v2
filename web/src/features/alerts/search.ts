/** Состояние раздела алертов в адресе: вкладка и открытый алерт. */
export interface AlertsSearch {
  tab?: "feed" | "rules";
  alert?: string;
}

export function alertsSearchSchema(search: Record<string, unknown>): AlertsSearch {
  return {
    tab: search.tab === "rules" ? "rules" : undefined,
    alert: typeof search.alert === "string" ? search.alert : undefined,
  };
}
