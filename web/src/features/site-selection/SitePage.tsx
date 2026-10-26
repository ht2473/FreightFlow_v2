import { useQueryClient } from "@tanstack/react-query";
import type { Feature, FeatureCollection } from "geojson";
import { ArrowDown, ArrowUp, Play, Save, Trash2 } from "lucide-react";
import type { LayerSpecification, Map as MapLibreMap } from "maplibre-gl";
import { useEffect, useMemo, useState } from "react";
import { Guard } from "@/app/Guard";
import { $api, problemMessage, type Schemas } from "@/shared/api/client";
import { dateTime, num } from "@/shared/format";
import { color } from "@/shared/map/dataLayers";
import { Badge, Button, Checkbox, cellClass, IconButton, Select, TextField, tableClass } from "@/shared/ui";
import { NumberField } from "@/shared/ui/fields";
import { SegmentedControl } from "@/shared/ui/navigation";
import { toast } from "@/shared/ui/toast";
import { extentOf, MainMap, type Overlay, type Palette, usePalette, useScenarioStyle } from "../scenarios/ScenarioMap";
import s from "../scenarios/scenario.module.css";
import { pairKey, pairs, pairwiseMatrix, saatyScale, scaleId, scaleValue } from "./pairwise";
import site from "./site.module.css";

type Params = Schemas["SiteParams"];
type Result = Schemas["SiteResult"];
type CriterionDef = Schemas["SiteCriterionDef"];

/** Критерии по умолчанию: выезд за МКАД, спрос, промзоны, ограничения сети. */
const defaults: Record<string, number> = {
  "h3.access_mkad_min": 3,
  "h3.population_5km": 2,
  "h3.share_industrial": 2,
  "h3.restricted_km": 1,
};
const defaultExclusions = ["residential", "heritage", "parks", "green", "water"];
const context = new Set(["rings"]);

interface CriterionState {
  on: boolean;
  benefit: boolean;
  weight: number;
}

export function SitePage() {
  return (
    <Guard permission="scenarios.manage">
      <SiteSelection />
    </Guard>
  );
}

function SiteSelection() {
  const qc = useQueryClient();
  const catalog = $api.useQuery("get", "/site-selection/catalog", {}, { staleTime: Number.POSITIVE_INFINITY });
  const runs = $api.useQuery("get", "/site-selection/runs");
  const evaluate = $api.useMutation("post", "/site-selection/evaluate");
  const save = $api.useMutation("post", "/site-selection/runs");
  const del = $api.useMutation("delete", "/site-selection/runs/{runId}");

  const [resolution, setResolution] = useState<8 | 9>(8);
  const [criteria, setCriteria] = useState<Record<string, CriterionState>>({});
  const [weighting, setWeighting] = useState<"direct" | "ahp">("direct");
  const [judgments, setJudgments] = useState<Record<string, number>>({});
  const [exclusions, setExclusions] = useState<Set<string>>(new Set(defaultExclusions));
  const [share, setShare] = useState(50);
  const [spacing, setSpacing] = useState(2);
  const [top, setTop] = useState(15);
  const [result, setResult] = useState<Result | null>(null);
  const [used, setUsed] = useState<string[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [name, setName] = useState("");
  const [runId, setRunId] = useState<string | null>(null);
  const [map, setMap] = useState<MapLibreMap | null>(null);

  // Начальное состояние критериев — из каталога.
  useEffect(() => {
    if (!catalog.data || Object.keys(criteria).length > 0) {
      return;
    }
    setCriteria(
      Object.fromEntries(
        catalog.data.criteria.map((c) => [
          c.metric,
          { on: c.metric in defaults, benefit: c.benefit, weight: defaults[c.metric] ?? 1 },
        ]),
      ),
    );
  }, [catalog.data, criteria]);

  const active = (catalog.data?.criteria ?? []).filter((c) => criteria[c.metric]?.on);
  const metrics = active.map((c) => c.metric);

  const params = (): Params => ({
    resolution,
    criteria: active.map((c) => ({
      metric: c.metric,
      benefit: criteria[c.metric]?.benefit ?? c.benefit,
      weight: criteria[c.metric]?.weight ?? 1,
    })),
    weighting,
    pairwise: weighting === "ahp" ? pairwiseMatrix(metrics, judgments) : undefined,
    exclusions: [...exclusions],
    exclusionShare: share,
    top,
    spacingKm: spacing,
  });

  const run = () => {
    const p = params();
    evaluate.mutate(
      { body: p },
      {
        onSuccess: (r) => {
          setResult(r);
          setUsed(metrics);
          setSelected(new Set(r.top.slice(0, 3).map((c) => c.h3)));
        },
      },
    );
  };

  const load = (r: Schemas["SiteRun"]) => {
    const p = r.params;
    setRunId(r.id);
    setName(r.name);
    setResolution((p.resolution ?? 8) as 8 | 9);
    setWeighting(p.weighting ?? "direct");
    setExclusions(new Set(p.exclusions ?? []));
    setShare(p.exclusionShare ?? 50);
    setSpacing(p.spacingKm ?? 0);
    setTop(p.top ?? 20);
    const next: Record<string, CriterionState> = {};
    for (const c of catalog.data?.criteria ?? []) {
      const pc = p.criteria.find((x) => x.metric === c.metric);
      next[c.metric] = { on: !!pc, benefit: pc?.benefit ?? c.benefit, weight: pc?.weight ?? 1 };
    }
    setCriteria(next);
    const ms = p.criteria.map((c) => c.metric);
    const js: Record<string, number> = {};
    for (const [i, a] of ms.entries()) {
      for (const [j, b] of ms.entries()) {
        const v = p.pairwise?.[i]?.[j];
        if (i < j && v !== undefined) {
          js[pairKey(a, b)] = v;
        }
      }
    }
    setJudgments(js);
    setResult(null);
  };

  const palette = usePalette();
  const cells = $api.useQuery(
    "get",
    "/site-selection/cells",
    { params: { query: { resolution } } },
    { staleTime: Number.POSITIVE_INFINITY },
  );
  const overlay = useMemo(
    () => siteOverlay(cells.data as FeatureCollection | undefined, result, selected, palette),
    [cells.data, result, selected, palette],
  );
  const style = useScenarioStyle(context, overlay);
  const fit = useMemo(() => extentOf((result?.top ?? []).map((c) => [c.lon, c.lat])), [result]);

  const defs = new Map((catalog.data?.criteria ?? []).map((c) => [c.metric, c]));
  const usedDefs = used.map((m) => defs.get(m)).filter((d): d is CriterionDef => !!d);
  const canRun = active.length >= 2 && !evaluate.isPending;

  return (
    <div className={s.workspace}>
      <aside className={s.panel} aria-label="Оценка локации">
        <div className={s.titleRow}>
          <h1 className={s.title}>Оценка локации</h1>
        </div>
        <p className={s.muted}>
          Поиск мест под логистический объект: ячейки H3 Москвы ранжируются методом TOPSIS по выбранным критериям; веса
          — напрямую или попарным сравнением (метод анализа иерархий).
        </p>

        <section className={s.block}>
          <h2>Сохранённые оценки</h2>
          <Select
            label="Открыть оценку"
            options={(runs.data ?? []).map((r) => ({ id: r.id, label: `${r.name} · ${dateTime(r.createdAt)}` }))}
            value={runId}
            onChange={(id) => {
              const r = runs.data?.find((x) => x.id === id);
              if (r) {
                load(r);
              }
            }}
          />
        </section>

        <section className={s.block}>
          <h2>Ячейки</h2>
          <SegmentedControl
            label="Разрешение ячеек H3"
            value={String(resolution)}
            onChange={(v) => setResolution(Number(v) as 8 | 9)}
            options={[
              { id: "8", label: "≈ 0,74 км² (разрешение 8)" },
              { id: "9", label: "≈ 0,11 км² (9)" },
            ]}
          />
        </section>

        <section className={s.block}>
          <h2>Критерии</h2>
          <ul className={s.list}>
            {(catalog.data?.criteria ?? []).map((c) => {
              const st = criteria[c.metric];
              if (!st) {
                return null;
              }
              const set = (patch: Partial<CriterionState>) =>
                setCriteria((all) => ({ ...all, [c.metric]: { ...st, ...patch } }));
              return (
                <li key={c.metric} className={site.criterion}>
                  <Checkbox isSelected={st.on} onChange={(on) => set({ on })}>
                    <span className={site.criterionText}>
                      <span>{c.title}</span>
                      <span className={s.itemNote}>{c.note}</span>
                    </span>
                  </Checkbox>
                  {st.on && (
                    <div className={site.criterionControls}>
                      <IconButton
                        label={st.benefit ? `${c.title}: больше — лучше` : `${c.title}: меньше — лучше`}
                        onPress={() => set({ benefit: !st.benefit })}
                      >
                        {st.benefit ? <ArrowUp size={15} /> : <ArrowDown size={15} />}
                      </IconButton>
                      {weighting === "direct" && (
                        <NumberField
                          label={`Вес: ${c.title}`}
                          value={st.weight}
                          onChange={(v) => set({ weight: v ?? 0 })}
                          minValue={0}
                          maxValue={10}
                          step={1}
                        />
                      )}
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
        </section>

        <section className={s.block}>
          <h2>Веса</h2>
          <SegmentedControl
            label="Способ задания весов"
            value={weighting}
            onChange={(v) => setWeighting(v as "direct" | "ahp")}
            options={[
              { id: "direct", label: "Прямые" },
              { id: "ahp", label: "Попарное сравнение (МАИ)" },
            ]}
          />
          {weighting === "ahp" && (
            <div className={s.block}>
              {pairs(active).map(([a, b]) => (
                <Select
                  key={pairKey(a.metric, b.metric)}
                  label={`${a.title} — ${b.title}`}
                  options={saatyScale.map((x) => ({ id: scaleId(x.value), label: x.label }))}
                  value={scaleId(judgments[pairKey(a.metric, b.metric)] ?? 1)}
                  onChange={(id) => {
                    if (id) {
                      setJudgments((js) => ({ ...js, [pairKey(a.metric, b.metric)]: scaleValue(id) }));
                    }
                  }}
                />
              ))}
              {active.length < 2 && <p className={s.muted}>Выберите хотя бы два критерия.</p>}
            </div>
          )}
        </section>

        <section className={s.block}>
          <h2>Исключающие зоны</h2>
          <div className={s.checks}>
            {(catalog.data?.exclusions ?? []).map((e) => (
              <Checkbox
                key={e.code}
                isSelected={exclusions.has(e.code)}
                onChange={(on) =>
                  setExclusions((all) => {
                    const next = new Set(all);
                    if (on) {
                      next.add(e.code);
                    } else {
                      next.delete(e.code);
                    }
                    return next;
                  })
                }
              >
                {e.title}
              </Checkbox>
            ))}
          </div>
          <div className={site.numbers}>
            <NumberField
              label="Ячейка исключается от"
              unit="%"
              value={share}
              onChange={(v) => setShare(v ?? 50)}
              minValue={5}
              maxValue={100}
              step={5}
            />
            <NumberField
              label="Кандидаты не ближе"
              unit="км"
              value={spacing}
              onChange={(v) => setSpacing(v ?? 0)}
              minValue={0}
              maxValue={20}
              step={0.5}
            />
            <NumberField
              label="Кандидатов"
              value={top}
              onChange={(v) => setTop(v ?? 15)}
              minValue={1}
              maxValue={50}
              step={1}
            />
          </div>
        </section>

        {evaluate.error && (
          <div className="notice notice-error" role="alert">
            {problemMessage(evaluate.error)}
          </div>
        )}
        <Button variant="primary" onPress={run} isDisabled={!canRun}>
          <Play size={15} aria-hidden /> {evaluate.isPending ? "Оцениваем…" : "Оценить"}
        </Button>

        {result && (
          <ResultBlock
            result={result}
            defs={usedDefs}
            selected={selected}
            onSelect={setSelected}
            onFocus={(c) =>
              map?.flyTo({ center: [c.lon, c.lat], zoom: Math.max(map.getZoom(), resolution === 8 ? 12 : 13.5) })
            }
          />
        )}

        {result && (
          <section className={s.block}>
            <h2>Сохранить</h2>
            <TextField label="Название оценки" value={name} onChange={setName} />
            <div className={s.actions}>
              <Button
                size="sm"
                isDisabled={!name.trim() || save.isPending}
                onPress={() =>
                  save.mutate(
                    { body: { name: name.trim(), params: params() } },
                    {
                      onSuccess: (r) => {
                        setRunId(r.id);
                        toast.success("Оценка сохранена");
                        void qc.invalidateQueries({ queryKey: ["get", "/site-selection/runs"] });
                      },
                      onError: (e) => toast.error(problemMessage(e)),
                    },
                  )
                }
              >
                <Save size={14} aria-hidden /> Сохранить
              </Button>
              {runId && (
                <Button
                  size="sm"
                  variant="danger"
                  onPress={() =>
                    del.mutate(
                      { params: { path: { runId } } },
                      {
                        onSuccess: () => {
                          setRunId(null);
                          toast.success("Оценка удалена");
                          void qc.invalidateQueries({ queryKey: ["get", "/site-selection/runs"] });
                        },
                      },
                    )
                  }
                >
                  <Trash2 size={14} aria-hidden /> Удалить сохранённую
                </Button>
              )}
            </div>
          </section>
        )}
      </aside>
      <div className={s.maps}>
        <MainMap
          style={style}
          label="Пригодность"
          fit={fit}
          onMap={setMap}
          legend={
            <>
              <span className={site.ramp} aria-hidden />
              <span>ниже → выше пригодность</span>
              <span>
                <span className={s.swatch} style={{ background: "var(--n9)" }} /> исключено
              </span>
              <span>
                <span className={s.swatch} style={{ background: "var(--acc)" }} /> кандидат
              </span>
            </>
          }
        />
      </div>
    </div>
  );
}

function ResultBlock({
  result,
  defs,
  selected,
  onSelect,
  onFocus,
}: {
  result: Result;
  defs: CriterionDef[];
  selected: Set<string>;
  onSelect: (s: Set<string>) => void;
  onFocus: (c: Schemas["SiteCell"]) => void;
}) {
  const compare = result.top.filter((c) => selected.has(c.h3));
  return (
    <>
      <section className={s.block}>
        <h2>Итог</h2>
        <p className={s.muted}>
          Оценено {num(result.candidates)} ячеек из {num(result.cells)}; исключено {num(result.excluded.length)} по
          исключающим зонам.
        </p>
        {result.ahp && (
          <p>
            Согласованность суждений: CR = {num(result.ahp.cr, 3)}{" "}
            {result.ahp.consistent ? (
              <Badge tone="ok">согласованы</Badge>
            ) : (
              <Badge tone="warn">пересмотрите суждения (CR &gt; 0,1)</Badge>
            )}
          </p>
        )}
        <table className={tableClass}>
          <thead>
            <tr>
              <th scope="col">Критерий</th>
              <th scope="col" className={cellClass.right}>
                Вес
              </th>
              <th scope="col" className={cellClass.right}>
                Устойчивость
              </th>
            </tr>
          </thead>
          <tbody>
            {defs.map((d, i) => {
              const sens = result.sensitivity[i];
              return (
                <tr key={d.metric}>
                  <td>
                    {d.title}
                    {(result.missing[i] ?? 0) > 0 && (
                      <span className={s.itemNote}>нет данных у {num((result.missing[i] ?? 0) * 100, 1)} % ячеек</span>
                    )}
                  </td>
                  <td className={`${cellClass.right} num`}>{num((result.weights[i] ?? 0) * 100, 1)} %</td>
                  <td className={`${cellClass.right} num`}>
                    {sens ? `ρ ${num(sens.spearman, 3)} · ${num(sens.topKept * 100)} %` : "—"}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
        <p className={s.muted}>
          Устойчивость — при изменении веса на ±20 %: ранговая корреляция Спирмена и доля лучших кандидатов, оставшихся
          среди лучших.
        </p>
      </section>

      <section className={s.block}>
        <h2>Кандидаты</h2>
        <ol className={s.list}>
          {result.top.map((c) => (
            <li key={c.h3} className={site.candidate}>
              <Checkbox
                isSelected={selected.has(c.h3)}
                onChange={(on) => {
                  const next = new Set(selected);
                  if (on) {
                    next.add(c.h3);
                  } else {
                    next.delete(c.h3);
                  }
                  onSelect(next);
                }}
              >
                <span className={s.itemTitle}>
                  {c.rank}. {c.district || "Ячейка"} — {num(c.score, 3)}
                </span>
              </Checkbox>
              <span className={site.bar} role="img" aria-label={contributionText(c, defs)}>
                {c.contributions.map((v, i) => (
                  <span
                    key={defs[i]?.metric ?? i}
                    style={{ width: `${v * 100}%`, background: `var(--cat${(i % 8) + 1})` }}
                  />
                ))}
              </span>
              <Button size="sm" variant="ghost" onPress={() => onFocus(c)}>
                На карте
              </Button>
            </li>
          ))}
        </ol>
        <div className={site.barLegend}>
          {defs.map((d, i) => (
            <span key={d.metric}>
              <span className={s.swatch} style={{ background: `var(--cat${(i % 8) + 1})` }} />
              {d.title}
            </span>
          ))}
        </div>
      </section>

      {compare.length > 0 && (
        <section className={s.block}>
          <h2>Сравнение кандидатов</h2>
          <table className={tableClass}>
            <thead>
              <tr>
                <th scope="col">Критерий</th>
                {compare.map((c) => (
                  <th key={c.h3} scope="col" className={cellClass.right}>
                    № {c.rank}
                  </th>
                ))}
              </tr>
            </thead>
            <tbody>
              {defs.map((d, i) => (
                <tr key={d.metric}>
                  <td>
                    {d.title}, {d.unit}
                  </td>
                  {compare.map((c) => {
                    const v = c.values[i];
                    return (
                      <td key={c.h3} className={`${cellClass.right} num`}>
                        {v === null || v === undefined ? "—" : num(v, v < 10 ? 1 : 0)}
                      </td>
                    );
                  })}
                </tr>
              ))}
              <tr>
                <td>Оценка TOPSIS</td>
                {compare.map((c) => (
                  <td key={c.h3} className={`${cellClass.right} num`}>
                    {num(c.score, 3)}
                  </td>
                ))}
              </tr>
            </tbody>
          </table>
        </section>
      )}
    </>
  );
}

/** Текст вклада критериев для экранных читалок. */
function contributionText(c: Schemas["SiteCell"], defs: CriterionDef[]): string {
  return `Вклад: ${c.contributions
    .map((v, i) => `${defs[i]?.title ?? ""} ${num(v * 100)} %`)
    .filter(Boolean)
    .join(", ")}`;
}

/** Слои карты пригодности: ячейки по оценке, исключённые — серым, кандидаты — номерами. */
function siteOverlay(
  cells: FeatureCollection | undefined,
  result: Result | null,
  selected: Set<string>,
  p: Palette,
): Overlay {
  const score = new Map((result?.scores ?? []).map((x) => [x.h3, x.score]));
  const excluded = new Set(result?.excluded ?? []);
  const features: Feature[] = (cells?.features ?? []).map((f) => {
    const h3 = String(f.properties?.h3 ?? "");
    return {
      ...f,
      properties: { h3, score: score.get(h3) ?? null, excluded: excluded.has(h3) },
    };
  });
  const tops: Feature[] = (result?.top ?? []).map((c) => ({
    type: "Feature",
    properties: { rank: c.rank, selected: selected.has(c.h3) },
    geometry: { type: "Point", coordinates: [c.lon, c.lat] },
  }));
  const layers: LayerSpecification[] = [
    {
      id: "site:cells",
      type: "fill",
      source: "site:cells",
      filter: ["any", ["==", ["get", "excluded"], true], ["==", ["typeof", ["get", "score"]], "number"]],
      // Пригодность — насыщенностью одного цвета; исключённые — серым.
      paint: {
        "fill-color": ["case", ["==", ["get", "excluded"], true], color(p, "n9"), color(p, "acc")],
        "fill-opacity": [
          "case",
          ["==", ["get", "excluded"], true],
          0.3,
          ["interpolate", ["linear"], ["get", "score"], 0, 0.04, 0.6, 0.25, 1, 0.8],
        ],
      },
    },
    {
      id: "site:top",
      type: "circle",
      source: "site:top",
      paint: {
        "circle-radius": ["case", ["==", ["get", "selected"], true], 12, 10],
        "circle-color": color(p, "acc"),
        "circle-stroke-color": color(p, "panel"),
        "circle-stroke-width": 2,
      },
    },
    {
      id: "site:top-label",
      type: "symbol",
      source: "site:top",
      layout: {
        "text-field": ["to-string", ["get", "rank"]],
        "text-font": ["Noto Sans Medium"],
        "text-size": 11,
        "text-allow-overlap": true,
      },
      paint: { "text-color": color(p, "acc-on") },
    },
  ];
  return {
    sources: {
      "site:cells": { type: "geojson", data: { type: "FeatureCollection", features } },
      "site:top": { type: "geojson", data: { type: "FeatureCollection", features: tops } },
    },
    layers,
  };
}
