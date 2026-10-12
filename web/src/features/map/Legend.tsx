import { ChevronDown } from "lucide-react";
import { useState } from "react";
import { color, type MapLayer, type Palette } from "@/shared/map/dataLayers";
import s from "./map.module.css";
import { m } from "./messages";

/** Легенда включённых слоёв: значок по геометрии, категории, правила размера и пунктира. */
export function Legend({ catalog, visible, palette }: { catalog: MapLayer[]; visible: Set<string>; palette: Palette }) {
  const layers = [...catalog].reverse().filter((l) => visible.has(l.code));
  if (layers.length === 0) {
    return null;
  }
  return (
    <section className={`${s.float} ${s.legend}`} aria-label={m.legend}>
      <LegendBody layers={layers} palette={palette} />
    </section>
  );
}

/** Содержимое легенды; сворачивается, чтобы не закрывать карту. */
function LegendBody({ layers, palette }: { layers: MapLayer[]; palette: Palette }) {
  const [open, setOpen] = useState(true);
  return (
    <>
      <button type="button" className={s.legendToggle} aria-expanded={open} onClick={() => setOpen(!open)}>
        {m.legend}
        <ChevronDown size={14} className={open ? s.chevronOpen : undefined} aria-hidden />
      </button>
      {open && (
        <div className={s.legendItems}>
          {layers.map((l) => {
            const items =
              l.style.kind === "category" && l.style.categories.length > 0
                ? l.style.categories.map((c) => ({ label: c.label, color: c.color }))
                : [{ label: l.title, color: l.style.color ?? "acc" }];
            return (
              <div key={l.code} className={s.legendLayer}>
                {l.style.kind === "category" && <div className={s.legendTitle}>{l.title}</div>}
                {items.map((it) => (
                  <div key={it.label} className={s.legendItem}>
                    <Swatch geometry={l.geometry} color={color(palette, it.color)} />
                    {it.label}
                  </div>
                ))}
                {l.style.note && <div className={s.legendNote}>{l.style.note}</div>}
              </div>
            );
          })}
        </div>
      )}
    </>
  );
}

function Swatch({ geometry, color }: { geometry: MapLayer["geometry"]; color: string }) {
  if (geometry === "point") {
    return <i className={s.swatchPoint} style={{ background: color }} aria-hidden />;
  }
  if (geometry === "line") {
    return <i className={s.swatchLine} style={{ background: color }} aria-hidden />;
  }
  return <i className={s.swatchArea} style={{ borderColor: color }} aria-hidden />;
}
