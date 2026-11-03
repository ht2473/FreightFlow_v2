import { useQueryClient } from "@tanstack/react-query";
import { useServerEvent } from "@/shared/api/events";

/** Лента, сводка и правила перечитываются по событиям алертов. */
export function useAlertEvents() {
  const qc = useQueryClient();
  const refresh = () => {
    void qc.invalidateQueries({
      predicate: (q) =>
        typeof q.queryKey[1] === "string" && (q.queryKey[1].startsWith("/alerts") || q.queryKey[1] === "/alert-rules"),
    });
  };
  useServerEvent("alert.created", refresh);
  useServerEvent("alert.updated", refresh);
  useServerEvent("reconnected", refresh);
}
