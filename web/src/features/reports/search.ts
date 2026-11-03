/** Состояние раздела отчётов в адресе: вкладка, выделенный отчёт, шаблон и его параметры. */
export interface ReportsSearch {
  tab?: "reports" | "schedules";
  report?: string;
  template?: string;
  [param: string]: string | undefined;
}

export function reportsSearchSchema(search: Record<string, unknown>): ReportsSearch {
  const out: ReportsSearch = {};
  for (const [k, v] of Object.entries(search)) {
    if (typeof v === "string" || typeof v === "number") {
      out[k] = String(v);
    }
  }
  if (out.tab !== "schedules") {
    out.tab = undefined;
  }
  return out;
}
