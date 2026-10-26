import { expect, test } from "@playwright/test";
import { account, axeViolations, login } from "./support";

const analyst = account("FF_E2E");

test.skip(!analyst.email || !analyst.password, "нужны FF_E2E_EMAIL и FF_E2E_PASSWORD");

// Нужен посчитанный пакет устойчивости (фоновая задача после сетевого анализа).
test("устойчивость сети: рейтинг ущерба, слой и переход к сценарию", async ({ page }, info) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page, analyst, "/network/analysis");
  const section = page.locator("section").filter({ hasText: "Устойчивость: ущерб от выпадения" }).last();
  await expect(section).toBeVisible({ timeout: 30_000 });
  const rows = section.getByRole("row");
  await expect(rows.nth(1)).toBeVisible();
  await expect(section.getByText(/Пакет от/)).toBeVisible();
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.waitForTimeout(500);
    expect(await axeViolations(page)).toEqual([]);
    await section.screenshot({ path: info.outputPath(`resilience-${scheme}.png`) });
  }
  await page.emulateMedia({ colorScheme: "light" });

  // Первое место — сценарий с перекрытием этим элементом.
  const name = (await rows.nth(1).getByRole("cell").nth(1).innerText()).split("\n")[0] ?? "";
  await rows.nth(1).getByRole("link", { name: /^Сценарий с перекрытием/ }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Новый сценарий" })).toBeVisible();
  await expect(page.getByRole("button", { name: new RegExp(`Убрать перекрытие «${name}`) })).toBeVisible();
  await expect(page.getByLabel("Название")).toHaveValue(new RegExp("^Выпадение: "));

  // Слой устойчивости на карте.
  await page.goto("/map?l=resilience,rings&z=11");
  await expect(page.locator(".maplibregl-canvas")).toBeVisible();
  await page.waitForTimeout(2500);
  await expect(page.getByText("Устойчивость сети").first()).toBeVisible();
  await page.screenshot({ path: info.outputPath("resilience-map.png") });
  expect(errors).toEqual([]);
});
