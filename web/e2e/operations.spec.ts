import { expect, test } from "@playwright/test";
import { account, axeViolations, login } from "./support";

const analyst = account("FF_E2E");

test.skip(!analyst.email || !analyst.password, "нужны FF_E2E_EMAIL и FF_E2E_PASSWORD");

test("обстановка: показатели работ, динамика, фильтр, переход на карту", async ({ page }, info) => {
  test.setTimeout(60_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page, analyst, "/operations");
  await expect(page.getByRole("heading", { level: 1, name: "Оперативная обстановка" })).toBeVisible();
  await expect(page.getByText("Идут сейчас", { exact: true })).toBeVisible({ timeout: 30_000 });
  const table = page.getByRole("table", { name: "Дорожные работы" });
  await expect(table.getByRole("row").nth(1)).toBeVisible();
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.waitForTimeout(600);
    expect(await axeViolations(page)).toEqual([]);
    await page.screenshot({ path: info.outputPath(`operations-${scheme}.png`), fullPage: true });
  }
  await page.emulateMedia({ colorScheme: "light" });

  // Фильтр «С перекрытием»: в каждой строке — отметка перекрытия.
  const all = await table.getByRole("row").count();
  await page.getByRole("radio", { name: "С перекрытием" }).click();
  const rows = table.getByRole("row");
  const n = await rows.count();
  expect(n).toBeLessThanOrEqual(all);
  for (let i = 1; i < Math.min(n, 6); i++) {
    await expect(rows.nth(i).getByText("перекрытие", { exact: true })).toBeVisible();
  }

  // Поиск по месту сужает список; затем переход к работе на карте.
  await page.getByRole("radio", { name: "Все" }).click();
  const title = (await rows.nth(1).getByRole("cell").first().locator("div").first().textContent()) ?? "";
  await page.getByLabel("Поиск", { exact: true }).fill(title);
  await expect(page.getByText(/^Показано: [1-9]/)).toBeVisible();
  await table.getByRole("link", { name: "На карте" }).first().click();
  await expect(page).toHaveURL(/\/map\?.*pin=/);
  await expect(page.locator(".maplibregl-canvas")).toBeVisible();
  expect(errors).toEqual([]);
});
