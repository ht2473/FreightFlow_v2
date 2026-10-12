import { describe, expect, it } from "vitest";
import { can, initials, type Me } from "./auth";

const me: Me = {
  id: "0192f5d0-0000-7000-8000-000000000001",
  email: "analyst@example.com",
  name: "Анна Смирнова",
  roles: ["analyst"],
  permissions: ["map.view", "analytics.view"],
  totpEnabled: false,
};

describe("can", () => {
  it("проверяет право пользователя", () => {
    expect(can(me, "map.view")).toBe(true);
    expect(can(me, "users.manage")).toBe(false);
    expect(can(undefined, "map.view")).toBe(false);
  });
});

describe("initials", () => {
  it("берёт первые буквы двух слов", () => {
    expect(initials("Анна Смирнова")).toBe("АС");
    expect(initials("  иван  ")).toBe("И");
    expect(initials("")).toBe("?");
  });
});
