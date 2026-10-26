import { expect, test } from "@playwright/test";
import { account, axeViolations, login } from "./support";

const analyst = account("FF_E2E");

test.skip(!analyst.email || !analyst.password, "нужны FF_E2E_EMAIL и FF_E2E_PASSWORD");

// Нужны служба маршрутизации, сетевой анализ (рейтинг критичности) и worker с очередью scenario.
test("сценарий: перекрытие моста, расчёт, сравнение на двух картах", async ({ page }, info) => {
  test.setTimeout(300_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page, analyst, "/scenarios");
  await expect(page.getByRole("heading", { level: 1, name: "Сценарии" })).toBeVisible({ timeout: 30_000 });
  expect(await axeViolations(page)).toEqual([]);

  await page.getByRole("link", { name: "Новый сценарий" }).first().click();
  await expect(page.getByRole("heading", { level: 1, name: "Новый сценарий" })).toBeVisible();
  const name = `Проверка: мост ${Date.now()}`;
  await page.getByLabel("Название").fill(name);
  await expect(page.getByRole("button", { name: "Рассчитать" })).toBeDisabled();

  // Перекрытие — первый мост рейтинга критичности на Ярославском шоссе.
  const search = page.getByLabel("Мост или участок из рейтинга критичности");
  await search.fill("Ярославское");
  // Подсказки фильтруются на сервере: ждём ответа на введённый текст.
  await expect(page.getByRole("option", { name: /Третье транспортное кольцо/ })).toHaveCount(0);
  await page.getByRole("option", { name: /Ярославское шоссе.*место 1 ·/ }).click();
  await expect(page.getByRole("button", { name: /Убрать перекрытие «Ярославское шоссе»/ })).toBeVisible();
  await page.waitForTimeout(1000);
  await page.screenshot({ path: info.outputPath("scenario-builder.png") });
  expect(await axeViolations(page)).toEqual([]);

  await page.getByRole("button", { name: "Рассчитать" }).click();
  await page.waitForURL(/\/scenarios\/[0-9a-f-]{36}$/);
  await expect(page.getByRole("heading", { level: 1, name })).toBeVisible();
  // Ход расчёта приходит событиями; итог — по событию scenario.computed.
  await expect(page.getByText("Готов", { exact: true })).toBeVisible({ timeout: 240_000 });
  await expect(page.getByText("Объектов с приростом времени")).toBeVisible();
  await expect(page.getByText(/Объекты с изменениями \(\d+\)/)).toBeVisible();
  await expect(page.getByLabel("База", { exact: true })).toBeVisible();
  await expect(page.getByLabel("Сценарий", { exact: true })).toBeVisible();
  await expect(page.locator(".maplibregl-canvas")).toHaveCount(2);

  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.waitForTimeout(1500);
    expect(await axeViolations(page)).toEqual([]);
    await page.screenshot({ path: info.outputPath(`scenario-result-${scheme}.png`) });
  }

  // Одна карта вместо двух.
  await page.locator("label").filter({ has: page.getByRole("switch", { name: "Сравнение на двух картах" }) }).click();
  await expect(page.locator(".maplibregl-canvas")).toHaveCount(1);

  // Сценарий в списке; удаление.
  await page.getByRole("link", { name: "Все сценарии" }).click();
  await expect(page.getByRole("link", { name: new RegExp(name) })).toBeVisible();
  await page.getByRole("link", { name: new RegExp(name) }).click();
  await page.getByRole("button", { name: "Удалить" }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Удалить" }).click();
  await page.waitForURL(/\/scenarios$/);
  await expect(page.getByRole("link", { name: new RegExp(name) })).toHaveCount(0);
  expect(errors).toEqual([]);
});
