import { describe, expect, it } from "vitest";
import { navGroups, navItemFor, titleFor } from "./nav";
import { sections } from "./sections";

describe("titleFor", () => {
  it("находит раздел по адресу и вложенному адресу", () => {
    expect(titleFor("/map")).toBe("Карта");
    expect(titleFor("/admin/users")).toBe("Пользователи");
    expect(titleFor("/facilities/123")).toBe("Объекты");
    expect(titleFor("/profile")).toBe("Профиль");
  });

  it("не путает разделы с общим началом адреса", () => {
    expect(navItemFor("/mapping")).toBeUndefined();
    expect(titleFor("/unknown")).toBe("FreightFlow");
  });
});

describe("sections", () => {
  it("у каждого раздела-заглушки есть пункт меню с тем же правом", () => {
    const items = navGroups.flatMap((g) => g.items);
    for (const sec of sections) {
      const item = items.find((it) => it.to === sec.path);
      expect(item, sec.path).toBeDefined();
      expect(item?.permission).toBe(sec.permission);
    }
  });
});
