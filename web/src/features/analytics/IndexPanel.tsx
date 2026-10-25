import { useQueryClient } from "@tanstack/react-query";
import type { Feature, FeatureCollection } from "geojson";
import { useEffect, useMemo, useState } from "react";
import { $api, problemMessage, type Schemas } from "@/shared/api/client";
import { can, useMe } from "@/shared/auth";
import { num } from "@/shared/format";
import { Badge, Button, EmptyState, Select, Skeleton } from "@/shared/ui";
import { NumberField } from "@/shared/ui/fields";
import { toast } from "@/shared/ui/toast";
import { useDebounced } from "@/shared/useDebounced";
import t from "../facilities/facilities.module.css";
import s from "./analytics.module.css";
import { MetricMap, useTerritoryOpen } from "./MetricMap";
import { levelTitles } from "./metrics";
import { useRows } from "./useRows";

type IndexDef = Schemas["IndexDef"];
type IndexLevel = "district" | "h3_8";

/** Геометрия уровня для картограммы индекса: показатель, у которого есть все объекты уровня. */
const geometrySource: Record<IndexLevel, string> = { district: "territory.population", h3_8: "h3.facilities" };

/**
 * Индексы: веса составляющих (свои — без сохранения сразу пересчитываются), нормировка,
 * рейтинг и картограмма, чувствительность рангов к весам ±20 %; сохранение своих и общих весов.
 */
export function IndexPanel({
  code,
  level,
  onChange,
}: {
  code: string | undefined;
  level: IndexLevel;
  onChange: (code: string, level: IndexLevel) => void;
}) {
  const qc = useQueryClient();
  const me = useMe();
  const list = $api.useQuery("get", "/indexes", {}, { staleTime: 30_000 });
  const ix: IndexDef | undefined = list.data?.find((x) => x.code === code) ?? list.data?.[0];
  const comps = useMemo(() => ix?.levels.find((l) => l.level === level)?.components ?? [], [ix, level]);
  const [weights, setWeights] = useState<Record<string, number>>({});
  const [norm, setNorm] = useState<"minmax" | "zscore">("minmax");
  // Веса пользователя — при смене индекса, уровня или после сохранения.
  useEffect(() => {
    setWeights(Object.fromEntries(comps.map((c) => [c.ref, c.weight])));
    if (ix) {
      setNorm(ix.normalization);
    }
  }, [ix, comps]);
  const request = useMemo(() => ({ level, weights, normalization: norm }), [level, weights, norm]);
  const body = useDebounced(request, 300);
  const ready = ix !== undefined && Object.keys(body.weights).length === comps.length && body.level === level;
  const res = $api.useQuery(
    "post",
    "/indexes/{code}/compute",
    { params: { path: { code: ix?.code ?? "" } }, body },
    { enabled: ready, placeholderData: (prev) => prev },
  );
  const geo = $api.useQuery("get", "/metrics/{code}/geo", {
    params: { path: { code: geometrySource[level] }, query: { level } },
  });
  const map = useMemo(() => {
    if (!geo.data || !res.data) {
      return undefined;
    }
    const scores = new Map(res.data.scores.map((x) => [x.entity, x.score]));
    const fc = geo.data as unknown as FeatureCollection;
    const features = fc.features.flatMap((f): Feature[] => {
      const e = String((f.properties as { entity?: string } | null)?.entity);
      const v = scores.get(e);
      return v === undefined ? [] : [{ ...f, properties: { ...f.properties, value: v } }];
    });
    return { ...fc, features };
  }, [geo.data, res.data]);
  const open = useTerritoryOpen(level);
  const { shown, more } = useRows(res.data?.scores ?? []);

  const refresh = () => qc.invalidateQueries({ queryKey: ["get", "/indexes"] });
  const save = $api.useMutation("put", "/indexes/{code}/config", {
    onSuccess: () => {
      toast.success("Веса сохранены");
      void refresh();
    },
    onError: (e) => toast.error(problemMessage(e)),
  });
  const reset = $api.useMutation("delete", "/indexes/{code}/config", {
    onSuccess: () => {
      toast.success("Свои веса удалены — действуют общие");
      void refresh();
    },
  });
  const allWeights = () =>
    Object.fromEntries(
      (ix?.levels ?? []).map((l) => [
        l.level,
        l.level === level ? weights : Object.fromEntries(l.components.map((c) => [c.ref, c.weight])),
      ]),
    );
  const title = (ref: string) => comps.find((c) => c.ref === ref)?.title ?? ref;

  if (list.isPending) {
    return <Skeleton height={300} />;
  }
  return (
    <>
      <div className={s.toolbar}>
        <Select
          label="Индекс"
          options={(list.data ?? []).map((x) => ({ id: x.code, label: x.title }))}
          value={ix?.code ?? null}
          onChange={(c) => onChange(c, level)}
        />
        <Select
          label="Уровень"
          options={(["district", "h3_8"] as const).map((l) => ({ id: l, label: levelTitles[l] }))}
          value={level}
          onChange={(l) => onChange(ix?.code ?? "", l as IndexLevel)}
        />
        <Select
          label="Нормировка"
          options={[
            { id: "minmax", label: "min–max" },
            { id: "zscore", label: "z-оценка (±3σ)" },
          ]}
          value={norm}
          onChange={(n) => setNorm(n as "minmax" | "zscore")}
        />
        {ix?.own && <Badge tone="info">свои веса</Badge>}
      </div>
      {ix && <p className={s.note}>{ix.method}</p>}
      <div className={s.layout}>
        <div>
          <fieldset className={s.weights} aria-label="Веса составляющих">
            {comps.map((c) => (
              <div key={c.ref} style={{ display: "contents" }}>
                <div>
                  <div>{c.title}</div>
                  <div className={s.dir}>
                    {c.direction > 0 ? "больше — лучше" : "меньше — лучше"} · по умолчанию {num(c.defaultWeight, 2)}
                  </div>
                </div>
                <NumberField
                  label={`Вес: ${c.title}`}
                  value={weights[c.ref] ?? 0}
                  minValue={0}
                  maxValue={100}
                  step={0.05}
                  onChange={(v) => setWeights((w) => ({ ...w, [c.ref]: v ?? 0 }))}
                />
              </div>
            ))}
          </fieldset>
          <div className={s.actions}>
            {can(me.data, "views.manage") && (
              <Button
                size="sm"
                variant="primary"
                onPress={() =>
                  ix &&
                  save.mutate({
                    params: { path: { code: ix.code } },
                    body: { scope: "own", weights: allWeights(), normalization: norm },
                  })
                }
              >
                Сохранить как свои
              </Button>
            )}
            {can(me.data, "settings.manage") && (
              <Button
                size="sm"
                onPress={() =>
                  ix &&
                  save.mutate({
                    params: { path: { code: ix.code } },
                    body: { scope: "default", weights: allWeights(), normalization: norm },
                  })
                }
              >
                Сделать весами по умолчанию
              </Button>
            )}
            {ix?.own && (
              <Button size="sm" variant="ghost" onPress={() => reset.mutate({ params: { path: { code: ix.code } } })}>
                Вернуть общие веса
              </Button>
            )}
          </div>
          {res.error ? (
            <div className="notice notice-error" role="alert">
              {problemMessage(res.error)}
            </div>
          ) : !res.data ? (
            <Skeleton height={200} />
          ) : res.data.scores.length === 0 ? (
            <EmptyState title="Нет объектов">Ни у одного объекта уровня нет всех составляющих.</EmptyState>
          ) : (
            <>
              <h3 className={s.note}>
                Чувствительность: вес каждой составляющей ±20 %, корреляция Спирмена с исходными рангами
              </h3>
              <table className={t.table} aria-label="Чувствительность к весам">
                <thead>
                  <tr>
                    <th>Составляющая</th>
                    <th className={t.right}>ρ при −20 %</th>
                    <th className={t.right}>ρ при +20 %</th>
                    <th className={t.right}>Наибольший сдвиг места</th>
                  </tr>
                </thead>
                <tbody>
                  {res.data.sensitivity.map((x) => (
                    <tr key={x.ref}>
                      <td>{title(x.ref)}</td>
                      <td className={`${t.right} num`}>{num(x.rhoMinus, 3)}</td>
                      <td className={`${t.right} num`}>{num(x.rhoPlus, 3)}</td>
                      <td className={`${t.right} num`}>{num(x.maxShift)}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
              <div>
                <table className={t.table} aria-label={`Рейтинг: ${ix?.title ?? ""}`}>
                  <thead>
                    <tr>
                      <th className={t.right}>Место</th>
                      <th>{levelTitles[level]}</th>
                      <th className={t.right}>Индекс</th>
                    </tr>
                  </thead>
                  <tbody>
                    {shown.map((x) => (
                      <tr key={x.entity}>
                        <td className={`${t.right} num`}>{x.rank}</td>
                        <td title={x.parts.map((p) => `${title(p.ref)}: ${num(p.normalized * 100)}`).join("; ")}>
                          {x.label}
                        </td>
                        <td className={`${t.right} num`}>{num(x.score, 1)}</td>
                      </tr>
                    ))}
                  </tbody>
                </table>
                {more}
              </div>
              <p className={s.note}>
                В итог входят объекты со всеми составляющими ненулевого веса: {num(res.data.scores.length)}.
              </p>
            </>
          )}
        </div>
        {map ? (
          <MetricMap geo={map} label={`Картограмма: ${ix?.title ?? ""}`} format={(v) => num(v, 1)} onSelect={open} />
        ) : (
          <Skeleton height={520} />
        )}
      </div>
    </>
  );
}
