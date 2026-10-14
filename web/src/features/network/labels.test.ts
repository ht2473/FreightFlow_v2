import { describe, expect, it } from "vitest";
import {
  accuracyText,
  damageText,
  detourText,
  difference,
  duration,
  elementLabel,
  limitText,
  mapCenter,
  pointLabel,
  restrictionText,
  segmentsLabel,
} from "./labels";

describe("подписи маршрута", () => {
  it("длительность", () => {
    expect(duration(25 * 60)).toBe("25 мин");
    expect(duration(65 * 60 + 20)).toBe("1 ч 05 мин");
  });

  it("разница с путём без ограничений", () => {
    const truck = { timeS: 1560, lengthKm: 25, coordinates: [] };
    const base = { timeS: 1080, lengthKm: 17.3, coordinates: [] };
    expect(difference(truck, base)).toBe("+8 мин, +7,7 км");
    expect(difference(base, base)).toBe("0 мин, 0 км");
  });

  it("точки A, промежуточные, B", () => {
    expect([0, 1, 2, 3].map((i) => pointLabel(i, 4))).toEqual(["A", "1", "2", "B"]);
  });

  it("ограничение со значением", () => {
    expect(restrictionText({ kind: "maxheight", value: 3.5, street: "", origins: [], segments: 1, lengthM: 80 })).toBe(
      "Высота до 3,5 м",
    );
    expect(restrictionText({ kind: "hgv_ban", street: "", origins: [], segments: 1, lengthM: 80 })).toBe(
      "Запрет грузовых",
    );
  });
});

describe("подписи анализа сети", () => {
  it("предел узкого места", () => {
    expect(limitText("maxheight", 3.5)).toBe("Высота до 3,5 м");
    expect(limitText("maxweight", undefined)).toBe("Масса");
  });

  it("объезд", () => {
    expect(detourText(undefined)).toBe("нет объезда");
    expect(detourText(0.2)).toBe("менее 1 мин");
    expect(detourText(4.46)).toBe("+4,5 мин");
    expect(detourText(12.4)).toBe("+12 мин");
  });

  it("точность оценки", () => {
    expect(accuracyText({ sources: 49, exact: true }, 49)).toBe("Оценка: точный расчёт по всем 49 узлам.");
    expect(accuracyText({ sources: 1024, exact: false, rse: 0.043, splitHalf: 0.91 }, 120000)).toBe(
      "Оценка: 1 024 узлов-источников из 120 000; медиана относительной ошибки верхней сотни участков — 4,3 %; согласованность рейтингов двух половин выборки (Спирмен) — 0,91.",
    );
  });

  it("центр карты", () => {
    expect(mapCenter({ lon: 37.6175, lat: 55.75 })).toBe("37.61750,55.75000");
  });
});

describe("проверка критичности", () => {
  it("итог матриц", () => {
    expect(damageText({ facilities: 12, minutes: 340.4, maxDelay: 9, disconnected: 0 })).toBe(
      "12 объектов, +340 объекто-мин",
    );
    expect(damageText({ facilities: 1, minutes: 3, maxDelay: 3, disconnected: 2 })).toBe(
      "1 объект, +3 объекто-мин; без доступа 2",
    );
    expect(damageText({ facilities: 0, minutes: 0, maxDelay: 0, disconnected: 0 })).toBe("без роста времени");
  });

  it("объединённые строки", () => {
    expect(elementLabel("bridge", 1)).toBe("мост, путепровод");
    expect(elementLabel("bridge", 4)).toBe("мост, путепровод; таких 4");
    expect(segmentsLabel(6)).toBe("6 участков");
    expect(segmentsLabel(2)).toBe("2 участка");
  });
});
