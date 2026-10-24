import { useQuery } from "@tanstack/react-query";
import type { StyleSpecification } from "maplibre-gl";
import { basemapStyle, type MapTheme } from "./basemap";
import { type buildDataLayers, color, type MapLayer, type Palette } from "./dataLayers";

/** Подложка установлена: TileJSON отвечает 200. */
export function useBasemapAvailable() {
  return useQuery({
    queryKey: ["map", "basemap"],
    queryFn: async () => (await fetch("/map/basemap.json", { credentials: "same-origin" })).ok,
    staleTime: 60_000,
  });
}

/** Стиль без подложки: фон темы, шрифты и значки — для слоёв данных. */
export function emptyStyle(theme: MapTheme, palette: Palette): StyleSpecification {
  const base = basemapStyle(theme);
  return {
    version: 8,
    glyphs: base.glyphs,
    sources: {},
    layers: [{ id: "background", type: "background", paint: { "background-color": color(palette, "panel") } }],
  };
}

/**
 * Полный стиль: подложка и слои данных. Линии и контуры — под подписями подложки, точки —
 * поверх; слои страницы (overlay: маршрут, точки) — поверх всего. Стиль строится заново при смене темы, слоёв или прозрачности; MapLibre
 * применяет только разницу.
 */
export function fullStyle(
  base: StyleSpecification,
  catalog: MapLayer[],
  data: ReturnType<typeof buildDataLayers>,
  overlay: { sources: StyleSpecification["sources"]; layers: StyleSpecification["layers"] } = {
    sources: {},
    layers: [],
  },
) {
  const firstLabel = base.layers.findIndex((x) => x.type === "symbol");
  const split = firstLabel < 0 ? base.layers.length : firstLabel;
  // Точки и подписи кластеров — поверх подписей подложки.
  const onTop = (x: { id: string; type: string }) => x.type === "circle" || x.id.endsWith(":count");
  const points = data.layers.filter(onTop);
  const shapes = data.layers.filter((x) => !onTop(x));
  return {
    ...base,
    sources: { ...base.sources, ...data.sources, ...overlay.sources },
    layers: [...base.layers.slice(0, split), ...shapes, ...base.layers.slice(split), ...points, ...overlay.layers],
    // Каталог входит в зависимость стиля: смена версии данных меняет адреса тайлов.
    metadata: { catalog: catalog.map((x) => x.version).join("|") },
  } satisfies StyleSpecification;
}
