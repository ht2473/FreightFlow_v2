import type { Map as MapLibreMap } from "maplibre-gl";
import { color, type MapLayer, type Palette } from "@/shared/map/dataLayers";

/** Строка легенды на изображении: заголовок слоя, элемент со значком или пояснение. */
export type LegendRow =
  | { kind: "title"; text: string }
  | { kind: "item"; text: string; geometry: MapLayer["geometry"]; color: string }
  | { kind: "note"; text: string };

/** Строки легенды включённых слоёв — те же, что в легенде страницы. */
export function legendRows(layers: MapLayer[]): LegendRow[] {
  const rows: LegendRow[] = [];
  for (const l of layers) {
    if (l.style.kind === "category" && l.style.categories.length > 0) {
      rows.push({ kind: "title", text: l.title });
      for (const c of l.style.categories) {
        rows.push({ kind: "item", text: c.label, geometry: l.geometry, color: c.color });
      }
    } else {
      rows.push({ kind: "item", text: l.title, geometry: l.geometry, color: l.style.color ?? "acc" });
    }
    if (l.style.note) {
      rows.push({ kind: "note", text: l.style.note });
    }
  }
  return rows;
}

/** Источники данных изображения: подложка и атрибуция каждого включённого слоя, без повторов. */
export function attributionText(layers: MapLayer[], basemap: boolean): string {
  const parts = new Set<string>();
  if (basemap) {
    parts.add("Protomaps");
  }
  parts.add("© участники OpenStreetMap");
  for (const l of layers) {
    for (const p of l.attribution.split(";")) {
      if (p.trim()) {
        parts.add(p.trim());
      }
    }
  }
  parts.add("MapLibre");
  return [...parts].join(" · ");
}

/** Кадр карты: буфер WebGL читается в том же кадре, что и отрисовка. */
function captureCanvas(map: MapLibreMap): Promise<HTMLCanvasElement> {
  return new Promise((resolve) => {
    map.once("render", () => {
      const src = map.getCanvas();
      const copy = document.createElement("canvas");
      copy.width = src.width;
      copy.height = src.height;
      copy.getContext("2d")?.drawImage(src, 0, 0);
      resolve(copy);
    });
    map.triggerRepaint();
  });
}

/** Переносит текст по словам в пределах ширины. */
function wrap(ctx: CanvasRenderingContext2D, text: string, width: number): string[] {
  const lines: string[] = [];
  let line = "";
  for (const word of text.split(" ")) {
    const next = line ? `${line} ${word}` : word;
    if (line && ctx.measureText(next).width > width) {
      lines.push(line);
      line = word;
    } else {
      line = next;
    }
  }
  if (line) {
    lines.push(line);
  }
  return lines;
}

/**
 * Изображение карты PNG: кадр карты, легенда включённых слоёв поверх в левом верхнем углу,
 * снизу — подпись с датой и источниками данных.
 */
export async function mapImage(
  map: MapLibreMap,
  opts: { title: string; layers: MapLayer[]; palette: Palette; basemap: boolean },
): Promise<Blob> {
  const frame = await captureCanvas(map);
  const k = frame.width / map.getCanvas().clientWidth || 1;
  const font = getComputedStyle(document.body).fontFamily || "sans-serif";
  const p = opts.palette;
  const pad = 12 * k;

  const out = document.createElement("canvas");
  const ctx = out.getContext("2d");
  if (!ctx) {
    throw new Error("canvas");
  }
  ctx.font = `${11 * k}px ${font}`;
  const footer = wrap(ctx, attributionText(opts.layers, opts.basemap), frame.width - 2 * pad);
  const footerH = (30 + footer.length * 15) * k;
  out.width = frame.width;
  out.height = frame.height + footerH;

  ctx.fillStyle = color(p, "panel");
  ctx.fillRect(0, 0, out.width, out.height);
  ctx.drawImage(frame, 0, 0);

  // Легенда.
  const rows = legendRows(opts.layers);
  if (rows.length > 0) {
    const w = 240 * k;
    const lines = rows.map((r) => {
      ctx.font = `${(r.kind === "note" ? 10.5 : 12) * k}px ${font}`;
      return { row: r, text: wrap(ctx, r.text, w - 2 * pad - (r.kind === "item" ? 20 * k : 0)) };
    });
    const lineH = (r: LegendRow) => (r.kind === "note" ? 14 : 17) * k;
    const h = pad * 2 + 20 * k + lines.reduce((sum, l) => sum + l.text.length * lineH(l.row), 0);
    const x0 = pad;
    const y0 = pad;
    ctx.globalAlpha = 0.94;
    ctx.fillStyle = color(p, "panel");
    ctx.fillRect(x0, y0, w, h);
    ctx.globalAlpha = 1;
    ctx.strokeStyle = color(p, "n8");
    ctx.lineWidth = k;
    ctx.strokeRect(x0, y0, w, h);
    ctx.textBaseline = "middle";
    ctx.fillStyle = color(p, "tx2");
    ctx.font = `600 ${10.5 * k}px ${font}`;
    ctx.fillText("ЛЕГЕНДА", x0 + pad, y0 + pad + 6 * k);
    let y = y0 + pad + 20 * k;
    for (const { row, text } of lines) {
      const lh = lineH(row);
      text.forEach((t, i) => {
        const cy = y + lh / 2;
        let tx = x0 + pad;
        if (row.kind === "item") {
          if (i === 0) {
            swatch(ctx, row.geometry, color(p, row.color), tx, cy, k);
          }
          tx += 20 * k;
        }
        ctx.fillStyle = row.kind === "note" ? color(p, "tx2") : color(p, "tx");
        ctx.font = `${row.kind === "title" ? "600 " : ""}${(row.kind === "note" ? 10.5 : 12) * k}px ${font}`;
        ctx.fillText(t, tx, cy);
        y += lh;
      });
    }
  }

  // Подпись: название, дата, источники.
  ctx.textBaseline = "alphabetic";
  ctx.fillStyle = color(p, "tx");
  ctx.font = `600 ${12 * k}px ${font}`;
  ctx.fillText(opts.title, pad, frame.height + 20 * k);
  ctx.fillStyle = color(p, "tx2");
  ctx.font = `${11 * k}px ${font}`;
  footer.forEach((t, i) => {
    ctx.fillText(t, pad, frame.height + (36 + i * 15) * k);
  });

  return new Promise((resolve, reject) => out.toBlob((b) => (b ? resolve(b) : reject(new Error("png"))), "image/png"));
}

function swatch(
  ctx: CanvasRenderingContext2D,
  geometry: MapLayer["geometry"],
  c: string,
  x: number,
  cy: number,
  k: number,
) {
  ctx.fillStyle = c;
  ctx.strokeStyle = c;
  if (geometry === "point") {
    ctx.beginPath();
    ctx.arc(x + 6 * k, cy, 5 * k, 0, Math.PI * 2);
    ctx.fill();
  } else if (geometry === "line") {
    ctx.fillRect(x, cy - 1.5 * k, 14 * k, 3 * k);
  } else {
    ctx.lineWidth = 1.5 * k;
    ctx.strokeRect(x, cy - 5 * k, 14 * k, 10 * k);
  }
}
