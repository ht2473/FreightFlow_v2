import { useEffect } from "react";
import { Guard } from "@/app/Guard";
import { $api } from "@/shared/api/client";
import { dateTime } from "@/shared/format";
import { PageHeader, Section, Skeleton } from "@/shared/ui";
import s from "./analytics.module.css";
import { MethodText } from "./MetricHelp";
import { levelTitles, useMetrics } from "./metrics";

/** Страница методик: каждый показатель и индекс — формула, методика, уровни, входы, версия. */
export function MethodsPage() {
  return (
    <Guard permission="analytics.view">
      <Methods />
    </Guard>
  );
}

function Methods() {
  const metrics = useMetrics();
  const indexes = $api.useQuery("get", "/indexes", {}, { staleTime: 60_000 });
  // Переход по ссылке «Все методики» — к нужному показателю.
  useEffect(() => {
    const id = decodeURIComponent(window.location.hash.slice(1));
    if (id && metrics.data) {
      document.getElementById(id)?.scrollIntoView();
    }
  }, [metrics.data]);
  return (
    <>
      <PageHeader title="Методики показателей" subtitle="Формулы, данные и ограничения каждого показателя и индекса" />
      <Section title="Сводные индексы">
        {indexes.isPending ? (
          <Skeleton height={120} />
        ) : (
          <dl className={s.methods}>
            {(indexes.data ?? []).map((ix) => (
              <div key={ix.code} id={ix.code}>
                <dt>
                  <a href={`#${ix.code}`}>{ix.title}</a> <span className={s.code}>{ix.code}</span>
                </dt>
                <dd>
                  <p className={s.method}>{ix.method}</p>
                  {ix.levels.map((l) => (
                    <p key={l.level} className={s.note}>
                      {levelTitles[l.level]}:{" "}
                      {l.components
                        .map((c) => `${c.title} (${c.direction > 0 ? "+" : "−"}, вес ${c.defaultWeight})`)
                        .join("; ")}
                    </p>
                  ))}
                  <p className={s.note}>
                    Нормировка составляющих — min–max по объектам уровня или z-оценка с усечением до ±3σ; итог —
                    взвешенное среднее × 100. Чувствительность — вес каждой составляющей ±20 %, корреляция Спирмена
                    рангов.
                  </p>
                </dd>
              </div>
            ))}
          </dl>
        )}
      </Section>
      <Section title="Показатели">
        {metrics.isPending ? (
          <Skeleton height={240} />
        ) : (
          <dl className={s.methods}>
            {(metrics.data ?? []).map((m) => (
              <div key={m.code} id={m.code}>
                <dt>
                  <a href={`#${m.code}`}>
                    {m.title}, {m.unit}
                  </a>{" "}
                  <span className={s.code}>{m.code}</span>
                </dt>
                <dd>
                  <p className={s.formula}>{m.formula}</p>
                  <p className={s.method}>
                    <MethodText text={m.method} />
                  </p>
                  <p className={s.note}>
                    Уровни: {m.levels.map((l) => levelTitles[l]).join(", ")}. Входы:{" "}
                    {[...m.domains, ...m.dependsOn].join(", ")}. Версия {m.version}.{" "}
                    {m.computedAt ? `Рассчитано ${dateTime(m.computedAt)}, значений ${m.rows}.` : ""}
                  </p>
                </dd>
              </div>
            ))}
          </dl>
        )}
      </Section>
    </>
  );
}
