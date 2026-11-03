import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import {
  ChevronLeft,
  ChevronRight,
  GripVertical,
  Plus,
  RotateCcw,
  Settings2,
  SlidersHorizontal,
  X,
} from "lucide-react";
import { useState } from "react";
import { Guard } from "@/app/Guard";
import { $api, problemMessage, type Schemas } from "@/shared/api/client";
import { dateTime, num, plural } from "@/shared/format";
import {
  Button,
  EmptyState,
  IconButton,
  iconButtonClass,
  Modal,
  PageHeader,
  Section,
  Select,
  Skeleton,
} from "@/shared/ui";
import { KpiTile } from "@/shared/ui/KpiTile";
import { Tooltip } from "@/shared/ui/overlays";
import { toast } from "@/shared/ui/toast";
import { MetricHelp } from "../analytics/MetricHelp";
import { type Level, levelTitles, metricValue, useMetrics } from "../analytics/metrics";
import { SeriesView } from "../analytics/SeriesPanel";
import s from "./dashboard.module.css";

type Widget = Schemas["Widget"];

export function DashboardPage() {
  return (
    <Guard permission="analytics.view">
      <Dashboard />
    </Guard>
  );
}

const widgetKey = (w: Widget) => `${w.kind}:${w.metric ?? ""}:${w.index ?? ""}:${w.level ?? ""}`;

function Dashboard() {
  const qc = useQueryClient();
  const dash = $api.useQuery("get", "/me/dashboard");
  const metrics = useMetrics();
  const [editing, setEditing] = useState(false);
  const [adding, setAdding] = useState(false);
  const refresh = () => qc.invalidateQueries({ queryKey: ["get", "/me/dashboard"] });
  const save = $api.useMutation("put", "/me/dashboard", {
    onError: (e) => {
      toast.error(problemMessage(e));
      void refresh();
    },
  });
  const reset = $api.useMutation("delete", "/me/dashboard", {
    onSuccess: () => {
      toast.success("Дашборд по умолчанию");
      void refresh();
    },
  });
  const widgets = dash.data?.widgets ?? [];
  // Новый набор сразу записывается в кеш запроса: порядок не откатывается, пока сервер сохраняет;
  // при ошибке набор перечитывается.
  const store = (next: Widget[]) => {
    qc.setQueriesData<typeof dash.data>({ queryKey: ["get", "/me/dashboard"] }, (old) =>
      old ? { ...old, widgets: next, own: true } : old,
    );
    save.mutate({ body: { widgets: next } });
  };
  const move = (from: number, to: number) => {
    if (from === to || to < 0 || to >= widgets.length) {
      return;
    }
    const next = [...widgets];
    const [w] = next.splice(from, 1);
    if (w) {
      next.splice(to, 0, w);
      store(next);
    }
  };
  // Перетаскивание в режиме настройки: откуда взят виджет и над каким он сейчас.
  const [dragFrom, setDragFrom] = useState<number | null>(null);
  const [dragOver, setDragOver] = useState<number | null>(null);
  const endDrag = () => {
    setDragFrom(null);
    setDragOver(null);
  };

  return (
    <>
      <PageHeader
        title="Дашборд"
        subtitle="Логистическая инфраструктура Москвы: показатели, динамика, рейтинги, обновления данных"
        actions={
          <>
            {editing && (
              <>
                <Button size="sm" onPress={() => setAdding(true)} isDisabled={widgets.length >= 16}>
                  <Plus size={14} aria-hidden /> Добавить виджет
                </Button>
                {dash.data?.own && (
                  <Button size="sm" variant="ghost" onPress={() => reset.mutate({})}>
                    <RotateCcw size={14} aria-hidden /> По умолчанию
                  </Button>
                )}
              </>
            )}
            <Button size="sm" variant={editing ? "primary" : undefined} onPress={() => setEditing(!editing)}>
              <Settings2 size={14} aria-hidden /> {editing ? "Готово" : "Настроить"}
            </Button>
          </>
        }
      />
      {editing && (
        <p className={s.hint}>
          Порядок карточек меняется перетаскиванием или стрелками на панели над карточкой. Изменения сохраняются сразу.
        </p>
      )}
      {dash.isPending ? (
        <Skeleton height={300} />
      ) : (
        <div className={`${s.grid}${editing ? ` ${s.editingGrid}` : ""}`}>
          {widgets.map((w, i) => (
            // biome-ignore lint/a11y/noStaticElementInteractions: перетаскивание мышью дублируется кнопками «раньше/позже»
            <div
              key={widgetKey(w)}
              className={[
                s.cell,
                w.kind === "series" ? s.wide : "",
                editing ? s.editing : "",
                dragFrom === i ? s.dragging : "",
                dragOver === i && dragFrom !== i ? s.dropTarget : "",
              ].join(" ")}
              draggable={editing}
              onDragStart={(e) => {
                e.dataTransfer.effectAllowed = "move";
                e.dataTransfer.setData("text/plain", String(i));
                setDragFrom(i);
              }}
              onDragOver={(e) => {
                if (dragFrom !== null) {
                  e.preventDefault();
                  setDragOver(i);
                }
              }}
              onDrop={(e) => {
                e.preventDefault();
                if (dragFrom !== null) {
                  move(dragFrom, i);
                }
                endDrag();
              }}
              onDragEnd={endDrag}
            >
              <WidgetCard w={w} />
              {editing && (
                <EditTools
                  title={widgetTitle(w, metrics.byCode)}
                  first={i === 0}
                  last={i === widgets.length - 1}
                  onMove={(d) => move(i, i + d)}
                  onRemove={() => store(widgets.filter((_, j) => j !== i))}
                />
              )}
            </div>
          ))}
        </div>
      )}
      <div className={s.grid}>
        <div className={`${s.cell} ${s.wide}`}>
          <Attention />
        </div>
        <div className={`${s.cell} ${s.wide}`}>
          <Updates />
        </div>
      </div>
      {adding && (
        <AddWidget
          present={new Set(widgets.map(widgetKey))}
          onClose={() => setAdding(false)}
          onAdd={(w) => {
            setAdding(false);
            store([...widgets, w]);
          }}
        />
      )}
    </>
  );
}

/**
 * Кнопки виджета в режиме настройки — панелью на верхней кромке карточки, чтобы не теснить
 * заголовок: перетаскивание, сдвиг раньше и позже (с клавиатуры), удаление.
 */
function EditTools({
  title,
  first,
  last,
  onMove,
  onRemove,
}: {
  title: string;
  first: boolean;
  last: boolean;
  onMove: (delta: number) => void;
  onRemove: () => void;
}) {
  return (
    <div className={s.tools} role="toolbar" aria-label={`Виджет «${title}»`}>
      <span className={s.grip} aria-hidden>
        <GripVertical size={14} />
      </span>
      <IconButton label="Переместить раньше" isDisabled={first} onPress={() => onMove(-1)}>
        <ChevronLeft size={15} />
      </IconButton>
      <IconButton label="Переместить позже" isDisabled={last} onPress={() => onMove(1)}>
        <ChevronRight size={15} />
      </IconButton>
      <IconButton label="Убрать виджет" onPress={onRemove}>
        <X size={15} />
      </IconButton>
    </div>
  );
}

const kindTitles: Record<Widget["kind"], string> = {
  kpi: "Плитка",
  series: "Ряд",
  rating: "Рейтинг",
  index: "Индекс",
};

/** Подпись виджета для панели настройки: вид и название показателя. */
function widgetTitle(w: Widget, defs: Map<string, { title: string }>): string {
  const name = w.metric ? (defs.get(w.metric)?.title ?? w.metric) : (w.index ?? "");
  return `${kindTitles[w.kind]}: ${name}`;
}

/** Значения показателей города для плиток. */
function useCity() {
  return $api.useQuery("get", "/metrics/entity", { params: { query: { level: "city", entity: "moscow" } } });
}

function WidgetCard({ w }: { w: Widget }) {
  const metrics = useMetrics();
  const def = w.metric ? metrics.byCode.get(w.metric) : undefined;
  if (w.kind === "kpi") {
    return <KpiWidget code={w.metric ?? ""} />;
  }
  if (w.kind === "series") {
    return (
      <Section title={def?.title ?? "Ряд"}>
        <SeriesView code={w.metric ?? ""} height={220} compact />
      </Section>
    );
  }
  if (w.kind === "index") {
    return <IndexWidget code={w.index ?? ""} level={(w.level ?? "district") as "district" | "h3_8"} />;
  }
  return <RatingWidget code={w.metric ?? ""} level={(w.level ?? "district") as Level} />;
}

function KpiWidget({ code }: { code: string }) {
  const metrics = useMetrics();
  const city = useCity();
  const def = metrics.byCode.get(code);
  const v = city.data?.find((x) => x.code === code);
  if (city.isPending || !def) {
    return <Skeleton height={120} />;
  }
  const model = code === "territory.population_coverage" || code.startsWith("h3.population");
  return (
    <KpiTile
      label={def.title}
      value={v?.value ?? null}
      unit={def.unit === "0–1" ? undefined : def.unit}
      fractionDigits={(v?.value ?? 0) < 100 ? 1 : 0}
      source={def.computedAt ? `Москва · рассчитано ${dateTime(def.computedAt)}` : "Москва"}
      method={`${def.formula}. ${def.method}`}
      model={model}
    />
  );
}

function RatingWidget({ code, level }: { code: string; level: Level }) {
  const metrics = useMetrics();
  const def = metrics.byCode.get(code);
  const q = $api.useQuery("get", "/metrics/{code}", { params: { path: { code }, query: { level, limit: 8 } } });
  const values = (q.data?.values ?? []).filter((v) => v.value > 0);
  return (
    <Section
      title={def?.title ?? "Рейтинг"}
      description={`${levelTitles[level]}${def?.unit ? `, ${def.unit}` : ""}`}
      actions={<MetricHelp def={def} />}
    >
      {q.isPending ? (
        <Skeleton height={160} />
      ) : values.length === 0 ? (
        <p className={s.muted}>Значений больше нуля нет.</p>
      ) : (
        <ol className={s.list} aria-label={`Рейтинг: ${def?.title ?? ""}`}>
          {values.map((v) => (
            <li key={v.entity}>
              <EntityLink level={level} entity={v.entity} label={v.label} />
              <span className={s.value}>{metricValue(v.value, def, false)}</span>
            </li>
          ))}
        </ol>
      )}
    </Section>
  );
}

function EntityLink({ level, entity, label }: { level: string; entity: string; label: string }) {
  if (level === "district" || level === "okrug") {
    return (
      <Link to="/territories/$territoryId" params={{ territoryId: entity }}>
        {label}
      </Link>
    );
  }
  if (level === "facility") {
    return (
      <Link to="/facilities/$facilityId" params={{ facilityId: entity }}>
        {label}
      </Link>
    );
  }
  return <span>{label}</span>;
}

function IndexWidget({ code, level }: { code: string; level: "district" | "h3_8" }) {
  const list = $api.useQuery("get", "/indexes", {}, { staleTime: 60_000 });
  const ix = list.data?.find((x) => x.code === code);
  const q = $api.useQuery("post", "/indexes/{code}/compute", { params: { path: { code } }, body: { level } });
  return (
    <Section
      title={ix?.title ?? "Индекс"}
      description={`${levelTitles[level]}, 0–100`}
      actions={
        <Tooltip content="Настроить веса">
          <Link
            to="/analytics"
            search={{ tab: "indexes", ix: code, il: level }}
            className={iconButtonClass}
            aria-label="Настроить веса"
          >
            <SlidersHorizontal size={15} aria-hidden />
          </Link>
        </Tooltip>
      }
    >
      {q.isPending ? (
        <Skeleton height={160} />
      ) : (
        <ol className={s.list} aria-label={ix?.title ?? "Индекс"}>
          {(q.data?.scores ?? []).slice(0, 8).map((x) => (
            <li key={x.entity}>
              <EntityLink level={level} entity={x.entity} label={x.label} />
              <span className={s.value}>{num(x.score, 1)}</span>
            </li>
          ))}
        </ol>
      )}
    </Section>
  );
}

/** Требуют внимания: объекты с работами рядом, районы с наибольшими ограничениями, критичные элементы сети. */
function Attention() {
  const works = $api.useQuery("get", "/metrics/{code}", {
    params: { path: { code: "facility.works_500m" }, query: { level: "facility", limit: 5 } },
  });
  const restr = $api.useQuery("post", "/indexes/{code}/compute", {
    params: { path: { code: "index.restrictions" } },
    body: { level: "district" },
  });
  const crit = $api.useQuery("get", "/metrics/{code}", {
    params: { path: { code: "segment.criticality" }, query: { level: "segment", limit: 20 } },
  });
  const busy = (works.data?.values ?? []).filter((v) => v.value > 0);
  // Участки одной улицы (две проезжие части, соседние перегоны) — одной строкой с наибольшим ростом.
  const critical = [...new Map([...(crit.data?.values ?? [])].reverse().map((v) => [v.label, v])).values()]
    .sort((a, b) => b.value - a.value)
    .slice(0, 5);
  return (
    <Section title="Требуют внимания">
      <h3 className={s.subhead}>Объекты с идущими дорожными работами в 500 м</h3>
      {busy.length === 0 ? (
        <p className={s.muted}>Сейчас таких нет.</p>
      ) : (
        <ul className={s.list}>
          {busy.map((v) => (
            <li key={v.entity}>
              <EntityLink level="facility" entity={v.entity} label={v.label} />
              <span className={s.value}>{plural(v.value, "работа", "работы", "работ")}</span>
            </li>
          ))}
        </ul>
      )}
      <h3 className={s.subhead}>Районы с наибольшим индексом ограничений</h3>
      <ul className={s.list}>
        {(restr.data?.scores ?? []).slice(0, 5).map((x) => (
          <li key={x.entity}>
            <EntityLink level="district" entity={x.entity} label={x.label} />
            <span className={s.value}>{num(x.score, 1)}</span>
          </li>
        ))}
      </ul>
      <h3 className={s.subhead}>Критичные мосты и участки — рост времени доступа при выпадении</h3>
      <ul className={s.list}>
        {critical.map((v) => (
          <li key={v.entity}>
            <span>{v.label}</span>
            <span className={s.value}>{num(v.value)} мин</span>
          </li>
        ))}
      </ul>
    </Section>
  );
}

function Updates() {
  const q = $api.useQuery("get", "/data/updates", { params: { query: { limit: 8 } } });
  return (
    <Section title="Обновления данных">
      {q.isPending ? (
        <Skeleton height={160} />
      ) : (q.data ?? []).length === 0 ? (
        <EmptyState title="Обновлений нет">Загрузки ещё не выполнялись.</EmptyState>
      ) : (
        <ul className={s.updates} aria-label="Обновления данных">
          {(q.data ?? []).map((u) => (
            <li key={`${u.source}:${u.finishedAt}`} className={s.update}>
              <span>
                {u.title}
                <span className={s.muted}> · {u.publisher}</span>
              </span>
              <span className={s.muted}>
                {dateTime(u.finishedAt)}
                {u.records !== undefined && ` · ${plural(u.records, "запись", "записи", "записей")}`}
              </span>
            </li>
          ))}
        </ul>
      )}
    </Section>
  );
}

const kinds = [
  { id: "kpi", label: "Плитка показателя Москвы" },
  { id: "series", label: "Ряд с прогнозом" },
  { id: "rating", label: "Рейтинг показателя" },
  { id: "index", label: "Рейтинг индекса" },
];

function AddWidget({
  present,
  onAdd,
  onClose,
}: {
  /** Ключи виджетов, уже стоящих на дашборде: повторно не добавляются. */
  present: Set<string>;
  onAdd: (w: Widget) => void;
  onClose: () => void;
}) {
  const metrics = useMetrics();
  const indexes = $api.useQuery("get", "/indexes", {}, { staleTime: 60_000 });
  const [kind, setKind] = useState<Widget["kind"]>("kpi");
  const [metric, setMetric] = useState<string | null>(null);
  const [index, setIndex] = useState<string | null>(null);
  const [level, setLevel] = useState<Level>("district");
  const all = metrics.data ?? [];
  const candidate = (): Widget =>
    kind === "index"
      ? { kind, index: index ?? "", level }
      : kind === "rating"
        ? { kind, metric: metric ?? "", level }
        : { kind, metric: metric ?? "" };
  const isPresent = (w: Widget) => present.has(widgetKey(w));
  const metricOptions = (
    kind === "kpi"
      ? all.filter((m) => m.levels.includes("city") && !m.code.startsWith("city."))
      : kind === "series"
        ? all.filter((m) => m.code.startsWith("city."))
        : all.filter((m) => m.levels.includes(level) && !m.code.startsWith("city."))
  ).filter((m) => !isPresent(kind === "rating" ? { kind, metric: m.code, level } : { kind, metric: m.code }));
  const indexOptions = (indexes.data ?? []).filter((x) => !isPresent({ kind: "index", index: x.code, level }));
  const levels: Level[] =
    kind === "index" ? ["district", "h3_8"] : ["district", "okrug", "facility", "h3_8", "segment"];
  const ok =
    kind === "index"
      ? indexOptions.some((x) => x.code === index)
      : !!metric && metricOptions.some((m) => m.code === metric);
  return (
    <Modal
      isOpen
      onOpenChange={(o) => !o && onClose()}
      title="Добавить виджет"
      actions={
        <>
          <Button onPress={onClose}>Отмена</Button>
          <Button variant="primary" isDisabled={!ok} onPress={() => onAdd(candidate())}>
            Добавить
          </Button>
        </>
      }
    >
      <div className={s.form}>
        <Select label="Вид" options={kinds} value={kind} onChange={(k) => setKind(k as Widget["kind"])} />
        {(kind === "rating" || kind === "index") && (
          <Select
            label="Уровень"
            options={levels.map((l) => ({ id: l, label: levelTitles[l] }))}
            value={level}
            onChange={(l) => setLevel(l as Level)}
          />
        )}
        {kind === "index" ? (
          <Select
            label="Индекс"
            options={indexOptions.map((x) => ({ id: x.code, label: x.title }))}
            value={index}
            onChange={setIndex}
          />
        ) : (
          <Select
            label="Показатель"
            options={metricOptions.map((m) => ({ id: m.code, label: m.title }))}
            value={metric}
            onChange={setMetric}
          />
        )}
      </div>
    </Modal>
  );
}
