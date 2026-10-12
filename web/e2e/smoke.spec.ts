import { expect, type Page, test } from "@playwright/test";
import { account, axeViolations, login as loginAs } from "./support";

const analyst = account("FF_E2E");

test.skip(!analyst.email || !analyst.password, "нужны FF_E2E_EMAIL и FF_E2E_PASSWORD");

const login = (page: Page, next = "/") => loginAs(page, analyst, next);

test("вход, оболочка, палитра команд, тема и выход", async ({ page }) => {
  await login(page);
  await expect(page).toHaveURL(/\/dashboard$/);
  await expect(page.getByRole("heading", { name: "Дашборд" })).toBeVisible();

  // Меню учитывает права: у аналитика нет администрирования.
  const nav = page.getByRole("navigation", { name: "Разделы" });
  await expect(nav.getByRole("link", { name: "Карта" })).toBeVisible();
  await expect(nav.getByRole("link", { name: "Пользователи" })).toHaveCount(0);
  expect(await axeViolations(page)).toEqual([]);

  // Палитра команд.
  await page.keyboard.press("Control+k");
  await page.getByRole("searchbox", { name: "Команда или раздел" }).fill("проф");
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/profile$/);
  await expect(page.getByRole("heading", { name: "Профиль" })).toBeVisible();
  expect(await axeViolations(page)).toEqual([]);

  // Тема применяется сразу и сохраняется в профиле.
  await page.getByRole("button", { name: "Меню пользователя" }).click();
  const saved = page.waitForResponse((r) => r.url().endsWith("/api/v1/me/settings") && r.request().method() === "PUT");
  await page.getByRole("menuitem", { name: "Тёмная тема" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  expect((await saved).ok()).toBe(true);
  await page.reload();
  await expect(page.locator("html")).toHaveAttribute("data-theme", "dark");
  expect(await axeViolations(page)).toEqual([]);

  // Возврат к системной теме, чтобы тест не менял состояние учётной записи.
  await page.getByRole("button", { name: "Меню пользователя" }).click();
  await page.getByRole("menuitem", { name: "Как в системе" }).click();
  await expect(page.locator("html")).not.toHaveAttribute("data-theme", /./);

  await page.getByRole("button", { name: "Меню пользователя" }).click();
  await page.getByRole("menuitem", { name: "Выйти" }).click();
  await expect(page).toHaveURL(/\/login$/);
});

test("раздел без права показывает отказ, адрес возврата сохраняется", async ({ page }) => {
  await login(page, "/admin/users");
  await expect(page).toHaveURL(/\/admin\/users$/);
  await expect(page.getByText("Нет доступа")).toBeVisible();
});

test("неизвестный адрес — страница «не найдено»", async ({ page }) => {
  await login(page, "/no-such-page");
  await expect(page.getByText("Страница не найдена")).toBeVisible();
});
