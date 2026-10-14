import { expect, test } from "@playwright/test";
import { account, axeViolations, login } from "./support";

const analyst = account("FF_E2E");

test.skip(!analyst.email || !analyst.password, "нужны FF_E2E_EMAIL и FF_E2E_PASSWORD");

// Нужны служба маршрутизации (task routing:up) и граф (task routing:build).
test("маршрут грузовика: точки на карте, пропуск, объяснение обходов", async ({ page }, info) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page, analyst, "/network");
  await expect(page.getByRole("heading", { level: 1, name: "Маршрут грузовика" })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText("Служба маршрутизации не запущена")).toBeHidden();

  // Карта по умолчанию — центр Москвы: обе точки внутри Садового кольца и ТТК.
  const canvas = page.locator(".maplibregl-canvas");
  await expect(canvas).toBeVisible();
  await page.waitForTimeout(1500);
  const box = await canvas.boundingBox();
  if (!box) {
    throw new Error("нет карты");
  }
  const cx = box.x + box.width / 2;
  const cy = box.y + box.height / 2;
  await page.mouse.click(cx - 40, cy + 30);
  await page.mouse.click(cx + 60, cy - 40);
  await expect(page.getByRole("button", { name: "Убрать точку B" })).toBeVisible();

  // Грузовик 20 т без пропуска: точки в закрытой зоне, маршрут не строится.
  await page.getByLabel("Отправление").fill("2026-10-14T10:00");
  await page.getByRole("button", { name: "Построить маршрут" }).click();
  await expect(page.getByText(/Точка A в зоне «.+», закрытой для этого ТС/)).toBeVisible({ timeout: 20_000 });
  await page.screenshot({ path: info.outputPath("route-blocked.png") });

  // С дневным пропуском зоны «СК» — маршрут, сравнение и зоны.
  // Поле флажка React Aria скрыто — нажимается подпись.
  await page
    .locator("label")
    .filter({ has: page.getByRole("checkbox", { name: "дневной пропуск зоны «СК»" }) })
    .click();
  await expect(page.getByRole("checkbox", { name: "дневной пропуск зоны «СК»" })).toBeChecked();
  await page.getByRole("button", { name: "Построить маршрут" }).click();
  const resultRegion = page.getByRole("region", { name: "Результат" });
  await expect(resultRegion.getByText("Маршрут грузовика", { exact: true })).toBeVisible({ timeout: 20_000 });
  await expect(page.getByText("Без ограничений (легковой)")).toBeVisible();
  const zones = resultRegion.getByRole("listitem");
  await expect(zones.filter({ hasText: "МКАД" }).getByText("открыта")).toBeVisible();
  await expect(page.getByRole("button", { name: "GeoJSON" })).toBeVisible();

  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.waitForTimeout(1000);
    expect(await axeViolations(page)).toEqual([]);
    await page.screenshot({ path: info.outputPath(`route-${scheme}.png`) });
  }

  // Изохроны от точки A: контуры по выбранному времени.
  await page.getByRole("tab", { name: "Изохроны" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Изохроны доступности" })).toBeVisible();
  await page.getByRole("button", { name: "Построить изохроны" }).click();
  await expect(page.getByText("до 15 мин")).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText("до 60 мин")).toBeVisible();
  await page.waitForTimeout(1000);
  await page.screenshot({ path: info.outputPath("isochrones.png") });
  expect(errors).toEqual([]);
});

test("точка маршрута по адресу и объекту", async ({ page }, info) => {
  test.setTimeout(60_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page, analyst, "/network");
  await expect(page.getByRole("heading", { level: 1, name: "Маршрут грузовика" })).toBeVisible({ timeout: 30_000 });
  const field = page.getByRole("combobox", { name: "Добавить точку по адресу или объекту" });
  await field.fill("Газгольдерная 10");
  const option = page.getByRole("option").filter({ hasText: "Газгольдерная улица, дом 10" }).first();
  await expect(option).toBeVisible({ timeout: 15_000 });
  await page.screenshot({ path: info.outputPath("place-search.png") });
  await option.click();
  await expect(page.getByRole("listitem").filter({ hasText: "Газгольдерная улица, дом 10" })).toBeVisible();
  await expect(page.getByRole("button", { name: "Убрать точку A" })).toBeVisible();
  await expect(field).toHaveValue("");
  expect(await axeViolations(page)).toEqual([]);
  expect(errors).toEqual([]);
});
