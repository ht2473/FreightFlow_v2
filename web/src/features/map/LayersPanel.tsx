import { Slider, SliderOutput, SliderThumb, SliderTrack } from "react-aria-components";
import type { Schemas } from "@/shared/api/client";
import { EmptyState, Skeleton, Switch } from "@/shared/ui";
import s from "./map.module.css";
import { m } from "./messages";
import { useOpacity } from "./state";

/** Каталог слоёв по группам: включение, непрозрачность, описание и источник. */
export function LayersPanel({
  catalog,
  isError,
  visible,
  zoom,
  onToggle,
}: {
  catalog: Schemas["LayerCatalog"] | undefined;
  isError: boolean;
  visible: Set<string>;
  zoom: number;
  onToggle: (code: string, on: boolean) => void;
}) {
  const opacity = useOpacity((st) => st.opacity);
  const setOpacity = useOpacity((st) => st.set);
  if (isError) {
    return <EmptyState title={m.catalogError}>{""}</EmptyState>;
  }
  if (!catalog) {
    return <Skeleton height={120} />;
  }
  // В каталоге слои идут снизу вверх; в панели — сверху вниз, как их видно на карте.
  const layers = [...catalog.layers].reverse();
  const attributions = [...new Set(catalog.layers.map((l) => l.attribution))];
  return (
    <div className={s.layers}>
      {catalog.groups.map((g) => (
        <section key={g.code} aria-labelledby={`layer-group-${g.code}`}>
          <h3 id={`layer-group-${g.code}`} className={s.sectionTitle}>
            {g.title}
          </h3>
          {layers
            .filter((l) => l.group === g.code)
            .map((l) => {
              const on = visible.has(l.code);
              const hiddenByZoom = on && zoom < l.minzoom;
              return (
                <div key={l.code} className={s.layerRow}>
                  <Switch isSelected={on} onChange={(v) => onToggle(l.code, v)}>
                    <span className={s.layerTitle}>{l.title}</span>
                  </Switch>
                  <div className={s.layerMeta}>
                    {l.description}
                    {hiddenByZoom && <span className={s.layerHint}> · {m.fromZoom(l.minzoom)}</span>}
                  </div>
                  {on && (
                    <Slider
                      className={s.opacity}
                      aria-label={m.opacity(l.title)}
                      minValue={0.2}
                      maxValue={1}
                      step={0.05}
                      value={opacity[l.code] ?? 1}
                      onChange={(v) => setOpacity(l.code, Array.isArray(v) ? (v[0] ?? 1) : v)}
                      formatOptions={{ style: "percent" }}
                    >
                      <SliderTrack className={s.opacityTrack}>
                        <SliderThumb className={s.opacityThumb} />
                      </SliderTrack>
                      <SliderOutput className={s.opacityValue} />
                    </Slider>
                  )}
                </div>
              );
            })}
        </section>
      ))}
      <h3 className={s.sectionTitle}>{m.sources}</h3>
      <div className={s.layerMeta}>
        {m.basemap}: Protomaps, © участники OpenStreetMap
        {attributions.map((a) => (
          <div key={a}>{a}</div>
        ))}
      </div>
    </div>
  );
}
