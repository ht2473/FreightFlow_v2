import { randomBytes } from "node:crypto";
import { expect, test } from "@playwright/test";
import { account, login } from "./support";

const admin = account("FF_DEV_ADMIN");

test.skip(!admin.email || !admin.password, "нужны FF_DEV_ADMIN_EMAIL и FF_DEV_ADMIN_PASSWORD");

// Отдельная учётная запись для блокировки: сеансы остальных тестов не затрагиваются.
const target = "e2e-blocking@example.com";

test("администрирование: пользователь, блокировка и разблокировка, матрица ролей, журнал", async ({ page }) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page, admin, "/admin/users");
  test.skip(page.url().includes("/login/2fa"), "у администратора включён второй фактор");
  await page.goto("/admin/users");
  await expect(page.getByRole("heading", { level: 1, name: "Пользователи" })).toBeVisible();

  const search = page.getByRole("searchbox", { name: "Поиск пользователей" });
  await search.fill(target);
  const row = page.getByRole("row").filter({ hasText: target });
  if ((await row.count()) === 0) {
    // Учётная запись создаётся один раз; пароль случайный — входить под ней тест не будет.
    await page.getByRole("button", { name: "Создать" }).click();
    const dlg = page.getByRole("dialog", { name: "Новый пользователь" });
    await dlg.getByLabel("Электронная почта").fill(target);
    await dlg.getByLabel("Имя").fill("Проверка блокировки");
    await dlg.getByLabel("Пароль", { exact: true }).fill(`${randomBytes(12).toString("base64url")} 2026`);
    await dlg.getByRole("checkbox", { name: /Наблюдатель/ }).check();
    await dlg.getByRole("button", { name: "Создать" }).click();
    await expect(dlg).toHaveCount(0);
    await search.fill(target);
  }
  await expect(row).toHaveCount(1);

  // Блокировка и обратно — через меню действий строки.
  const act = async (item: string, badge: string) => {
    await row.getByRole("button", { name: /^Действия: / }).click();
    await page.getByRole("menuitem", { name: item }).click();
    await expect(row.getByText(badge, { exact: true })).toBeVisible();
  };
  if (await row.getByText("Заблокирован", { exact: true }).isVisible()) {
    await act("Разблокировать", "Активен");
  }
  await act("Заблокировать", "Заблокирован");
  await act("Разблокировать", "Активен");

  // Матрица ролей и журнал: смена статуса записана.
  await page.goto("/admin/roles");
  await expect(page.getByRole("region", { name: "Матрица прав по ролям" }).getByRole("table")).toBeVisible();
  await page.goto("/admin/audit");
  await page.getByRole("button", { name: /Действие/ }).click();
  await page.getByRole("option", { name: "Изменение статуса" }).click();
  await expect(page.getByRole("row").filter({ hasText: "Изменение статуса" }).first()).toBeVisible();
  expect(errors).toEqual([]);
});
