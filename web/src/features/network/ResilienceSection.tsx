import { Link } from "@tanstack/react-router";
import { FlaskConical, Map as MapIcon, RefreshCw } from "lucide-react";
import { useState } from "react";
import { $api, problemMessage, type Schemas } from "@/shared/api/client";
import { can, useMe } from "@/shared/auth";
import { dateTime, num } from "@/shared/format";
import { Button, cellClass, EmptyState, Section, Skeleton, tableClass } from "@/shared/ui";
import { toast } from "@/shared/ui/toast";
import s from "./analysis.module.css";
import { mapCenter } from "./labels";

const resilienceLayers = "resilience,rings";

const targetShort: Record<Schemas["ScenarioTargetKind"], string> = {
  mkad: "МКАД",
  ttk: "ТТК",
  center: "центр",
  rail_freight: "ж/д",
};

/** Элементов в таблице до «Показать все». */
const rows = 15;

/**
 * Устойчивость сети: ущерб от выпадения верхних элементов рейтинга критичности по сценариям
 * службы маршрутизации (несколько видов целей) и переход к сценарию с этим перекрытием.
 */
export function ResilienceSection() {
  const me = useMe();
  const q = $api.useQuery(
    "get",
    "/scenarios/resilience",
    {},
    { refetchInterval: (x) => (x.state.data?.running ? 15_000 : false) },
  );
  const rerun = $api.useMutation("post", "/scenarios/resilience/run");
  const [all, setAll] = useState(false);
  const run = q.data?.run;
  const list = q.data?.elements ?? [];
  const description = run
    ? `Каждый элемент перекрывается в службе маршрутизации; время объектов в ${num(run.radiusKm)} км пересчитывается до ${run.targets
        .map((t) => targetShort[t])
        .join(
          ", ",
        )}. Ущерб — прирост объекто-минут и час за каждую потерю доступа. Пакет от ${dateTime(run.finishedAt ?? run.startedAt)}.`
    : "Пакет сценариев выпадения верхних элементов рейтинга критичности с расчётом в службе маршрутизации.";
  return (
    <Section
      title="Устойчивость: ущерб от выпадения"
      description={description}
      actions={
        <>
          {can(me.data, "data.manage") && (
            <Button
              size="sm"
              isDisabled={!!q.data?.running || rerun.isPending}
              onPress={() =>
                rerun.mutate(
                  {},
                  {
                    onSuccess: () => {
                      toast.success("Пересчёт устойчивости поставлен в очередь");
                      void q.refetch();
                    },
                    onError: (e) => toast.error(problemMessage(e)),
                  },
                )
              }
            >
              <RefreshCw size={14} aria-hidden /> Пересчитать
            </Button>
          )}
          <Link to="/map" search={{ l: resilienceLayers, z: 11 }} className="btn btn-sm">
            <MapIcon size={14} aria-hidden /> Слой устойчивости
          </Link>
        </>
      }
      flush
    >
      {q.data?.running && (
        <p className={s.accuracy} role="status">
          Идёт пересчёт пакета (начат {dateTime(q.data.running.startedAt)}); показан прежний результат.
        </p>
      )}
      {q.isPending ? (
        <Skeleton height={120} />
      ) : q.error ? (
        <div className="notice notice-error" role="alert">
          {problemMessage(q.error)}
        </div>
      ) : list.length === 0 ? (
        <EmptyState title={run?.status === "failed" ? "Пакет не посчитан" : "Пакет устойчивости ещё не посчитан"}>
          {run?.error ?? "Пакет считается фоновой задачей после сетевого анализа, если запущена служба маршрутизации."}
        </EmptyState>
      ) : (
        <>
          <table className={tableClass}>
            <thead>
              <tr>
                <th className={cellClass.right}>Место</th>
                <th>Элемент</th>
                <th className={cellClass.right}>Ущерб, объекто-мин</th>
                <th>По целям</th>
                <th className={cellClass.right}>Затронуто</th>
                <th className={cellClass.right}>Рейтинг критичности</th>
                <th aria-label="Действия" />
              </tr>
            </thead>
            <tbody>
              {(all ? list : list.slice(0, rows)).map((el) => (
                <tr key={el.elementId}>
                  <td className={`${cellClass.right} num`}>{num(el.rank)}</td>
                  <td>
                    {el.name || "Без названия"}
                    <div className={s.kind}>{el.kind === "bridge" ? "мост, путепровод" : "участок дороги"}</div>
                  </td>
                  <td className={`${cellClass.right} num`}>{num(el.damage)}</td>
                  <td className={s.kind}>
                    {el.byTarget
                      .filter((t) => t.damage > 0)
                      .map(
                        (t) => `${targetShort[t.kind]} ${num(t.damage)}${t.lost > 0 ? ` (без доступа ${t.lost})` : ""}`,
                      )
                      .join("; ") || "объезд без потерь"}
                  </td>
                  <td className={`${cellClass.right} num`}>
                    {num(el.affected)} из {num(el.candidates)}
                  </td>
                  <td className={`${cellClass.right} num`}>{num(el.criticalityRank)}</td>
                  <td className={cellClass.right}>
                    <span className={s.rowActions}>
                      <Link
                        to="/map"
                        search={{ c: mapCenter(el.point), z: 15, l: resilienceLayers }}
                        aria-label={`${el.name || "Элемент"} на карте`}
                        className={s.mapLink}
                      >
                        <MapIcon size={14} aria-hidden />
                      </Link>
                      {can(me.data, "scenarios.manage") && (
                        <Link
                          to="/scenarios/new"
                          search={{ element: el.elementId }}
                          aria-label={`Сценарий с перекрытием «${el.name || "элемента"}»`}
                          className={s.mapLink}
                        >
                          <FlaskConical size={14} aria-hidden />
                        </Link>
                      )}
                    </span>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
          {list.length > rows && (
            <div className={s.accuracy}>
              <Button size="sm" variant="ghost" onPress={() => setAll(!all)}>
                {all ? `Первые ${rows}` : `Показать все ${list.length}`}
              </Button>
            </div>
          )}
        </>
      )}
    </Section>
  );
}
