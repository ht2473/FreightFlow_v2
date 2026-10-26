import { useQueryClient } from "@tanstack/react-query";
import { useServerEvent } from "@/shared/api/events";

interface ScenarioEvent {
  kind?: string;
  id?: string;
}

/** Запросы сценариев перечитываются по событиям хода расчёта и готовности. */
export function useScenarioEvents() {
  const qc = useQueryClient();
  const refresh = (data: unknown) => {
    const e = data as ScenarioEvent;
    if (e.kind !== undefined && e.kind !== "scenario") {
      return;
    }
    void qc.invalidateQueries({
      predicate: (q) => typeof q.queryKey[1] === "string" && q.queryKey[1].startsWith("/scenarios"),
    });
  };
  useServerEvent("job.progress", refresh);
  useServerEvent("scenario.computed", refresh);
  useServerEvent("reconnected", refresh);
}
