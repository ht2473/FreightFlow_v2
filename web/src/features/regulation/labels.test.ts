import { describe, expect, it } from "vitest";
import type { Schemas } from "@/shared/api/client";
import { docRequisites, docShort, monthLabel, monthRange, ruleBasis, ruleRequirement, ruleSubject } from "./labels";

type Rule = Schemas["ZoneRule"];
type Doc = Schemas["LegalDocument"];

const base: Rule = {
  code: "x",
  zone: "ttk",
  title: "",
  effect: "pass_required",
  passZones: ["ttk", "sk"],
  document: "pp-379-2011",
  confirmed: true,
};

const act: Doc = {
  code: "pp-379-2011",
  kind: "act",
  title: "Об ограничении движения грузового автотранспорта",
  number: "379-ПП",
  signedOn: "2011-08-22",
  authority: "Правительство Москвы",
  status: "in_force",
};

describe("правила зон", () => {
  it("описывает массу, время и пропуск", () => {
    const r: Rule = {
      ...base,
      massBasis: "gross",
      massOverT: 3.5,
      timeFrom: "23:00",
      timeTo: "07:00",
      passKind: "night",
    };
    expect(ruleSubject(r)).toBe("РММ более 3,5 т · 23:00–07:00");
    expect(ruleRequirement(r)).toBe("ночной пропуск зоны «ТТК» или «СК»");
  });

  it("экокласс без времени — круглосуточно, без пропуска", () => {
    const r: Rule = { ...base, effect: "prohibited", ecoClassMin: 3, passZones: [] };
    expect(ruleSubject(r)).toBe("экологический класс ниже 3 · круглосуточно");
    expect(ruleRequirement(r)).toBeNull();
  });

  it("грузоподъёмность и перечень зон из трёх", () => {
    const r: Rule = { ...base, massBasis: "payload", massOverT: 1, passKind: "day", passZones: ["mkad", "ttk", "sk"] };
    expect(ruleSubject(r)).toBe("грузоподъёмность более 1 т · круглосуточно");
    expect(ruleRequirement(r)).toBe("дневной пропуск зоны «МКАД», «ТТК» или «СК»");
  });

  it("основание — номер акта и пункт", () => {
    const docs = new Map([[act.code, act]]);
    expect(ruleBasis({ ...base, clause: "п. 2.4 (ред. 399-ПП)" }, docs)).toBe("379-ПП, п. 2.4 (ред. 399-ПП)");
    expect(ruleBasis({ ...base, document: "unknown" }, docs)).toBe("unknown");
    expect(docRequisites(act)).toBe("№ 379-ПП от 22.08.2011");
    expect(docRequisites({ ...act, number: undefined, signedOn: undefined, kind: "page" })).toBe(
      "официальная страница",
    );
    expect(docShort(act)).toBe("379-ПП");
    expect(docShort({ ...act, number: undefined, url: "https://transport.mos.ru/gruzoviki/faq" })).toBe(
      "transport.mos.ru",
    );
  });

  it("подпись месяца и непрерывный ряд месяцев", () => {
    expect(monthLabel("2021-05-01")).toBe("05.2021");
    expect(monthRange("2023-11-01", "2024-02-01")).toEqual(["2023-11-01", "2023-12-01", "2024-01-01", "2024-02-01"]);
    expect(monthRange("2024-02-01", "2024-02-01")).toEqual(["2024-02-01"]);
  });
});
