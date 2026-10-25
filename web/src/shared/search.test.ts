import { describe, expect, it } from "vitest";
import { groupHits, type Hit, hitTarget, type Parsed, registrySearch } from "./search";

const parsed = (over: Partial<Parsed>): Parsed => ({
  text: "",
  chips: [],
  facility: true,
  types: [],
  okrugs: [],
  districts: [],
  rings: [],
  ...over,
});

describe("поиск в интерфейсе", () => {
  it("разобранный запрос — параметры реестра", () => {
    const near: Hit = {
      kind: "street",
      ref: "x",
      title: "Курский вокзал",
      score: 1,
      point: { lon: 37.661234, lat: 55.756789 },
    };
    expect(
      registrySearch(
        parsed({
          types: ["warehouse", "terminal"],
          okrugs: ["o1"],
          minArea: 10000,
          maxMkadMin: 10,
          nearKm: 3,
          nearRestrictions: false,
        }),
        near,
      ),
    ).toMatchObject({
      type: "warehouse,terminal",
      okrug: "o1",
      district: undefined,
      amin: 10000,
      mkad: 10,
      restr: "no",
      near: "37.66123,55.75679,3000",
    });
  });

  it("группы по видам в порядке показа; переходы", () => {
    const hits: Hit[] = [
      { kind: "address", ref: "1", title: "Адрес", score: 1, point: { lon: 37.6, lat: 55.7 } },
      { kind: "territory", ref: "t", title: "Район", score: 1 },
      { kind: "facility", ref: "f", title: "Склад", score: 1 },
    ];
    expect(groupHits(hits).map((g) => g.kind)).toEqual(["facility", "territory", "address"]);
    expect(hitTarget(hits[0] as Hit)).toMatchObject({ to: "/map", search: { pin: "37.60000,55.70000", z: 17 } });
    expect(hitTarget(hits[1] as Hit)).toEqual({ to: "/territories/$territoryId", params: { territoryId: "t" } });
    expect(hitTarget({ kind: "dataset", ref: "mos_roads", title: "", score: 0 })).toEqual({
      to: "/data",
      search: { source: "mos_roads" },
    });
  });
});
