import { describe, expect, it } from "vitest";
import { navItemFor, titleFor } from "./nav";

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
