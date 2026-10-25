import type { FeatureCollection } from "geojson";
import { $api, problemMessage } from "@/shared/api/client";
import { EmptyState, Select, Skeleton } from "@/shared/ui";
import t from "../facilities/facilities.module.css";
import s from "./analytics.module.css";
import { MetricHelp } from "./MetricHelp";
import { MetricMap, useTerritoryOpen } from "./MetricMap";
import { type Level, levelTitles, metricValue, useMetrics } from "./metrics";
import { useRows } from "./useRows";

const mapLevels: Level[] = ["district", "okrug", "h3_8", "h3_9"];
const panelLevels: Level[] = ["district", "okrug", "h3_8", "h3_9", "facility", "segment"];

/** Показатели по уровням: рейтинг таблицей и картограмма (районы, округа, ячейки H3). */
export function MetricsPanel({
  level,
  metric,
  onChange,
}: {
  level: Level;
  metric: string | undefined;
  onChange: (level: Level, metric: string | undefined) => void;
}) {
  const metrics = useMetrics();
  const options = (metrics.data ?? []).filter((m) => m.levels.includes(level) && !m.code.startsWith("city."));
  const code = options.find((m) => m.code === metric)?.code ?? options[0]?.code;
  const def = code ? metrics.byCode.get(code) : undefined;
  const values = $api.useQuery(
    "get",
    "/metrics/{code}",
    { params: { path: { code: code ?? "" }, query: { level, limit: 500 } } },
    { enabled: !!code },
  );
  const geo = $api.useQuery(
    "get",
    "/metrics/{code}/geo",
    { params: { path: { code: code ?? "" }, query: { level: level as "district" } } },
    { enabled: !!code && mapLevels.includes(level) },
  );
  const open = useTerritoryOpen(level);
  const { shown, more } = useRows(values.data?.values ?? []);
  const fmt = (v: number) => metricValue(v, def);

  return (
    <>
      <div className={s.toolbar}>
        <Select
          label="Уровень"
          options={panelLevels.map((l) => ({ id: l, label: levelTitles[l] }))}
          value={level}
          onChange={(l) => onChange(l as Level, undefined)}
        />
        <Select
          label="Показатель"
          options={options.map((m) => ({ id: m.code, label: m.title }))}
          value={code ?? null}
          onChange={(m) => onChange(level, m)}
        />
        <MetricHelp def={def} />
      </div>
      {def && <p className={s.note}>{def.formula}</p>}
      {values.error ? (
        <div className="notice notice-error" role="alert">
          {problemMessage(values.error)}
        </div>
      ) : values.isPending ? (
        <Skeleton height={240} />
      ) : (values.data?.values.length ?? 0) === 0 ? (
        <EmptyState title="Значений нет">Показатель ещё не рассчитан для этого уровня.</EmptyState>
      ) : (
        <div className={mapLevels.includes(level) ? s.layout : undefined}>
          <div>
            <table className={t.table} aria-label={`Рейтинг: ${def?.title ?? ""}`}>
              <thead>
                <tr>
                  <th className={t.right}>Место</th>
                  <th>{levelTitles[level]}</th>
                  <th className={t.right}>
                    {def?.title}
                    {def?.unit ? `, ${def.unit}` : ""}
                  </th>
                </tr>
              </thead>
              <tbody>
                {shown.map((v) => (
                  <tr key={v.entity}>
                    <td className={`${t.right} num`}>{v.rank}</td>
                    <td>{v.label}</td>
                    <td className={`${t.right} num`}>{metricValue(v.value, def, false)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
            {more}
          </div>
          {mapLevels.includes(level) &&
            (geo.data ? (
              <MetricMap
                geo={geo.data as unknown as FeatureCollection}
                label={`Картограмма: ${def?.title ?? ""}`}
                format={fmt}
                onSelect={open}
              />
            ) : (
              <Skeleton height={520} />
            ))}
        </div>
      )}
    </>
  );
}
