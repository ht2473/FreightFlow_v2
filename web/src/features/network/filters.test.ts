import { describe, expect, it } from "vitest";
import { passabilityFilter } from "./filters";

describe("отбор непроходимых ограничений", () => {
  it("запрет без времени и пределы меньше параметров ТС", () => {
    const f = passabilityFilter({ grossT: 20, heightM: 4, widthM: 2.55, lengthM: 10, axleLoadT: 10 });
    expect(f[0]).toBe("any");
    expect(f).toContainEqual(["all", ["==", ["get", "severity"], "ban"], ["!=", ["get", "conditional"], true]]);
    expect(f).toContainEqual(["<", ["coalesce", ["get", "maxweight"], 1000], 20]);
    expect(f).toContainEqual(["<", ["coalesce", ["get", "maxheight"], 1000], 4]);
    expect(f).toContainEqual(["<", ["coalesce", ["get", "maxaxleload"], 1000], 10]);
  });

  it("неуказанная нагрузка на ось не сравнивается", () => {
    const f = passabilityFilter({ grossT: 3.5, heightM: 2.8, widthM: 2.1, lengthM: 6 });
    expect(JSON.stringify(f)).not.toContain("maxaxleload");
  });
});
