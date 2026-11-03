import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import type { Feature, FeatureCollection } from "geojson";
import { BellRing, Plus } from "lucide-react";
import type { LayerSpecification } from "maplibre-gl";
import { useMemo, useState } from "react";
import { Guard } from "@/app/Guard";
import { $api, problemMessage, type Schemas } from "@/shared/api/client";
import { can, useMe } from "@/shared/auth";
import { ago, dateTime } from "@/shared/format";
import { color } from "@/shared/map/dataLayers";
import {
  Badge,
  Button,
  Checkbox,
  EmptyState,
  PageHeader,
  Section,
  Select,
  Skeleton,
  TextArea,
  TextField,
} from "@/shared/ui";
import { SegmentedControl, Tabs } from "@/shared/ui/navigation";
import { toast } from "@/shared/ui/toast";
import { useDebounced } from "@/shared/useDebounced";
import {
  coordsOf,
  extentOf,
  MainMap,
  type Overlay,
  type Palette,
  usePalette,
  useScenarioStyle,
} from "../scenarios/ScenarioMap";
import s from "./alerts.module.css";
import { CheckGroup } from "./CheckGroup";
import {
  type Alert,
  actionTitle,
  kindTitle,
  type RuleKind,
  type Severity,
  type Status,
  severityTitle,
  severityTone,
  statusTitle,
  statusTone,
} from "./labels";
import { RulesList } from "./RulesList";
import { useAlertEvents } from "./useAlertEvents";

export interface AlertsSearch {
  tab?: "feed" | "rules";
  alert?: string;
}

export function alertsSearchSchema(search: Record<string, unknown>): AlertsSearch {
  return {
    tab: search.tab === "rules" ? "rules" : undefined,
    alert: typeof search.alert === "string" ? search.alert : undefined,
  };
}

export function AlertsPage() {
  return (
    <Guard permission="alerts.own">
      <Alerts />
    </Guard>
  );
}

function Alerts() {
  useAlertEvents();
  const search = useSearch({ from: "/alerts" });
  const navigate = useNavigate({ from: "/alerts" });
  const tab = search.tab ?? "feed";
  return (
    <>
      <PageHeader
        title="Алерты"
        subtitle="Изменения работ, ограничений, доступности, реестра и источников по вашим правилам"
        actions={
          <Link to="/alerts/rules/new" className="btn btn-sm btn-primary">
            <Plus size={14} aria-hidden /> Новое правило
          </Link>
        }
      />
      <Tabs
        label="Разделы алертов"
        selected={tab}
        onChange={(id) => void navigate({ search: { tab: id === "rules" ? "rules" : undefined } })}
        items={[
          { id: "feed", title: "Лента", content: <Feed selected={search.alert} /> },
          { id: "rules", title: "Правила", content: <RulesList /> },
        ]}
      />
    </>
  );
}

type StatusFilter = "open" | Status | "all";

const statusFilters: { id: StatusFilter; label: string }[] = [
  { id: "open", label: "Открытые" },
  { id: "active", label: "Активные" },
  { id: "acknowledged", label: "Подтверждённые" },
  { id: "closed", label: "Закрытые" },
  { id: "all", label: "Все" },
];

const statusQuery: Record<StatusFilter, Status[] | undefined> = {
  open: ["active", "acknowledged"],
  active: ["active"],
  acknowledged: ["acknowledged"],
  closed: ["closed"],
  all: undefined,
};

const kindOptions = [
  { id: "", label: "Все виды" },
  ...(Object.keys(kindTitle) as RuleKind[]).map((k) => ({ id: k, label: kindTitle[k] })),
];

function Feed({ selected }: { selected?: string }) {
  const navigate = useNavigate({ from: "/alerts" });
  const [status, setStatus] = useState<StatusFilter>("open");
  const [severity, setSeverity] = useState<Severity[]>([]);
  const [kind, setKind] = useState<string>("");
  const [assigned, setAssigned] = useState(false);
  const [text, setText] = useState("");
  const q = useDebounced(text.trim(), 300);
  const [limit, setLimit] = useState(50);
  const query = {
    status: statusQuery[status],
    severity: severity.length > 0 ? severity : undefined,
    kind: kind ? (kind as RuleKind) : undefined,
    assigned: assigned || undefined,
    q: q || undefined,
    limit,
  };
  const list = $api.useQuery("get", "/alerts", { params: { query } }, { placeholderData: (prev) => prev });
  const items = list.data?.items ?? [];
  const open = (id: string) => void navigate({ search: { alert: id } });

  return (
    <>
      <div className={s.filters}>
        <SegmentedControl
          label="Статус"
          value={status}
          onChange={(v) => setStatus(v as StatusFilter)}
          options={statusFilters}
        />
        <Select label="Вид правила" options={kindOptions} value={kind} onChange={setKind} />
        <TextField label="Поиск" placeholder="Улица, объект, источник" value={text} onChange={setText} />
        <CheckGroup label="Важность и назначение">
          {(["critical", "warning", "info"] as Severity[]).map((sv) => (
            <Checkbox
              key={sv}
              isSelected={severity.includes(sv)}
              onChange={(on) => setSeverity((cur) => (on ? [...cur, sv] : cur.filter((x) => x !== sv)))}
            >
              {severityTitle[sv]}
            </Checkbox>
          ))}
          <Checkbox isSelected={assigned} onChange={setAssigned}>
            Назначенные мне
          </Checkbox>
        </CheckGroup>
      </div>
      <div className={s.feed}>
        <Section title="Лента" flush={false}>
          {list.isPending ? (
            <Skeleton height={120} />
          ) : list.error ? (
            <div className="notice notice-error" role="alert">
              {problemMessage(list.error)}
            </div>
          ) : items.length === 0 ? (
            <EmptyState icon={<BellRing size={20} strokeWidth={1.7} />} title="Алертов нет">
              <p>Алерты появятся, когда правила найдут изменения в данных.</p>
            </EmptyState>
          ) : (
            <>
              <ul className={s.list} aria-label="Алерты">
                {items.map((a) => (
                  <li key={a.id}>
                    <AlertCard alert={a} selected={a.id === selected} onOpen={() => open(a.id)} />
                  </li>
                ))}
              </ul>
              {list.data?.next && (
                <Button className={s.more} size="sm" onPress={() => setLimit((n) => Math.min(n + 50, 200))}>
                  Показать ещё
                </Button>
              )}
            </>
          )}
        </Section>
        {selected ? (
          <AlertDetail id={selected} />
        ) : (
          <Section title="Алерт">
            <p className="muted">Выберите алерт в ленте: место на карте, подробности, действия и журнал.</p>
          </Section>
        )}
      </div>
    </>
  );
}

function AlertCard({ alert: a, selected, onOpen }: { alert: Alert; selected: boolean; onOpen: () => void }) {
  return (
    <button
      type="button"
      className={s.alert}
      data-severity={a.severity}
      data-selected={selected}
      aria-current={selected || undefined}
      onClick={onOpen}
    >
      <span className={s.alertTitle}>{a.title}</span>
      {a.summary && <span className={s.summary}>{a.summary}</span>}
      <span className={s.meta}>
        <Badge tone={severityTone[a.severity]}>{severityTitle[a.severity]}</Badge>
        <Badge tone={statusTone[a.status]} plain>
          {statusTitle[a.status]}
        </Badge>
        <span>{a.ruleName}</span>
        <span>{ago(a.lastSeenAt)}</span>
        {a.occurrences > 1 && <span>срабатываний: {a.occurrences}</span>}
        {a.assigneeName && <span>назначен: {a.assigneeName}</span>}
      </span>
    </button>
  );
}

/** Слои карты алерта: область правила и место алерта. */
function alertOverlay(area: FeatureCollection, place: FeatureCollection, p: Palette): Overlay {
  const layers: LayerSpecification[] = [
    {
      id: "al-area-fill",
      type: "fill",
      source: "al:area",
      paint: { "fill-color": color(p, "acc"), "fill-opacity": 0.1 },
    },
    {
      id: "al-area-line",
      type: "line",
      source: "al:area",
      paint: { "line-color": color(p, "acc"), "line-width": 1.5, "line-dasharray": [2, 1.5] },
    },
    {
      id: "al-place-line",
      type: "line",
      source: "al:place",
      filter: ["in", ["geometry-type"], ["literal", ["LineString", "MultiLineString"]]],
      paint: { "line-color": color(p, "crit"), "line-width": 5 },
    },
    {
      id: "al-place-fill",
      type: "fill",
      source: "al:place",
      filter: ["in", ["geometry-type"], ["literal", ["Polygon", "MultiPolygon"]]],
      paint: { "fill-color": color(p, "crit"), "fill-opacity": 0.35 },
    },
    {
      id: "al-place-point",
      type: "circle",
      source: "al:place",
      filter: ["in", ["geometry-type"], ["literal", ["Point", "MultiPoint"]]],
      paint: {
        "circle-radius": 7,
        "circle-color": color(p, "crit"),
        "circle-stroke-color": color(p, "panel"),
        "circle-stroke-width": 2,
      },
    },
  ];
  return {
    sources: { "al:area": { type: "geojson", data: area }, "al:place": { type: "geojson", data: place } },
    layers,
  };
}

function collection(geometry: Record<string, unknown> | undefined): FeatureCollection {
  const features: Feature[] = geometry
    ? [{ type: "Feature", properties: {}, geometry: geometry as unknown as Feature["geometry"] }]
    : [];
  return { type: "FeatureCollection", features };
}

const noContext = new Set<string>(["road_works"]);

function AlertDetail({ id }: { id: string }) {
  const me = useMe();
  const detail = $api.useQuery("get", "/alerts/{alertId}", { params: { path: { alertId: id } } });
  const palette = usePalette();
  const d = detail.data;
  const place = useMemo(() => collection(d?.alert.geometry), [d?.alert.geometry]);
  const area = useMemo(() => collection(d?.ruleArea), [d?.ruleArea]);
  const overlay = useMemo(() => alertOverlay(area, place, palette), [area, place, palette]);
  const style = useScenarioStyle(noContext, overlay);
  const fit = useMemo(() => extentOf([...coordsOf(place), ...coordsOf(area)]), [place, area]);

  if (detail.isPending) {
    return <Skeleton height={320} />;
  }
  if (detail.error || !d) {
    return (
      <div className="notice notice-error" role="alert">
        {problemMessage(detail.error)}
      </div>
    );
  }
  const a = d.alert;
  return (
    <div className={s.detail}>
      <Section title={a.title} description={a.summary || undefined}>
        <div className={s.meta}>
          <Badge tone={severityTone[a.severity]}>{severityTitle[a.severity]}</Badge>
          <Badge tone={statusTone[a.status]} plain>
            {statusTitle[a.status]}
          </Badge>
          <span>
            {kindTitle[a.kind]} · правило «{a.ruleName}»
          </span>
        </div>
        {(place.features.length > 0 || area.features.length > 0) && (
          <div className={s.map}>
            <MainMap style={style} fit={fit} label="Место алерта" />
          </div>
        )}
        <Facts alert={a} />
        <Actions detail={d} canAssign={can(me.data, "alerts.manage")} />
      </Section>
      <Section title="Журнал">
        <ol className={s.log}>
          {d.log.map((l) => (
            <li key={l.id}>
              <div className={s.logHead}>
                <strong>{actionTitle[l.action]}</strong>
                <span>{dateTime(l.at)}</span>
                {l.actorName && <span>{l.actorName}</span>}
              </div>
              {l.comment && <p className={s.logComment}>{l.comment}</p>}
            </li>
          ))}
        </ol>
      </Section>
    </div>
  );
}

interface Change {
  op: "added" | "changed" | "removed";
  title: string;
  name: string;
  from?: string;
  to?: string;
}

const changeOp = { added: "новое", changed: "изменено", removed: "снято" };

function Facts({ alert: a }: { alert: Alert }) {
  const changes = (a.details.changes as Change[] | undefined) ?? [];
  return (
    <>
      <dl className={s.facts}>
        <dt>Создан</dt>
        <dd>{dateTime(a.createdAt)}</dd>
        <dt>Последнее срабатывание</dt>
        <dd>
          {dateTime(a.lastSeenAt)} (всего {a.occurrences})
        </dd>
        {a.assigneeName && (
          <>
            <dt>Назначен</dt>
            <dd>{a.assigneeName}</dd>
          </>
        )}
        {a.closedAt && (
          <>
            <dt>Закрыт</dt>
            <dd>
              {dateTime(a.closedAt)}
              {a.resolution === "auto" ? " — причина исчезла" : ""}
            </dd>
          </>
        )}
        {a.entity.startsWith("facility:") && (
          <>
            <dt>Объект</dt>
            <dd>
              <Link to="/facilities/$facilityId" params={{ facilityId: a.entity.slice("facility:".length) }}>
                Карточка объекта
              </Link>
            </dd>
          </>
        )}
      </dl>
      {changes.length > 0 && (
        <ul className={s.changes} aria-label="Изменения ограничений">
          {changes.slice(0, 20).map((c) => (
            <li key={`${c.op}-${c.name}-${c.title}-${c.from}-${c.to}`}>
              {c.name || "Участок без названия"}: {c.title} — {changeOp[c.op]}
              {c.from || c.to ? ` (${[c.from, c.to].filter(Boolean).join(" → ")})` : ""}
            </li>
          ))}
          {changes.length > 20 && <li>и ещё {changes.length - 20}</li>}
        </ul>
      )}
    </>
  );
}

function Actions({ detail, canAssign }: { detail: Schemas["AlertDetail"]; canAssign: boolean }) {
  const a = detail.alert;
  const act = $api.useMutation("post", "/alerts/{alertId}/actions");
  const assignees = $api.useQuery("get", "/alerts/assignees", {}, { enabled: canAssign && detail.canManage });
  const [comment, setComment] = useState("");
  const run = (action: Schemas["AlertAction"]["action"], extra: Partial<Schemas["AlertAction"]> = {}) =>
    act.mutate(
      { params: { path: { alertId: a.id } }, body: { action, comment: comment.trim() || undefined, ...extra } },
      {
        onSuccess: () => {
          setComment("");
          if (action !== "comment") {
            toast.success(
              action === "assign" ? "Назначение сохранено" : `Статус: ${actionTitle[actionLog[action]].toLowerCase()}`,
            );
          }
        },
      },
    );
  const people = [
    { id: "", label: "Не назначен" },
    ...(assignees.data ?? []).map((u) => ({ id: u.id, label: u.name })),
  ];
  return (
    <>
      <TextArea label="Комментарий" value={comment} onChange={setComment} maxLength={4000} />
      <div className={s.actions}>
        {detail.canManage && a.status === "active" && (
          <Button variant="primary" size="sm" onPress={() => run("acknowledge")} isDisabled={act.isPending}>
            Подтвердить
          </Button>
        )}
        {detail.canManage && a.status !== "closed" && (
          <Button size="sm" onPress={() => run("close")} isDisabled={act.isPending}>
            Закрыть
          </Button>
        )}
        {detail.canManage && a.status === "closed" && (
          <Button size="sm" onPress={() => run("reopen")} isDisabled={act.isPending}>
            Открыть заново
          </Button>
        )}
        <Button size="sm" onPress={() => run("comment")} isDisabled={act.isPending || !comment.trim()}>
          Добавить комментарий
        </Button>
        {canAssign && detail.canManage && assignees.data && (
          <Select
            label="Ответственный"
            options={people}
            value={a.assigneeId ?? ""}
            onChange={(v) => run("assign", { assigneeId: v || undefined })}
          />
        )}
      </div>
      {act.error && (
        <div className="notice notice-error" role="alert">
          {problemMessage(act.error)}
        </div>
      )}
    </>
  );
}

const actionLog = {
  acknowledge: "acknowledged",
  close: "closed",
  reopen: "reopened",
  assign: "assigned",
  comment: "commented",
} as const;
