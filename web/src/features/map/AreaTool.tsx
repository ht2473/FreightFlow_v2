import type { Geometry, Polygon } from "geojson";
import { PenLine, Ruler, X } from "lucide-react";
import type { Map as MapLibreMap } from "maplibre-gl";
import { useEffect, useRef } from "react";
import { TerraDraw, TerraDrawLineStringMode, TerraDrawPolygonMode } from "terra-draw";
import { TerraDrawMapLibreGLAdapter } from "terra-draw-maplibre-gl-adapter";
import { IconButton } from "@/shared/ui";
import s from "./map.module.css";

export type DrawMode = "area" | "measure";

export interface Drawn {
  mode: DrawMode;
  geometry: Geometry;
}

/** Длина линии по сфере, м. */
export function lineLength(coords: number[][]): number {
  const r = 6_371_008.8;
  const rad = Math.PI / 180;
  let m = 0;
  for (let i = 1; i < coords.length; i++) {
    const [lon1 = 0, lat1 = 0] = coords[i - 1] ?? [];
    const [lon2 = 0, lat2 = 0] = coords[i] ?? [];
    const dLat = (lat2 - lat1) * rad;
    const dLon = (lon2 - lon1) * rad;
    const h = Math.sin(dLat / 2) ** 2 + Math.cos(lat1 * rad) * Math.cos(lat2 * rad) * Math.sin(dLon / 2) ** 2;
    m += 2 * r * Math.asin(Math.sqrt(h));
  }
  return m;
}

/**
 * Рисование на карте: многоугольник области (сводка) или линия (измерение). terra-draw работает
 * только во время рисования; результат передаётся наверх и рисуется слоем стиля страницы —
 * обновление стиля не стирает его.
 */
export function AreaTool({
  map,
  mode,
  onMode,
  onDrawn,
}: {
  map: MapLibreMap | null;
  mode: DrawMode | null;
  onMode: (m: DrawMode | null) => void;
  onDrawn: (d: Drawn) => void;
}) {
  const doneRef = useRef(onDrawn);
  doneRef.current = onDrawn;
  useEffect(() => {
    if (!map || !mode) {
      return;
    }
    const draw = new TerraDraw({
      adapter: new TerraDrawMapLibreGLAdapter({ map, prefixId: "ff-draw" }),
      modes: [new TerraDrawPolygonMode(), new TerraDrawLineStringMode()],
    });
    draw.start();
    draw.setMode(mode === "area" ? "polygon" : "linestring");
    draw.on("finish", (id) => {
      const f = draw.getSnapshotFeature(id);
      if (f) {
        doneRef.current({ mode, geometry: f.geometry as Geometry });
      }
      onMode(null);
    });
    return () => {
      draw.stop();
    };
  }, [map, mode, onMode]);

  return (
    <>
      {mode && (
        <div className={`${s.float} ${s.measure}`} role="status">
          {mode === "area"
            ? "Щёлкайте вершины области; замкните щелчком в первую вершину"
            : "Щёлкайте точки линии; завершите повторным щелчком в последнюю"}
        </div>
      )}
      <div className={`${s.float} ${s.tools}`} role="toolbar" aria-label="Инструменты карты">
        <IconButton
          label={mode === "area" ? "Отменить выделение области" : "Выделить область"}
          onPress={() => onMode(mode === "area" ? null : "area")}
        >
          {mode === "area" ? <X size={17} /> : <PenLine size={17} />}
        </IconButton>
        <IconButton
          label={mode === "measure" ? "Отменить измерение" : "Измерить расстояние"}
          onPress={() => onMode(mode === "measure" ? null : "measure")}
        >
          {mode === "measure" ? <X size={17} /> : <Ruler size={17} />}
        </IconButton>
      </div>
    </>
  );
}

export const isPolygon = (g: Geometry): g is Polygon => g.type === "Polygon";
