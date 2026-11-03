import { useQueryClient } from "@tanstack/react-query";
import { useNavigate, useSearch } from "@tanstack/react-router";
import { CalendarClock, Download, Eye, FileText, Plus, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { Guard } from "@/app/Guard";
import { $api, problemMessage, type Schemas } from "@/shared/api/client";
import { useServerEvent } from "@/shared/api/events";
import { can, useMe } from "@/shared/auth";
import { dateTime } from "@/shared/format";
import {
  Badge,
  Button,
  cellClass,
  EmptyState,
  IconButton,
  Modal,
  PageHeader,
  Section,
  Select,
  Skeleton,
  Switch,
  TextArea,
  tableClass,
} from "@/shared/ui";
import { NumberField } from "@/shared/ui/fields";
import { SegmentedControl, Tabs } from "@/shared/ui/navigation";
import { toast } from "@/shared/ui/toast";
import { PlaceSearch } from "../network/PlaceSearch";
import s from "./reports.module.css";
import type { ReportsSearch } from "./search";

type Template = Schemas["ReportTemplate"];
type Report = Schemas["Report"];
type Format = Schemas["ReportFormat"];

export function ReportsPage() {
  return (
    <Guard permission="reports.view">
      <Reports />
    </Guard>
  );
}

const statusTitle: Record<Report["status"], string> = {
  queued: "В очереди",
  running: "Формируется",
  done: "Готов",
  failed: "Ошибка",
};
const statusTone: Record<Report["status"], "neutral" | "info" | "ok" | "crit"> = {
  queued: "neutral",
  running: "info",
  done: "ok",
  failed: "crit",
};

function size(bytes?: number) {
  if (!bytes) {
    return "";
  }
  return bytes > 1 << 20
    ? `${(bytes / (1 << 20)).toFixed(1).replace(".", ",")} МБ`
    : `${Math.max(1, Math.round(bytes / 1024))} КБ`;
}

function printURL(r: { template: string; params: Record<string, string> }) {
  const q = new URLSearchParams(r.params).toString();
  return `/print/${r.template}${q ? `?${q}` : ""}`;
}

function Reports() {
  const me = useMe();
  const search = useSearch({ from: "/reports" });
  const navigate = useNavigate({ from: "/reports" });
  const creator = can(me.data, "reports.create");
  // Ссылка «Отчёт» из карточки: /reports?template=facility&facilityId=… открывает форму.
  const [creating, setCreating] = useState(!!search.template && creator);
  const tab = search.tab ?? "reports";
  return (
    <>
      <PageHeader
        title="Отчёты"
        subtitle="Отчёты PDF и XLSX по шаблонам — с графиками, картами и источниками данных; хранятся неделю"
        actions={
          creator ? (
            <Button variant="primary" size="sm" onPress={() => setCreating(true)}>
              <Plus size={14} aria-hidden /> Сформировать отчёт
            </Button>
          ) : undefined
        }
      />
      <Tabs
        label="Отчёты и расписания"
        selected={tab}
        onChange={(id) => void navigate({ search: { tab: id === "schedules" ? "schedules" : undefined } })}
        items={[
          { id: "reports", title: "Мои отчёты", content: <ReportList highlight={search.report} /> },
          ...(creator ? [{ id: "schedules", title: "Расписания", content: <Schedules /> }] : []),
        ]}
      />
      {creating && (
        <CreateReport
          initial={search}
          onClose={() => {
            setCreating(false);
            if (search.template) {
              void navigate({ search: {} });
            }
          }}
        />
      )}
    </>
  );
}

function ReportList({ highlight }: { highlight?: string }) {
  const qc = useQueryClient();
  const list = $api.useQuery(
    "get",
    "/reports",
    {},
    {
      // Пока что-то формируется — опрос; о готовности сообщает и уведомление.
      refetchInterval: (q) =>
        q.state.data?.some((r) => r.status === "queued" || r.status === "running") ? 3000 : false,
    },
  );
  const refresh = () => void qc.invalidateQueries({ queryKey: ["get", "/reports"] });
  useServerEvent("notification.created", refresh);
  const remove = $api.useMutation("delete", "/reports/{reportId}", { onSuccess: refresh });
  const templates = $api.useQuery("get", "/report-templates", {}, { staleTime: 300_000 });
  const titles = new Map((templates.data ?? []).map((t) => [t.code, t.title]));
  if (list.isPending) {
    return <Skeleton height={80} />;
  }
  if ((list.data ?? []).length === 0) {
    return (
      <EmptyState icon={<FileText size={20} strokeWidth={1.7} />} title="Отчётов пока нет">
        <p>Паспорт объекта, сводка по территории, проходимость, работы, сценарий, оценка локации, качество данных.</p>
      </EmptyState>
    );
  }
  return (
    <Section flush>
      <table className={tableClass} aria-label="Отчёты">
        <thead>
          <tr>
            <th>Отчёт</th>
            <th>Формат</th>
            <th>Статус</th>
            <th>Создан</th>
            <th className={cellClass.right}>
              <span className="visually-hidden">Действия</span>
            </th>
          </tr>
        </thead>
        <tbody>
          {(list.data ?? []).map((r) => (
            <tr
              key={r.id}
              data-highlight={r.id === highlight || undefined}
              className={r.id === highlight ? s.highlight : undefined}
            >
              <td>
                <div>{r.title}</div>
                <div className="muted">
                  {titles.get(r.template) ?? r.template}
                  {r.scheduleId ? " · по расписанию" : ""}
                </div>
                {r.error && <div className={s.error}>{r.error}</div>}
              </td>
              <td>{r.format.toUpperCase()}</td>
              <td>
                <Badge tone={statusTone[r.status]}>{statusTitle[r.status]}</Badge>
              </td>
              <td className="nowrap">{dateTime(r.createdAt)}</td>
              <td className={cellClass.right}>
                <span className={s.actions}>
                  {r.status === "done" && (
                    <a className="btn btn-sm" href={`/api/v1/reports/${r.id}/file`} download>
                      <Download size={14} aria-hidden /> Скачать{r.fileSize ? `, ${size(r.fileSize)}` : ""}
                    </a>
                  )}
                  <a
                    className="btn btn-sm btn-ghost"
                    href={printURL(r)}
                    target="_blank"
                    rel="noreferrer"
                    aria-label={`Предпросмотр «${r.title}»`}
                  >
                    <Eye size={14} aria-hidden />
                  </a>
                  <IconButton
                    label={`Удалить «${r.title}»`}
                    onPress={() => remove.mutate({ params: { path: { reportId: r.id } } })}
                  >
                    <Trash2 size={15} />
                  </IconButton>
                </span>
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </Section>
  );
}

/** Поля параметров шаблона: объект — поиском по реестру, территория, профиль, сценарий, оценка — из списков. */
function ParamFields({
  template,
  values,
  onChange,
}: {
  template?: Template;
  values: Record<string, string>;
  onChange: (v: Record<string, string>) => void;
}) {
  const names = new Set(template?.params.map((p) => p.name));
  const territories = $api.useQuery(
    "get",
    "/territories",
    {},
    { enabled: names.has("territoryId"), staleTime: 300_000 },
  );
  const profiles = $api.useQuery("get", "/routing/profiles", {}, { enabled: names.has("profile"), staleTime: 300_000 });
  const scenarios = $api.useQuery("get", "/scenarios", {}, { enabled: names.has("scenarioId") });
  const runs = $api.useQuery("get", "/site-selection/runs", {}, { enabled: names.has("siteRunId") });
  const [facilityName, setFacilityName] = useState("");
  const set = (k: string, v: string) => onChange({ ...values, [k]: v });
  if (!template) {
    return null;
  }
  return (
    <>
      {template.params.map((p) => {
        switch (p.name) {
          case "facilityId":
            return (
              <div key={p.name}>
                <PlaceSearch
                  label={`${p.title}${p.required ? "" : " (необязательно)"}`}
                  onSelect={(pl) => {
                    if (pl.kind !== "facility") {
                      toast.error("Выберите объект реестра");
                      return;
                    }
                    setFacilityName(pl.title);
                    set(p.name, pl.ref);
                  }}
                />
                <p className="muted">
                  {values.facilityId ? `Выбран: ${facilityName || "объект из карточки"}` : "Объект не выбран"}
                </p>
              </div>
            );
          case "territoryId":
            return (
              <Select
                key={p.name}
                label={p.required ? p.title : `${p.title} (без выбора — вся Москва)`}
                value={values.territoryId ?? (p.required ? null : "")}
                onChange={(v) => set(p.name, v)}
                options={[
                  ...(p.required ? [] : [{ id: "", label: "Вся Москва" }]),
                  ...(territories.data ?? [])
                    .slice()
                    .sort((a, b) =>
                      a.kind === b.kind ? a.name.localeCompare(b.name, "ru") : a.kind === "okrug" ? -1 : 1,
                    )
                    .map((t) => ({ id: t.id, label: t.kind === "okrug" ? `${t.name} (округ)` : t.name })),
                ]}
              />
            );
          case "profile":
            return (
              <Select
                key={p.name}
                label={p.title}
                value={values.profile ?? null}
                onChange={(v) => set(p.name, v)}
                options={(profiles.data ?? []).map((x) => ({ id: x.code, label: x.name }))}
              />
            );
          case "scenarioId":
            return (
              <Select
                key={p.name}
                label={p.title}
                value={values.scenarioId ?? null}
                onChange={(v) => set(p.name, v)}
                options={(scenarios.data ?? [])
                  .filter((x) => x.status === "done")
                  .map((x) => ({ id: x.id, label: x.name }))}
              />
            );
          case "siteRunId":
            return (
              <Select
                key={p.name}
                label={p.title}
                value={values.siteRunId ?? null}
                onChange={(v) => set(p.name, v)}
                options={(runs.data ?? []).map((x) => ({ id: x.id, label: x.name }))}
              />
            );
          default:
            return p.kind === "int" ? (
              <NumberField
                key={p.name}
                label={p.title}
                value={values[p.name] ? Number(values[p.name]) : (p.min ?? 0) * 2}
                minValue={p.min}
                maxValue={p.max}
                onChange={(v) => set(p.name, v === null ? "" : String(v))}
              />
            ) : null;
        }
      })}
    </>
  );
}

function ready(template: Template | undefined, values: Record<string, string>) {
  return !!template && template.params.every((p) => !p.required || !!values[p.name]);
}

function cleanParams(template: Template | undefined, values: Record<string, string>) {
  const out: Record<string, string> = {};
  for (const p of template?.params ?? []) {
    if (values[p.name]) {
      out[p.name] = values[p.name] as string;
    }
  }
  return out;
}

function CreateReport({ initial, onClose }: { initial: ReportsSearch; onClose: () => void }) {
  const qc = useQueryClient();
  const templates = $api.useQuery("get", "/report-templates", {}, { staleTime: 300_000 });
  const [code, setCode] = useState<string | null>(initial.template ?? null);
  const [format, setFormat] = useState<Format>("pdf");
  const [values, setValues] = useState<Record<string, string>>(() => {
    const { tab: _t, report: _r, template: _tpl, ...rest } = initial;
    return Object.fromEntries(Object.entries(rest).filter(([, v]) => v)) as Record<string, string>;
  });
  const create = $api.useMutation("post", "/reports");
  const template = templates.data?.find((t) => t.code === code);
  useEffect(() => {
    if (!code && templates.data?.[0]) {
      setCode(templates.data[0].code);
    }
  }, [code, templates.data]);
  return (
    <Modal
      isOpen
      onOpenChange={(open) => !open && onClose()}
      title="Новый отчёт"
      description={template?.description}
      actions={
        <>
          <Button variant="ghost" onPress={onClose}>
            Отмена
          </Button>
          <Button
            variant="primary"
            isDisabled={!ready(template, values) || create.isPending}
            onPress={() =>
              create.mutate(
                { body: { template: code ?? "", format, params: cleanParams(template, values) } },
                {
                  onSuccess: () => {
                    void qc.invalidateQueries({ queryKey: ["get", "/reports"] });
                    toast.success("Отчёт поставлен в очередь — придёт уведомление о готовности");
                    onClose();
                  },
                },
              )
            }
          >
            Сформировать
          </Button>
        </>
      }
    >
      <div className={s.form}>
        <Select
          label="Шаблон"
          value={code}
          onChange={(v) => {
            setCode(v);
            setValues({});
          }}
          options={(templates.data ?? []).map((t) => ({ id: t.code, label: t.title }))}
        />
        <SegmentedControl
          label="Формат"
          value={format}
          onChange={(v) => setFormat(v as Format)}
          options={[
            { id: "pdf", label: "PDF" },
            { id: "xlsx", label: "XLSX" },
          ]}
        />
        <ParamFields template={template} values={values} onChange={setValues} />
        {create.error && (
          <div className="notice notice-error" role="alert">
            {problemMessage(create.error)}
          </div>
        )}
      </div>
    </Modal>
  );
}

const frequencyTitle: Record<Schemas["ReportSchedule"]["frequency"], string> = {
  daily: "Ежедневно",
  weekly: "Еженедельно",
  monthly: "Ежемесячно",
};

const weekdays = ["понедельник", "вторник", "среда", "четверг", "пятница", "суббота", "воскресенье"];

function scheduleWhen(sc: Schemas["ReportSchedule"]) {
  const h = `${String(sc.hour).padStart(2, "0")}:00`;
  if (sc.frequency === "weekly" && sc.weekday) {
    return `по дням: ${weekdays[sc.weekday - 1]}, ${h}`;
  }
  if (sc.frequency === "monthly" && sc.monthDay) {
    return `${sc.monthDay}-го числа, ${h}`;
  }
  return `каждый день, ${h}`;
}

function Schedules() {
  const qc = useQueryClient();
  const list = $api.useQuery("get", "/report-schedules");
  const refresh = () => void qc.invalidateQueries({ queryKey: ["get", "/report-schedules"] });
  const update = $api.useMutation("put", "/report-schedules/{scheduleId}", { onSuccess: refresh });
  const remove = $api.useMutation("delete", "/report-schedules/{scheduleId}", { onSuccess: refresh });
  const [adding, setAdding] = useState(false);
  return (
    <Section
      title="Расписания"
      description="Отчёт формируется в выбранный час по Москве и приходит получателям письмом с файлом."
      flush
      actions={
        <Button size="sm" onPress={() => setAdding(true)}>
          <CalendarClock size={14} aria-hidden /> Новое расписание
        </Button>
      }
    >
      {list.isPending ? (
        <Skeleton height={60} />
      ) : (list.data ?? []).length === 0 ? (
        <EmptyState icon={<CalendarClock size={20} strokeWidth={1.7} />} title="Расписаний нет">
          <p>Например, сводка по округу каждый понедельник в 8:00 — руководителю на почту.</p>
        </EmptyState>
      ) : (
        <table className={tableClass} aria-label="Расписания отчётов">
          <thead>
            <tr>
              <th>Отчёт</th>
              <th>Когда</th>
              <th>Получатели</th>
              <th>Следующий</th>
              <th>Включено</th>
              <th className={cellClass.right}>
                <span className="visually-hidden">Действия</span>
              </th>
            </tr>
          </thead>
          <tbody>
            {(list.data ?? []).map((sc) => (
              <tr key={sc.id}>
                <td>
                  <div>{sc.title}</div>
                  <div className="muted">
                    {frequencyTitle[sc.frequency]} · {sc.format.toUpperCase()}
                  </div>
                </td>
                <td>{scheduleWhen(sc)}</td>
                <td>{sc.recipients?.length ? sc.recipients.join(", ") : "только в системе"}</td>
                <td className="nowrap">{dateTime(sc.nextRunAt)}</td>
                <td>
                  <Switch
                    aria-label={`Расписание «${sc.title}» включено`}
                    isSelected={sc.enabled}
                    onChange={(enabled) =>
                      update.mutate({ params: { path: { scheduleId: sc.id } }, body: { ...sc, enabled } })
                    }
                  >
                    <span className="visually-hidden">Включено</span>
                  </Switch>
                </td>
                <td className={cellClass.right}>
                  <IconButton
                    label={`Удалить расписание «${sc.title}»`}
                    onPress={() => remove.mutate({ params: { path: { scheduleId: sc.id } } })}
                  >
                    <Trash2 size={15} />
                  </IconButton>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}
      {(update.error || remove.error) && (
        <div className="notice notice-error" role="alert">
          {problemMessage(update.error ?? remove.error)}
        </div>
      )}
      {adding && <CreateSchedule onClose={() => setAdding(false)} onSaved={refresh} />}
    </Section>
  );
}

const hours = Array.from({ length: 24 }, (_, h) => ({ id: String(h), label: `${String(h).padStart(2, "0")}:00` }));

function CreateSchedule({ onClose, onSaved }: { onClose: () => void; onSaved: () => void }) {
  const templates = $api.useQuery("get", "/report-templates", {}, { staleTime: 300_000 });
  const [code, setCode] = useState<string | null>(null);
  const [format, setFormat] = useState<Format>("pdf");
  const [values, setValues] = useState<Record<string, string>>({});
  const [frequency, setFrequency] = useState<Schemas["ReportSchedule"]["frequency"]>("weekly");
  const [weekday, setWeekday] = useState("1");
  const [monthDay, setMonthDay] = useState(1);
  const [hour, setHour] = useState("8");
  const [recipients, setRecipients] = useState("");
  const [enabled, setEnabled] = useState(true);
  const create = $api.useMutation("post", "/report-schedules");
  const template = templates.data?.find((t) => t.code === code);
  useEffect(() => {
    if (!code && templates.data?.[0]) {
      setCode(templates.data[0].code);
    }
  }, [code, templates.data]);
  return (
    <Modal
      isOpen
      onOpenChange={(open) => !open && onClose()}
      title="Новое расписание"
      actions={
        <>
          <Button variant="ghost" onPress={onClose}>
            Отмена
          </Button>
          <Button
            variant="primary"
            isDisabled={!ready(template, values) || create.isPending}
            onPress={() =>
              create.mutate(
                {
                  body: {
                    template: code ?? "",
                    format,
                    params: cleanParams(template, values),
                    frequency,
                    weekday: frequency === "weekly" ? Number(weekday) : undefined,
                    monthDay: frequency === "monthly" ? monthDay : undefined,
                    hour: Number(hour),
                    recipients: recipients
                      .split(/[,;\s]+/)
                      .map((x) => x.trim())
                      .filter(Boolean),
                    enabled,
                  },
                },
                {
                  onSuccess: () => {
                    toast.success("Расписание сохранено");
                    onSaved();
                    onClose();
                  },
                },
              )
            }
          >
            Сохранить
          </Button>
        </>
      }
    >
      <div className={s.form}>
        <Select
          label="Шаблон"
          value={code}
          onChange={(v) => {
            setCode(v);
            setValues({});
          }}
          options={(templates.data ?? []).map((t) => ({ id: t.code, label: t.title }))}
        />
        <SegmentedControl
          label="Формат"
          value={format}
          onChange={(v) => setFormat(v as Format)}
          options={[
            { id: "pdf", label: "PDF" },
            { id: "xlsx", label: "XLSX" },
          ]}
        />
        <ParamFields template={template} values={values} onChange={setValues} />
        <SegmentedControl
          label="Периодичность"
          value={frequency}
          onChange={(v) => setFrequency(v as typeof frequency)}
          options={Object.entries(frequencyTitle).map(([id, label]) => ({ id, label }))}
        />
        {frequency === "weekly" && (
          <Select
            label="День недели"
            value={weekday}
            onChange={setWeekday}
            options={weekdays.map((w, i) => ({ id: String(i + 1), label: w }))}
          />
        )}
        {frequency === "monthly" && (
          <NumberField
            label="День месяца"
            value={monthDay}
            minValue={1}
            maxValue={28}
            onChange={(v) => setMonthDay(v ?? 1)}
          />
        )}
        <Select label="Час (по Москве)" value={hour} onChange={setHour} options={hours} />
        <TextArea
          label="Получатели письма (через запятую, до 10)"
          value={recipients}
          onChange={setRecipients}
          placeholder="boss@example.com, ops@example.com"
        />
        <Switch isSelected={enabled} onChange={setEnabled}>
          Расписание включено
        </Switch>
        {create.error && (
          <div className="notice notice-error" role="alert">
            {problemMessage(create.error)}
          </div>
        )}
      </div>
    </Modal>
  );
}
