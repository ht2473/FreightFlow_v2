import { describe, expect, it } from "vitest";
import { routeGeoJSON, routeGPX } from "./exportRoute";

const result = {
  routes: [
    {
      timeS: 600,
      lengthKm: 5.2,
      coordinates: [
        [37.6, 55.75],
        [37.61, 55.76],
      ],
    },
    {
      timeS: 660,
      lengthKm: 5.9,
      coordinates: [
        [37.6, 55.75],
        [37.62, 55.755],
      ],
    },
  ],
  zones: [],
  blocked: [],
  avoidedZones: [],
  avoided: [],
} as unknown as Parameters<typeof routeGPX>[0];

describe("выгрузка маршрута", () => {
  it("GPX: трек на маршрут, точки в порядке «широта, долгота»", () => {
    const gpx = routeGPX(result);
    expect(gpx).toContain('<gpx version="1.1" creator="FreightFlow"');
    expect(gpx.match(/<trk>/g)).toHaveLength(2);
    expect(gpx).toContain('<trkpt lat="55.750000" lon="37.600000"/>');
    expect(gpx).toContain("<name>Маршрут грузовика: альтернатива 1</name>");
  });

  it("GeoJSON: основной маршрут и альтернатива", () => {
    const fc = JSON.parse(routeGeoJSON(result));
    expect(fc.features.map((f: { properties: { kind: string } }) => f.properties.kind)).toEqual(["truck", "alternate"]);
  });
});
