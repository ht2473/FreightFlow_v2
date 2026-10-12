import { ChevronRight } from "lucide-react";
import { useState } from "react";
import { $api, type Schemas } from "@/shared/api/client";
import { dateTime, num } from "@/shared/format";
import { Badge, EmptyState, Skeleton } from "@/shared/ui";
import { Tabs } from "@/shared/ui/navigation";
import { Drawer } from "@/shared/ui/overlays";
import s from "./data.module.css";
import {
  domainLabel,
  duration,
  isActive,
  runStatus,
  scheduleLabel,
  severityLabel,
  statLabel,
  triggerLabel,
} from "./labels";

type Source = Schemas["SourceInfo"];

/** Карточка источника: запуски с журналом и проверками, описание и лицензия. */
export function SourceDrawer({ source, onClose }: { source: Source; onClose: () => void }) {
  return (
    <Drawer isOpen onOpenChange={(o) => !o && onClose()} title={source.title} subtitle={source.publisher}>
      <Tabs
        label="Разделы источника"
        items={[
          { id: "runs", title: "Запуски", content: <Runs code={source.code} /> },
          { id: "about", title: "Описание", content: <About source={source} /> },
        ]}
      />
    </Drawer>
  );
}

function Runs({ code }: { code: string }) {
  const runs = $api.useQuery(
    "get",
    "/data/sources/{code}/runs",
    { params: { path: { code }, query: { limit: 20 } } },
    { refetchInterval: (q) => (q.state.data?.some((r) => isActive(r.status)) ? 3000 : false) },
  );
  const [openRun, setOpenRun] = useState<string | null>(null);

  if (runs.isPending) {
    return <Skeleton height={60} />;
  }
  const list = runs.data ?? [];
  if (list.length === 0) {
    return <EmptyState title="Запусков ещё не было">Запустите загрузку из списка источников.</EmptyState>;
  }
  return (
    <ul className={s.runs}>
      {list.map((r) => {
        const st = runStatus[r.status];
        const expanded = openRun === r.id;
        return (
          <li key={r.id} className={s.run}>
            <button
              type="button"
              className={s.runHead}
              aria-expanded={expanded}
              onClick={() => setOpenRun(expanded ? null : r.id)}
            >
              <ChevronRight size={15} className={expanded ? s.chevronOpen : s.chevron} aria-hidden />
              <Badge tone={st.tone}>{st.label}</Badge>
              <span className={s.runTime}>{dateTime(r.queuedAt)}</span>
              <span className={s.muted}>
                {triggerLabel[r.trigger]}
                {r.forced ? ", заново" : ""} · {duration(r.startedAt, r.finishedAt)}
              </span>
            </button>
            {expanded && <RunDetail id={r.id} />}
          </li>
        );
      })}
    </ul>
  );
}

function RunDetail({ id }: { id: string }) {
  const d = $api.useQuery(
    "get",
    "/data/runs/{runId}",
    { params: { path: { runId: id } } },
    { refetchInterval: (q) => (isActive(q.state.data?.run.status) ? 2000 : false) },
  );
  if (d.isPending) {
    return <Skeleton height={40} />;
  }
  if (!d.data) {
    return null;
  }
  const { run, log, checks } = d.data;
  const stats = Object.entries(run.stats);
  return (
    <div className={s.runBody}>
      {run.error && <div className="notice notice-error">{run.error}</div>}
      {stats.length > 0 && (
        <dl className={s.stats}>
          {stats.map(([k, v]) => (
            <div key={k}>
              <dt>{statLabel[k] ?? k}</dt>
              <dd className="num">{num(v)}</dd>
            </div>
          ))}
        </dl>
      )}
      {checks.length > 0 && (
        <>
          <h3 className={s.subhead}>Проверки качества</h3>
          <ul className={s.checks}>
            {checks.map((c) => (
              <li key={`${c.code}-${c.checkedAt}`}>
                <Badge tone={c.passed ? "ok" : severityLabel[c.severity].tone}>
                  {c.passed ? "Пройдена" : severityLabel[c.severity].label}
                </Badge>
                <span>
                  {c.title}
                  {c.message && <span className={s.muted}> — {c.message}</span>}
                </span>
              </li>
            ))}
          </ul>
        </>
      )}
      <h3 className={s.subhead}>Журнал</h3>
      <ol className={s.log}>
        {log.map((l) => (
          <li key={`${l.at}-${l.message}`} className={l.level === "warn" ? s.logWarn : undefined}>
            <time className={s.logTime}>{dateTime(l.at).slice(-5)}</time>
            {l.message}
          </li>
        ))}
      </ol>
    </div>
  );
}

function About({ source }: { source: Source }) {
  return (
    <dl className={s.about}>
      <dt>Издатель</dt>
      <dd>{source.publisher}</dd>
      <dt>Адрес</dt>
      <dd>
        <a href={source.url} target="_blank" rel="noreferrer noopener">
          {source.url}
        </a>
      </dd>
      <dt>Лицензия</dt>
      <dd>{source.license}</dd>
      <dt>Атрибуция</dt>
      <dd>{source.attribution}</dd>
      <dt>Расписание</dt>
      <dd>
        {scheduleLabel(source.intervalSeconds)}
        {source.intervalSeconds && !source.enabled ? " (выключено)" : ""}
      </dd>
      <dt>Данные</dt>
      <dd>{source.domains.map((d) => domainLabel[d] ?? d).join(", ")}</dd>
      <dt>Сеть</dt>
      <dd>
        {source.ruNetwork ? "Российский госресурс: прямое соединение, сертификат Минцифры" : "Обычное соединение"}
      </dd>
      <dt>Код</dt>
      <dd>
        <code>{source.code}</code>
      </dd>
    </dl>
  );
}
