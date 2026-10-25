import { expect, test } from "@playwright/test";
import { account, axeViolations, login } from "./support";

const analyst = account("FF_E2E");

test.skip(!analyst.email || !analyst.password, "нужны FF_E2E_EMAIL и FF_E2E_PASSWORD");

test("палитра Ctrl K: подсказки по группам, улица на карте, район, все результаты", async ({ page }, info) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page, analyst, "/dashboard");
  await expect(page.getByRole("heading", { name: "Дашборд" })).toBeVisible({ timeout: 30_000 });

  // Улица: группа «Улицы», переход на карту с меткой.
  await page.keyboard.press("Control+k");
  const input = page.getByRole("searchbox", { name: "Команда или раздел" });
  await input.fill("газгольдерная");
  const results = page.getByRole("menu", { name: "Результаты" });
  await expect(results.getByText("Улицы", { exact: true })).toBeVisible({ timeout: 15_000 });
  await expect(results.getByText("Адреса", { exact: true })).toBeVisible();
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.waitForTimeout(300);
    expect(await axeViolations(page)).toEqual([]);
    await page.screenshot({ path: info.outputPath(`palette-${scheme}.png`) });
  }
  await page.emulateMedia({ colorScheme: "light" });
  await results
    .getByRole("menuitem", { name: /^Газгольдерная улица/ })
    .first()
    .click();
  await expect(page).toHaveURL(/\/map\?.*pin=/);
  await expect(page).toHaveURL(/pt=/);

  // Район: профиль территории.
  await page.keyboard.press("Control+k");
  await input.fill("нижегородский");
  await results.getByRole("menuitem", { name: /^Нижегородский район/ }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Нижегородский район" })).toBeVisible({ timeout: 30_000 });

  // Недавнее — при пустом запросе; «Все результаты» — страница поиска.
  await page.keyboard.press("Control+k");
  await expect(results.getByText("Недавнее", { exact: true })).toBeVisible();
  await expect(results.getByRole("menuitem", { name: /Нижегородский район/ })).toBeVisible();
  await input.fill("379-ПП");
  await results.getByRole("menuitem", { name: "Все результаты по «379-ПП»" }).click();
  await expect(page).toHaveURL(/\/search\?q=379-%D0%9F%D0%9F|\/search\?q=379-ПП/);
  await expect(page.getByRole("list", { name: "Правовые акты" }).getByRole("button").first()).toContainText(
    "Об ограничении движения грузового автотранспорта",
    { timeout: 30_000 },
  );
  expect(errors).toEqual([]);
});

test("страница поиска: запрос в фильтры реестра, опечатка, карта", async ({ page }, info) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page, analyst, `/search?q=${encodeURIComponent("склады ЮВАО больше 10 000 м² у МКАД")}`);
  const filters = page.getByRole("list", { name: "Разобранные фильтры" });
  await expect(filters).toBeVisible({ timeout: 30_000 });
  for (const label of ["Склад", "ЮВАО", "Площадь ≥ 10 000 м²", "До МКАД ≤ 10 мин"]) {
    await expect(filters.getByText(label, { exact: true })).toBeVisible();
  }
  await expect(page.getByRole("heading", { name: /^Объекты по фильтрам: \d/ })).toBeVisible();
  await expect(page.getByRole("region", { name: "Карта результатов" }).locator("canvas")).toBeVisible();
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.waitForTimeout(800);
    expect(await axeViolations(page)).toEqual([]);
    await page.screenshot({ path: info.outputPath(`search-${scheme}.png`), fullPage: true });
  }
  await page.emulateMedia({ colorScheme: "light" });

  // Те же фильтры в реестре — их можно изменить.
  await page.getByRole("link", { name: /Открыть в реестре/ }).click();
  await expect(page).toHaveURL(/\/facilities\?.*type=warehouse/);
  await expect(page).toHaveURL(/amin=10000/);
  await expect(page).toHaveURL(/mkad=10/);
  const chips = page.getByRole("list", { name: "Активные фильтры" });
  await expect(chips.getByText("Площадь ≥ 10 000 м²")).toBeVisible();
  await expect(chips.getByText("Округ: 1")).toBeVisible();

  // Опечатка — похожие по написанию.
  await page.goto(`/search?q=${encodeURIComponent("газгалдерная")}`);
  await expect(page.getByText(/показаны похожие по написанию/)).toBeVisible({ timeout: 30_000 });
  await expect(page.getByRole("list", { name: "Улицы" }).getByText("Газгольдерная улица")).toBeVisible();
  // Документ открывается из списка.
  await page.getByRole("list", { name: "Улицы" }).getByRole("button").first().click();
  await expect(page).toHaveURL(/\/map\?.*pin=/);
  expect(errors).toEqual([]);
});

test("геокодер: «что здесь» на карте и адрес точки маршрута", async ({ page }) => {
  test.setTimeout(60_000);
  await login(page, analyst, "/dashboard");
  const res = await page.request.get(`/api/v1/geocode?q=${encodeURIComponent("улица Плющиха 18")}`);
  expect(res.ok()).toBe(true);
  const [hit] = (await res.json()) as { title: string; point: { lon: number; lat: number } }[];
  expect(hit?.title).toMatch(/Плющиха, дом 18/);
  const c = `${hit?.point.lon.toFixed(5)},${hit?.point.lat.toFixed(5)}`;

  // Пустое место карты (включены только кольца) — адрес и район.
  await page.goto(`/map?c=${c}&z=18&l=rings`);
  const canvas = page.locator(".maplibregl-canvas");
  await expect(canvas).toBeVisible({ timeout: 30_000 });
  await page.waitForTimeout(1500);
  const box = await canvas.boundingBox();
  await page.mouse.click((box?.x ?? 0) + (box?.width ?? 0) / 2, (box?.y ?? 0) + (box?.height ?? 0) / 2);
  const here = page.getByRole("region", { name: "Что здесь" });
  await expect(here.getByText(/Плющиха/)).toBeVisible({ timeout: 15_000 });
  await expect(here.getByRole("link", { name: "Хамовники" })).toBeVisible();

  // Маршрут отсюда — точка подписана адресом.
  await here.getByRole("link", { name: "Маршрут отсюда" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Маршрут грузовика" })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByRole("listitem").filter({ hasText: /Плющиха/ })).toBeVisible();
});
