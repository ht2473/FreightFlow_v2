import type { FeatureCollection, Geometry } from "geojson";
import { useMemo, useState } from "react";
import { $api, problemMessage } from "@/shared/api/client";
import { num } from "@/shared/format";
import { Select, Skeleton } from "@/shared/ui";
import { NumberField } from "@/shared/ui/fields";
import { SegmentedControl } from "@/shared/ui/navigation";
import { useDebounced } from "@/shared/useDebounced";
import t from "../facilities/facilities.module.css";
import { area } from "../facilities/labels";
import s from "./analytics.module.css";
import { MetricHelp } from "./MetricHelp";
import { MetricMap } from "./MetricMap";
import { useMetrics } from "./metrics";

/** Классы значимости Gi*: знак × уровень (горячие — красные, холодные — синие). */
const hotClasses = [
  { value: 99, color: "crit", label: "Горячая точка, 99 %" },
  { value: 95, color: "warn", label: "Горячая точка, 95 %" },
  { value: 90, color: "cat5", label: "Горячая точка, 90 %" },
  { value: -90, color: "cat6", label: "Холодная точка, 90 %" },
  { value: -95, color: "cat1", label: "Холодная точка, 95 %" },
  { value: -99, color: "info", label: "Холодная точка, 99 %" },
];

/** Горячие точки показателя ячеек (Getis–Ord Gi*) и кластеры объектов (DBSCAN). */
export function SpatialPanel() {
  const [mode, setMode] = useState("hotspots");
  return (
    <>
      <SegmentedControl
        label="Метод"
        options={[
          { id: "hotspots", label: "Горячие точки (Gi*)" },
          { id: "clusters", label: "Кластеры объектов (DBSCAN)" },
        ]}
        value={mode}
        onChange={setMode}
      />
      <div style={{ marginTop: 16 }}>{mode === "hotspots" ? <Hotspots /> : <Clusters />}</div>
    </>
  );
}

function Hotspots() {
  const metrics = useMetrics();
  const options = (metrics.data ?? []).filter((m) => m.code.startsWith("h3."));
  const [metric, setMetric] = useState("h3.building_area");
  const [level, setLevel] = useState<"h3_8" | "h3_9">("h3_8");
  const [k, setK] = useState(1);
  const req = useMemo(() => ({ metric, level, k }), [metric, level, k]);
  const q = $api.useQuery("post", "/analytics/hotspots", { body: req }, { placeholderData: (prev) => prev });
  const geo = useMemo<FeatureCollection | undefined>(
    () =>
      q.data && {
        type: "FeatureCollection",
        features: q.data.significant.map((h) => ({
          type: "Feature",
          id: h.h3,
          properties: { entity: h.h3, label: `z = ${num(h.z, 2)}`, value: h.sign * h.confidence },
          geometry: h.geometry as unknown as Geometry,
        })),
      },
    [q.data],
  );
  return (
    <>
      <div className={s.toolbar}>
        <Select
          label="Показатель ячеек"
          options={options.map((m) => ({ id: m.code, label: m.title }))}
          value={metric}
          onChange={setMetric}
        />
        <MetricHelp def={metrics.byCode.get(metric)} />
        <Select
          label="Ячейки"
          options={[
            { id: "h3_8", label: "≈ 0,7 км² (разрешение 8)" },
            { id: "h3_9", label: "≈ 0,1 км² (разрешение 9)" },
          ]}
          value={level}
          onChange={(v) => setLevel(v as "h3_8" | "h3_9")}
        />
        <NumberField label="Соседство, колец" value={k} minValue={1} maxValue={3} onChange={(v) => setK(v ?? 1)} />
      </div>
      {q.error ? (
        <div className="notice notice-error" role="alert">
          {problemMessage(q.error)}
        </div>
      ) : !q.data || !geo ? (
        <Skeleton height={520} />
      ) : (
        <>
          <p className={s.note}>
            Getis–Ord Gi* по {num(q.data.cells)} ячейкам: соседство — диск из {q.data.k} колец (сама ячейка входит),
            бинарные веса; среднее {num(q.data.mean, 2)}, σ = {num(q.data.sd, 2)}. Значимых: горячих {num(q.data.hot)},
            холодных {num(q.data.cold)}.
          </p>
          <MetricMap
            geo={geo}
            label="Горячие и холодные точки"
            format={(v) => `${v > 0 ? "горячая" : "холодная"} ${Math.abs(v)} %`}
            classes={hotClasses}
          />
        </>
      )}
    </>
  );
}

function Clusters() {
  const [eps, setEps] = useState(500);
  const [min, setMin] = useState(5);
  const req = useDebounced(
    useMemo(() => ({ epsM: eps, minPoints: min }), [eps, min]),
    300,
  );
  const q = $api.useQuery("post", "/analytics/clusters", { body: req }, { placeholderData: (prev) => prev });
  const geo = useMemo<FeatureCollection | undefined>(
    () =>
      q.data && {
        type: "FeatureCollection",
        features: q.data.clusters.map((c) => ({
          type: "Feature",
          id: c.id,
          properties: {
            entity: String(c.id),
            label: `Кластер ${c.id}: ${num(c.facilities)} объектов`,
            value: c.facilities,
          },
          geometry: c.hull as unknown as Geometry,
        })),
      },
    [q.data],
  );
  return (
    <>
      <div className={s.toolbar}>
        <NumberField
          label="Радиус соседства"
          unit="м"
          value={eps}
          minValue={50}
          maxValue={10000}
          step={50}
          onChange={(v) => setEps(v ?? 500)}
        />
        <NumberField
          label="Наименьшее число объектов"
          value={min}
          minValue={2}
          maxValue={100}
          onChange={(v) => setMin(v ?? 5)}
        />
      </div>
      {q.error ? (
        <div className="notice notice-error" role="alert">
          {problemMessage(q.error)}
        </div>
      ) : !q.data || !geo ? (
        <Skeleton height={520} />
      ) : (
        <div className={s.layout}>
          <div>
            <p className={s.note}>
              DBSCAN по {num(q.data.total)} объектам: радиус {num(q.data.epsM)} м, не меньше {q.data.minPoints}{" "}
              объектов; кластеров {num(q.data.clusters.length)}, вне кластеров {num(q.data.noise)}.
            </p>
            <div>
              <table className={t.table} aria-label="Кластеры объектов">
                <thead>
                  <tr>
                    <th className={t.right}>№</th>
                    <th className={t.right}>Объектов</th>
                    <th className={t.right}>Площадь застройки</th>
                    <th>Крупнейшие</th>
                  </tr>
                </thead>
                <tbody>
                  {q.data.clusters.map((c) => (
                    <tr key={c.id}>
                      <td className={`${t.right} num`}>{c.id}</td>
                      <td className={`${t.right} num`}>{num(c.facilities)}</td>
                      <td className={`${t.right} num`}>{area(c.buildingAreaM2)}</td>
                      <td>{c.names.join("; ")}</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </div>
          <MetricMap geo={geo} label="Кластеры объектов" format={(v) => `${num(v)} объектов`} />
        </div>
      )}
    </>
  );
}
