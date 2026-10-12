import type { MapGeoJSONFeature } from "maplibre-gl";
import type { Schemas } from "@/shared/api/client";
import { num } from "@/shared/format";
import { Drawer } from "@/shared/ui/overlays";
import s from "./map.module.css";
import { m } from "./messages";

export interface Selection {
  layer: Schemas["MapLayer"];
  feature: MapGeoJSONFeature;
  lngLat: [number, number];
}

/** Значение свойства для показа: подпись перечисления, число по-русски с единицей. */
function formatValue(prop: Schemas["LayerProperty"], v: unknown): string | null {
  if (v === undefined || v === null || v === "") {
    return null;
  }
  switch (prop.kind) {
    case "enum":
      return prop.labels?.[String(v)] ?? String(v);
    case "number": {
      const n = Number(v);
      if (Number.isNaN(n)) {
        return null;
      }
      return `${num(n, n < 10 ? 2 : 0)}${prop.unit ? ` ${prop.unit}` : ""}`;
    }
    case "bool":
      return v === true ? m.yes : m.no;
    default:
      return String(v);
  }
}

/** Временная карточка объекта карты (до реестра): свойства из тайла по описанию слоя. */
export function FeatureCard({ selection, onClose }: { selection: Selection; onClose: () => void }) {
  const { layer, feature, lngLat } = selection;
  const p = feature.properties as Record<string, unknown>;
  const rows = layer.properties
    .map((prop) => ({ prop, value: formatValue(prop, p[prop.name]) }))
    .filter((r) => r.value !== null && r.prop.name !== "name");
  const enumProp = layer.properties.find((x) => x.kind === "enum");
  const fallback = enumProp ? formatValue(enumProp, p[enumProp.name]) : null;
  const title = typeof p.name === "string" && p.name ? p.name : (fallback ?? layer.title);
  return (
    <Drawer isOpen onOpenChange={(o) => !o && onClose()} title={title} subtitle={layer.title}>
      <dl className={s.card}>
        {rows.map((r) => (
          <div key={r.prop.name} className={s.cardRow}>
            <dt>{r.prop.title}</dt>
            <dd className={r.prop.kind === "number" ? "num" : undefined}>{r.value}</dd>
          </div>
        ))}
        <div className={s.cardRow}>
          <dt>{m.cardCoordinates}</dt>
          <dd className="num">
            {num(lngLat[1], 5)}° с. ш., {num(lngLat[0], 5)}° в. д.
          </dd>
        </div>
        <div className={s.cardRow}>
          <dt>{m.cardSource}</dt>
          <dd>{layer.attribution}</dd>
        </div>
      </dl>
      <p className={s.cardNote}>{m.cardTemporary}</p>
    </Drawer>
  );
}
