import { useQueryClient } from "@tanstack/react-query";
import { Link } from "@tanstack/react-router";
import { Plus, RotateCcw, Settings2, X } from "lucide-react";
import { useState } from "react";
import { Guard } from "@/app/Guard";
import { $api, problemMessage, type Schemas } from "@/shared/api/client";
import { dateTime, num, plural } from "@/shared/format";
import { Button, EmptyState, IconButton, Modal, PageHeader, Section, Select, Skeleton } from "@/shared/ui";
import { KpiTile } from "@/shared/ui/KpiTile";
import { toast } from "@/shared/ui/toast";
import s from "../analytics/analytics.module.css";
import { MetricHelp } from "../analytics/MetricHelp";
import { type Level, levelTitles, metricValue, useMetrics } from "../analytics/metrics";
import { SeriesView } from "../analytics/SeriesPanel";

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
  const [editing, setEditing] = useState(false);
  const [adding, setAdding] = useState(false);
  const refresh = () => qc.invalidateQueries({ queryKey: ["get", "/me/dashboard"] });
  const save = $api.useMutation("put", "/me/dashboard", {
    onSuccess: () => void refresh(),
    onError: (e) => toast.error(problemMessage(e)),
  });
  const reset = $api.useMutation("delete", "/me/dashboard", {
    onSuccess: () => {
      toast.success("Дашборд по умолчанию");
      void refresh();
    },
  });
  const widgets = dash.data?.widgets ?? [];
  const store = (next: Widget[]) => save.mutate({ body: { widgets: next } });

  return (
    <>
      <PageHeader
        title="Дашборд"
        subtitle="Логистическая инфраструктура Москвы: показатели, динамика, рейтинги, обновления данных"
        actions={
          <div className={s.actions}>
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
          </div>
        }
      />
      {dash.isPending ? (
        <Skeleton height={300} />
      ) : (
        <div className={s.grid}>
          {widgets.map((w, i) => (
            <WidgetCard
              key={widgetKey(w)}
              w={w}
              onRemove={editing ? () => store(widgets.filter((_, j) => j !== i)) : undefined}
            />
          ))}
        </div>
      )}
      <div className={s.grid} style={{ marginTop: 16 }}>
        <Attention />
        <Updates />
      </div>
      {adding && (
        <AddWidget
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

/** Значения показателей города для плиток. */
function useCity() {
  return $api.useQuery("get", "/metrics/entity", { params: { query: { level: "city", entity: "moscow" } } });
}

function WidgetCard({ w, onRemove }: { w: Widget; onRemove?: () => void }) {
  const metrics = useMetrics();
  const def = w.metric ? metrics.byCode.get(w.metric) : undefined;
  const remove = onRemove && (
    <IconButton label="Убрать виджет" onPress={onRemove}>
      <X size={14} />
    </IconButton>
  );
  if (w.kind === "kpi") {
    return <KpiWidget code={w.metric ?? ""} remove={remove} />;
  }
  if (w.kind === "series") {
    return (
      <div className={s.wide}>
        <Section title={def?.title ?? "Ряд"} actions={remove}>
          <SeriesView code={w.metric ?? ""} height={220} compact />
        </Section>
      </div>
    );
  }
  if (w.kind === "index") {
    return <IndexWidget code={w.index ?? ""} level={(w.level ?? "district") as "district" | "h3_8"} remove={remove} />;
  }
  return <RatingWidget code={w.metric ?? ""} level={(w.level ?? "district") as Level} remove={remove} />;
}

function KpiWidget({ code, remove }: { code: string; remove?: React.ReactNode }) {
  const metrics = useMetrics();
  const city = useCity();
  const def = metrics.byCode.get(code);
  const v = city.data?.find((x) => x.code === code);
  if (city.isPending || !def) {
    return <Skeleton height={120} />;
  }
  const model = code === "territory.population_coverage" || code.startsWith("h3.population");
  return (
    <div style={{ position: "relative" }}>
      <KpiTile
        label={def.title}
        value={v?.value ?? null}
        unit={def.unit === "0–1" ? undefined : def.unit}
        fractionDigits={(v?.value ?? 0) < 100 ? 1 : 0}
        source={def.computedAt ? `Москва · рассчитано ${dateTime(def.computedAt)}` : "Москва"}
        method={`${def.formula}. ${def.method}`}
        model={model}
      />
      {remove && <div style={{ position: "absolute", top: 6, right: 6 }}>{remove}</div>}
    </div>
  );
}

function RatingWidget({ code, level, remove }: { code: string; level: Level; remove?: React.ReactNode }) {
  const metrics = useMetrics();
  const def = metrics.byCode.get(code);
  const q = $api.useQuery("get", "/metrics/{code}", { params: { path: { code }, query: { level, limit: 8 } } });
  const values = (q.data?.values ?? []).filter((v) => v.value > 0);
  return (
    <Section
      title={def?.title ?? "Рейтинг"}
      description={`${levelTitles[level]}${def?.unit ? `, ${def.unit}` : ""}`}
      actions={
        <>
          <MetricHelp def={def} />
          {remove}
        </>
      }
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
              <span className="num">{metricValue(v.value, def, false)}</span>
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

function IndexWidget({ code, level, remove }: { code: string; level: "district" | "h3_8"; remove?: React.ReactNode }) {
  const list = $api.useQuery("get", "/indexes", {}, { staleTime: 60_000 });
  const ix = list.data?.find((x) => x.code === code);
  const q = $api.useQuery("post", "/indexes/{code}/compute", { params: { path: { code } }, body: { level } });
  return (
    <Section
      title={ix?.title ?? "Индекс"}
      description={`${levelTitles[level]}, 0–100`}
      actions={
        <>
          <Link to="/analytics" search={{ tab: "indexes", ix: code, il: level }} className="btn btn-sm">
            Настроить веса
          </Link>
          {remove}
        </>
      }
    >
      {q.isPending ? (
        <Skeleton height={160} />
      ) : (
        <ol className={s.list} aria-label={ix?.title ?? "Индекс"}>
          {(q.data?.scores ?? []).slice(0, 8).map((x) => (
            <li key={x.entity}>
              <EntityLink level={level} entity={x.entity} label={x.label} />
              <span className="num">{num(x.score, 1)}</span>
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
    params: { path: { code: "segment.criticality" }, query: { level: "segment", limit: 5 } },
  });
  const busy = (works.data?.values ?? []).filter((v) => v.value > 0);
  return (
    <Section title="Требуют внимания">
      <h3 className={s.muted}>Объекты с идущими дорожными работами в 500 м</h3>
      {busy.length === 0 ? (
        <p className={s.muted}>Сейчас таких нет.</p>
      ) : (
        <ul className={s.list}>
          {busy.map((v) => (
            <li key={v.entity}>
              <EntityLink level="facility" entity={v.entity} label={v.label} />
              <span className="num">{plural(v.value, "работа", "работы", "работ")}</span>
            </li>
          ))}
        </ul>
      )}
      <h3 className={s.muted}>Районы с наибольшим индексом ограничений</h3>
      <ul className={s.list}>
        {(restr.data?.scores ?? []).slice(0, 5).map((x) => (
          <li key={x.entity}>
            <EntityLink level="district" entity={x.entity} label={x.label} />
            <span className="num">{num(x.score, 1)}</span>
          </li>
        ))}
      </ul>
      <h3 className={s.muted}>Критичные мосты и участки (рост времени доступа при выпадении)</h3>
      <ul className={s.list}>
        {(crit.data?.values ?? []).map((v) => (
          <li key={v.entity}>
            <span>{v.label}</span>
            <span className="num">{num(v.value)} мин</span>
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
        <ul className={s.list} aria-label="Обновления данных">
          {(q.data ?? []).map((u) => (
            <li key={`${u.source}:${u.finishedAt}`}>
              <span>
                {u.title}
                <span className={s.muted}> · {u.publisher}</span>
              </span>
              <span className={s.muted}>
                {dateTime(u.finishedAt)}
                {u.records !== undefined && ` · ${num(u.records)} записей`}
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

function AddWidget({ onAdd, onClose }: { onAdd: (w: Widget) => void; onClose: () => void }) {
  const metrics = useMetrics();
  const indexes = $api.useQuery("get", "/indexes", {}, { staleTime: 60_000 });
  const [kind, setKind] = useState<Widget["kind"]>("kpi");
  const [metric, setMetric] = useState<string | null>(null);
  const [index, setIndex] = useState<string | null>(null);
  const [level, setLevel] = useState<Level>("district");
  const all = metrics.data ?? [];
  const metricOptions =
    kind === "kpi"
      ? all.filter((m) => m.levels.includes("city") && !m.code.startsWith("city."))
      : kind === "series"
        ? all.filter((m) => m.code.startsWith("city."))
        : all.filter((m) => m.levels.includes(level) && !m.code.startsWith("city."));
  const levels: Level[] =
    kind === "index" ? ["district", "h3_8"] : ["district", "okrug", "facility", "h3_8", "segment"];
  const ok = kind === "index" ? !!index : !!metric && metricOptions.some((m) => m.code === metric);
  return (
    <Modal
      isOpen
      onOpenChange={(o) => !o && onClose()}
      title="Добавить виджет"
      actions={
        <>
          <Button onPress={onClose}>Отмена</Button>
          <Button
            variant="primary"
            isDisabled={!ok}
            onPress={() =>
              onAdd(
                kind === "index"
                  ? { kind, index: index ?? "", level }
                  : kind === "rating"
                    ? { kind, metric: metric ?? "", level }
                    : { kind, metric: metric ?? "" },
              )
            }
          >
            Добавить
          </Button>
        </>
      }
    >
      <div className={s.toolbar} style={{ flexDirection: "column", alignItems: "stretch" }}>
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
            options={(indexes.data ?? []).map((x) => ({ id: x.code, label: x.title }))}
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
