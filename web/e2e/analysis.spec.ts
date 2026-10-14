import { expect, test } from "@playwright/test";
import { account, axeViolations, login } from "./support";

const analyst = account("FF_E2E");

test.skip(!analyst.email || !analyst.password, "нужны FF_E2E_EMAIL и FF_E2E_PASSWORD");

// Нужен построенный граф анализа (freightflow routing analyze или фоновая задача).
test("анализ сети: точность, рейтинги улиц, критичных элементов и узких мест, переход на карту", async ({
  page,
}, info) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page, analyst, "/network/analysis");
  await expect(page.getByRole("heading", { level: 1, name: "Анализ сети" })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText("Связность для профиля")).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText(/узлов-источников из|точный расчёт по всем/).first()).toBeVisible();

  const streets = page.locator("section").filter({ hasText: "Улицы с наибольшей значимостью" }).getByRole("row");
  await expect(streets.nth(1)).toBeVisible();
  expect(await streets.count()).toBeGreaterThan(5);
  const bottlenecks = page.locator("section").filter({ hasText: "Узкие места по габаритам" }).getByRole("row");
  await expect(bottlenecks.nth(1)).toBeVisible();
  const critical = page.locator("section").filter({ hasText: "Критичные мосты и участки" }).getByRole("row");
  await expect(critical.nth(1)).toBeVisible();
  await expect(critical.nth(1).getByText(/объекто-мин|без роста времени|не проверялся/)).toBeVisible();

  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.waitForTimeout(500);
    expect(await axeViolations(page)).toEqual([]);
    await page.screenshot({ path: info.outputPath(`analysis-${scheme}.png`) });
  }
  await page
    .locator("section")
    .filter({ hasText: "Критичные мосты и участки" })
    .last()
    .screenshot({
      path: info.outputPath("analysis-critical.png"),
    });
  await page
    .locator("section")
    .filter({ hasText: "Узкие места по габаритам" })
    .last()
    .screenshot({
      path: info.outputPath("analysis-bottlenecks.png"),
    });

  // Первая улица рейтинга — на карте со слоем значимости.
  await page.emulateMedia({ colorScheme: "light" });
  await streets.nth(1).getByRole("link").click();
  await expect(page).toHaveURL(/\/map\?.*l=edge_centrality/);
  await expect(page.locator(".maplibregl-canvas")).toBeVisible();
  await page.waitForTimeout(2500);
  await expect(page.getByText("Значимость участков").first()).toBeVisible();
  await page.screenshot({ path: info.outputPath("analysis-map.png") });

  // Из раздела маршрутов — переход к анализу.
  await page.goto("/network");
  await page.getByRole("link", { name: "Анализ сети" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Анализ сети" })).toBeVisible();
  expect(errors).toEqual([]);
});
