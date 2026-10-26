import { Link } from "@tanstack/react-router";
import { FlaskConical, Network, Plus } from "lucide-react";
import { Guard } from "@/app/Guard";
import { $api, problemMessage } from "@/shared/api/client";
import { dateTime } from "@/shared/format";
import { Badge, EmptyState, PageHeader, Section, Skeleton } from "@/shared/ui";
import { paramsSummary, statusTitle, statusTone, targetTitle } from "./labels";
import s from "./scenario.module.css";
import { useScenarioEvents } from "./useScenarioEvents";

export function ScenariosPage() {
  return (
    <Guard permission="scenarios.manage">
      <ScenarioList />
    </Guard>
  );
}

function ScenarioList() {
  useScenarioEvents();
  const list = $api.useQuery("get", "/scenarios");
  return (
    <>
      <PageHeader
        title="Сценарии"
        subtitle="Расчёты «что если»: перекрытия, правила зон, новые объекты — с количественной оценкой изменений доступности"
        actions={
          <>
            <Link to="/network/analysis" className="btn btn-sm">
              <Network size={14} aria-hidden /> Устойчивость сети
            </Link>
            <Link to="/scenarios/new" search={{}} className="btn btn-sm btn-primary">
              <Plus size={14} aria-hidden /> Новый сценарий
            </Link>
          </>
        }
      />
      <Section title="Мои сценарии">
        {list.isPending ? (
          <Skeleton height={64} />
        ) : list.error ? (
          <div className="notice notice-error" role="alert">
            {problemMessage(list.error)}
          </div>
        ) : list.data.length === 0 ? (
          <EmptyState
            icon={<FlaskConical size={22} />}
            title="Сценариев пока нет"
            action={
              <Link to="/scenarios/new" search={{}} className="btn btn-primary">
                <Plus size={15} aria-hidden /> Новый сценарий
              </Link>
            }
          >
            Перекройте мост или участок, измените правила зон или поставьте гипотетический объект — система посчитает,
            как изменится доступность объектов.
          </EmptyState>
        ) : (
          <ul className={s.list}>
            {list.data.map((sc) => {
              const main = sc.summary?.targets[0];
              return (
                <li key={sc.id}>
                  <Link to="/scenarios/$scenarioId" params={{ scenarioId: sc.id }} className={s.scenarioLink}>
                    <span className={s.scenarioHead}>
                      <span>{sc.name}</span>
                      <Badge tone={statusTone[sc.status]}>{statusTitle[sc.status]}</Badge>
                    </span>
                    <span className={s.muted}>
                      {[paramsSummary(sc.params), dateTime(sc.createdAt)].filter(Boolean).join(" · ")}
                    </span>
                    {main && (
                      <span className={s.muted}>
                        {targetTitle[main.kind]}: затронуто {main.affected}, потеряли доступ {main.lost}
                      </span>
                    )}
                  </Link>
                </li>
              );
            })}
          </ul>
        )}
      </Section>
    </>
  );
}
