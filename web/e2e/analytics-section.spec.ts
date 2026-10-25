import { expect, test } from "@playwright/test";
import { account, axeViolations, login } from "./support";

const analyst = account("FF_E2E");

test.skip(!analyst.email || !analyst.password, "нужны FF_E2E_EMAIL и FF_E2E_PASSWORD");

test("дашборд: плитки, ряды с прогнозом, рейтинги, настройка виджетов", async ({ page }, info) => {
  test.setTimeout(120_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page, analyst, "/dashboard");
  await page.request.delete("/api/v1/me/dashboard");
  await page.reload();
  await expect(page.getByRole("heading", { level: 1, name: "Дашборд" })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText("Объектов логистики", { exact: true })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByRole("img", { name: /Действующие пропуска грузовиков: ряд и прогноз/ })).toBeVisible();
  await expect(page.getByText(/Прогноз на 12 мес\./).first()).toBeVisible({ timeout: 30_000 });
  await expect(page.getByRole("list", { name: "Индекс логистической привлекательности" }).getByRole("link").first()).toBeVisible();
  await expect(page.getByRole("heading", { name: "Требуют внимания" })).toBeVisible();
  await expect(page.getByRole("list", { name: "Обновления данных" }).getByRole("listitem").first()).toBeVisible();
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.waitForTimeout(800);
    expect(await axeViolations(page)).toEqual([]);
    await page.screenshot({ path: info.outputPath(`dashboard-${scheme}.png`), fullPage: true });
  }
  await page.emulateMedia({ colorScheme: "light" });

  // Свой виджет: рейтинг районов по плотности складских площадей; затем сброс.
  await page.getByRole("button", { name: "Настроить" }).click();
  await page.getByRole("button", { name: "Добавить виджет" }).click();
  const dlg = page.getByRole("dialog", { name: "Добавить виджет" });
  await dlg.getByRole("button", { name: /Вид/ }).click();
  await page.getByRole("option", { name: "Рейтинг показателя" }).click();
  await dlg.getByRole("button", { name: /Показатель/ }).click();
  await page.getByRole("option", { name: "Плотность складских площадей" }).click();
  await dlg.getByRole("button", { name: "Добавить" }).click();
  await expect(page.getByRole("list", { name: "Рейтинг: Плотность складских площадей" })).toBeVisible({ timeout: 15_000 });
  await page.getByRole("button", { name: "По умолчанию" }).click();
  await expect(page.getByRole("list", { name: "Рейтинг: Плотность складских площадей" })).toHaveCount(0);
  expect(errors).toEqual([]);
});

test("аналитика: показатели с картограммой, индексы с весами, ряды, горячие точки, методики", async ({ page }, info) => {
  test.setTimeout(150_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page, analyst, "/analytics");
  await expect(page.getByRole("heading", { level: 1, name: "Аналитика" })).toBeVisible({ timeout: 30_000 });

  // Показатели: рейтинг районов и картограмма.
  const rating = page.getByRole("table", { name: /^Рейтинг: / });
  await expect(rating.getByRole("row").nth(1)).toBeVisible({ timeout: 30_000 });
  await expect(page.getByRole("region", { name: /^Картограмма: / }).locator("canvas")).toBeVisible();
  await expect(page.getByRole("list", { name: "Легенда картограммы" }).getByRole("listitem")).not.toHaveCount(0);
  await page.waitForTimeout(1500);
  expect(await axeViolations(page)).toEqual([]);
  await page.screenshot({ path: info.outputPath("analytics-metrics.png") });

  // Методика показателя — подсказка «?».
  await page.getByRole("button", { name: /^Методика: / }).first().click();
  await expect(page.getByRole("dialog", { name: /^Методика: / }).getByRole("link", { name: "Все методики" })).toBeVisible();
  await page.keyboard.press("Escape");

  // Индексы: изменение веса пересчитывает рейтинг без сохранения; чувствительность.
  await page.goto("/analytics?tab=indexes&ix=index.attractiveness&il=district");
  const sens = page.getByRole("table", { name: "Чувствительность к весам" });
  await expect(sens.getByRole("row").nth(1)).toBeVisible({ timeout: 30_000 });
  const indexRating = page.getByRole("table", { name: /^Рейтинг: Индекс/ });
  const firstBefore = await indexRating.getByRole("row").nth(1).getByRole("cell").nth(1).innerText();
  const weight = page.getByRole("textbox", { name: "Вес: Индекс доступности" });
  await weight.fill("0");
  await weight.press("Tab");
  await expect(sens.getByRole("row")).toHaveCount(4, { timeout: 15_000 });
  await page.screenshot({ path: info.outputPath("analytics-index.png") });
  expect(firstBefore.length).toBeGreaterThan(0);

  // Ряды: история, прогноз, точность моделей.
  await page.getByRole("tab", { name: "Ряды и прогнозы" }).click();
  await expect(page.getByRole("table", { name: "Точность моделей на истории" }).getByText(/— выбрана/)).toBeVisible({
    timeout: 30_000,
  });
  const [csv] = await Promise.all([
    page.waitForEvent("download"),
    page.getByRole("button", { name: "Скачать данные графика" }).click(),
  ]);
  expect(csv.suggestedFilename()).toMatch(/^city\..+\.csv$/);
  await page.screenshot({ path: info.outputPath("analytics-series.png") });

  // Пространственный анализ: горячие точки и кластеры.
  await page.getByRole("tab", { name: "Пространственный анализ" }).click();
  await expect(page.getByText(/Getis–Ord Gi\* по \d/)).toBeVisible({ timeout: 30_000 });
  await page.screenshot({ path: info.outputPath("analytics-hotspots.png") });
  await page.getByRole("radio", { name: "Кластеры объектов (DBSCAN)" }).click();
  await expect(page.getByRole("table", { name: "Кластеры объектов" }).getByRole("row").nth(1)).toBeVisible({
    timeout: 30_000,
  });
  await page.screenshot({ path: info.outputPath("analytics-clusters.png") });

  // Методики.
  await page.getByRole("link", { name: "Методики" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Методики показателей" })).toBeVisible();
  await expect(page.getByText("territory.population_coverage")).toBeVisible();
  expect(await axeViolations(page)).toEqual([]);
  expect(errors).toEqual([]);
});

test("профиль территории: рассчитанные показатели с местом и методикой", async ({ page }) => {
  test.setTimeout(60_000);
  await login(page, analyst, "/territories?kind=okrug");
  await page.getByRole("table", { name: "Округа" }).getByRole("link").first().click();
  const table = page.getByRole("table", { name: "Показатели территории" });
  await expect(table.getByRole("row").nth(1)).toBeVisible({ timeout: 30_000 });
  await expect(table.getByText(/\d+ из 12/).first()).toBeVisible();
  await expect(table.getByText("Покрытие населения объектами (3 км)")).toBeVisible();
});
