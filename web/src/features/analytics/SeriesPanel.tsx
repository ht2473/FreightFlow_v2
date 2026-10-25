import { $api, problemMessage } from "@/shared/api/client";
import { num } from "@/shared/format";
import { Badge, EmptyState, Select, Skeleton } from "@/shared/ui";
import { Chart } from "@/shared/ui/Chart";
import t from "../facilities/facilities.module.css";
import s from "./analytics.module.css";
import { MetricHelp } from "./MetricHelp";
import { isYearly, metricValue, modelTitles, periodLabel, useMetrics } from "./metrics";

/** Ряд показателя с прогнозом: история, прогноз пунктиром с интервалом 80 %, точность моделей. */
export function SeriesView({ code, height = 300, compact }: { code: string; height?: number; compact?: boolean }) {
  const q = $api.useQuery("get", "/metrics/{code}/series", { params: { path: { code }, query: { horizon: 12 } } });
  const d = q.data;
  if (q.isPending) {
    return <Skeleton height={height} />;
  }
  if (q.error || !d) {
    return (
      <div className="notice notice-error" role="alert">
        {problemMessage(q.error)}
      </div>
    );
  }
  if (d.points.length === 0) {
    return <EmptyState title="Ряда нет">Источник ещё не загружен.</EmptyState>;
  }
  const yearly = isYearly(d.points);
  const fc = d.forecast;
  // Последние 5 лет истории (у годовых — весь ряд) и прогноз.
  const hist = yearly ? d.points : d.points.slice(-60);
  const fcPoints = fc?.points ?? [];
  const labels = [
    ...hist.map((p) => periodLabel(p.period, yearly)),
    ...fcPoints.map((p) => periodLabel(p.period, yearly)),
  ];
  const pad = (n: number) => Array.from({ length: n }, () => null);
  const last = hist[hist.length - 1]?.value ?? null;
  const series = [
    {
      name: d.metric.title,
      kind: "line" as const,
      color: "acc",
      data: [...hist.map((p) => p.value), ...pad(fcPoints.length)],
    },
    ...(fc
      ? [
          {
            name: `Прогноз (${modelTitles[fc.model] ?? fc.model})`,
            kind: "line" as const,
            color: "cat2",
            dashed: true,
            // Пунктир начинается от последнего фактического значения.
            data: [...pad(hist.length - 1), last, ...fcPoints.map((p) => p.value)],
          },
        ]
      : []),
  ];
  const band = fc
    ? {
        name: "Интервал 80 %",
        lo: [...pad(hist.length), ...fcPoints.map((p) => p.lo80)],
        hi: [...pad(hist.length), ...fcPoints.map((p) => p.hi80)],
      }
    : undefined;
  return (
    <>
      <Chart
        labels={labels}
        series={series}
        band={band}
        label={`${d.metric.title}: ряд и прогноз`}
        unit={d.metric.unit === "в единицах набора" ? undefined : d.metric.unit}
        height={height}
        exportName={d.metric.code}
        compact={compact}
      />
      {fc ? (
        <p className={s.note}>
          <Badge tone="info" plain>
            модель
          </Badge>{" "}
          Прогноз на {fcPoints.length} {yearly ? "лет" : "мес."}: {modelTitles[fc.model] ?? fc.model}, выбрана по MASE
          на скользящей проверке ({fc.holdout} начал). Следующий период — {metricValue(fcPoints[0]?.value, d.metric)}{" "}
          (80 %: {metricValue(fcPoints[0]?.lo80, d.metric)} – {metricValue(fcPoints[0]?.hi80, d.metric)}).
        </p>
      ) : (
        <p className={s.note}>{d.forecastNote}</p>
      )}
      {!compact && fc && (
        <table className={t.table} aria-label="Точность моделей на истории">
          <thead>
            <tr>
              <th>Модель</th>
              <th className={t.right}>MASE</th>
              <th className={t.right}>Проверочных пар</th>
            </tr>
          </thead>
          <tbody>
            {fc.scores.map((sc) => (
              <tr key={sc.model}>
                <td>
                  {modelTitles[sc.model] ?? sc.model}
                  {sc.model === fc.model && " — выбрана"}
                </td>
                <td className={`${t.right} num`}>{num(sc.mase, 3)}</td>
                <td className={`${t.right} num`}>{num(sc.points)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
    </>
  );
}

/** Вкладка рядов: выбор ряда города. */
export function SeriesPanel({ metric, onChange }: { metric: string | undefined; onChange: (m: string) => void }) {
  const metrics = useMetrics();
  const options = (metrics.data ?? []).filter((m) => m.code.startsWith("city."));
  const code = options.find((m) => m.code === metric)?.code ?? options[0]?.code;
  return (
    <>
      <div className={s.toolbar}>
        <Select
          label="Ряд"
          options={options.map((m) => ({ id: m.code, label: m.title }))}
          value={code ?? null}
          onChange={onChange}
        />
        <MetricHelp def={code ? metrics.byCode.get(code) : undefined} />
      </div>
      {code ? <SeriesView code={code} /> : <Skeleton height={300} />}
      <p className={s.note}>
        MASE — средняя абсолютная ошибка прогноза на скользящей проверке, делённая на ошибку сезонной наивной модели на
        одном шаге: меньше 1 — модель точнее повторения прошлого сезона.
      </p>
    </>
  );
}
