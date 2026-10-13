import type { ExpressionSpecification } from "maplibre-gl";
import type { Schemas } from "@/shared/api/client";

type Vehicle = Schemas["VehicleParams"];

/**
 * Отбор ограничений, непроходимых для ТС: запрет движения без условий по времени или предел
 * массы, нагрузки на ось, высоты, ширины меньше параметров ТС. «Только для подъезда» не входит.
 */
export function passabilityFilter(v: Vehicle): ExpressionSpecification {
  const below = (prop: string, value: number | undefined): ExpressionSpecification[] =>
    value === undefined ? [] : [["<", ["coalesce", ["get", prop], 1000], value]];
  return [
    "any",
    ["all", ["==", ["get", "severity"], "ban"], ["!=", ["get", "conditional"], true]],
    ...below("maxweight", v.grossT),
    ...below("maxaxleload", v.axleLoadT),
    ...below("maxheight", v.heightM),
    ...below("maxwidth", v.widthM),
  ];
}
