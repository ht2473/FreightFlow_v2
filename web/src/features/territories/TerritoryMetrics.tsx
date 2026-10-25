import { $api } from "@/shared/api/client";
import { can, useMe } from "@/shared/auth";
import { Section, Skeleton } from "@/shared/ui";
import { MetricHelp } from "../analytics/MetricHelp";
import { metricValue, useMetrics } from "../analytics/metrics";
import t from "../facilities/facilities.module.css";

/** Рассчитанные показатели территории с местом среди районов (округов) и методикой. */
export function TerritoryMetrics({ level, id }: { level: "district" | "okrug"; id: string }) {
  const me = useMe();
  const metrics = useMetrics();
  const q = $api.useQuery(
    "get",
    "/metrics/entity",
    { params: { query: { level, entity: id } } },
    { enabled: can(me.data, "analytics.view") },
  );
  if (!can(me.data, "analytics.view")) {
    return null;
  }
  const peers = level === "district" ? "районов" : "округов";
  return (
    <Section title="Показатели">
      {q.isPending ? (
        <Skeleton height={200} />
      ) : (
        <table className={t.table} aria-label="Показатели территории">
          <thead>
            <tr>
              <th>Показатель</th>
              <th className={t.right}>Значение</th>
              <th className={t.right}>Место среди {peers}</th>
              <th aria-label="Методика" />
            </tr>
          </thead>
          <tbody>
            {(q.data ?? []).map((v) => {
              const def = metrics.byCode.get(v.code);
              return (
                <tr key={v.code}>
                  <td>{def?.title ?? v.code}</td>
                  <td className={`${t.right} num`}>{metricValue(v.value, def)}</td>
                  <td className={`${t.right} num`}>
                    {v.rank} из {v.of}
                  </td>
                  <td>
                    <MetricHelp def={def} />
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </Section>
  );
}
