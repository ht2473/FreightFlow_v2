import type { Schemas } from "@/shared/api/client";

type Result = Schemas["RouteResult"];

/** Маршруты в GeoJSON: основной и альтернативы со временем и длиной. */
export function routeGeoJSON(result: Result): string {
  const features = result.routes.map((r, i) => ({
    type: "Feature",
    properties: { kind: i === 0 ? "truck" : "alternate", time_s: r.timeS, length_km: r.lengthKm },
    geometry: { type: "LineString", coordinates: r.coordinates },
  }));
  return JSON.stringify({ type: "FeatureCollection", features });
}

const escapeXml = (v: string) =>
  v.replace(/[<>&"]/g, (c) => ({ "<": "&lt;", ">": "&gt;", "&": "&amp;", '"': "&quot;" })[c] ?? c);

/** Маршрут грузовика в GPX 1.1 (трек; альтернативы — отдельными треками) для навигаторов. */
export function routeGPX(result: Result, name = "Маршрут грузовика"): string {
  const tracks = result.routes.map((r, i) => {
    const title = i === 0 ? name : `${name}: альтернатива ${i}`;
    const points = r.coordinates
      .map(([lon = 0, lat = 0]) => `<trkpt lat="${lat.toFixed(6)}" lon="${lon.toFixed(6)}"/>`)
      .join("");
    return `<trk><name>${escapeXml(title)}</name><trkseg>${points}</trkseg></trk>`;
  });
  return (
    `<?xml version="1.0" encoding="UTF-8"?>\n` +
    `<gpx version="1.1" creator="FreightFlow" xmlns="http://www.topografix.com/GPX/1/1">` +
    `${tracks.join("")}</gpx>\n`
  );
}

/** Сохранение текста файлом. */
export function saveFile(content: string, name: string, type: string) {
  const url = URL.createObjectURL(new Blob([content], { type }));
  const a = document.createElement("a");
  a.href = url;
  a.download = name;
  a.click();
  URL.revokeObjectURL(url);
}
