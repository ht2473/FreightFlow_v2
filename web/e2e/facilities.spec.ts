import { expect, test } from "@playwright/test";
import { account, axeViolations, login } from "./support";

const analyst = account("FF_E2E");

test.skip(!analyst.email || !analyst.password, "нужны FF_E2E_EMAIL и FF_E2E_PASSWORD");

test("реестр: фасеты, сортировка, страницы, карточка", async ({ page }, info) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page, analyst, "/facilities");
  await expect(page.getByRole("heading", { level: 1, name: "Объекты" })).toBeVisible({ timeout: 30_000 });
  const table = page.getByRole("table", { name: "Реестр объектов" });
  await expect(table.getByRole("row").nth(1)).toBeVisible({ timeout: 30_000 });
  await expect(page.getByText(/Найдено объектов: \d/)).toBeVisible();
  await expect(page.getByText(/^1–50 из /)).toBeVisible();

  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.waitForTimeout(500);
    expect(await axeViolations(page)).toEqual([]);
    await page.screenshot({ path: info.outputPath(`registry-${scheme}.png`) });
  }
  await page.emulateMedia({ colorScheme: "light" });

  // Фасет типа: фильтр в адресе, чип, меньше объектов.
  const facets = page.getByRole("complementary", { name: "Фильтры" });
  await facets.locator("label").filter({ hasText: "Склад" }).first().click();
  await expect(page).toHaveURL(/type=warehouse/);
  await expect(page.getByRole("list", { name: "Активные фильтры" }).getByText("Тип: 1")).toBeVisible();
  await expect(table.getByRole("row").nth(1)).toContainText("Склад");

  // Сортировка по площади застройки — по убыванию после двух нажатий.
  const areaHeader = table.getByRole("columnheader", { name: /Площадь застройки/ });
  await areaHeader.getByRole("button").click();
  await expect(page).toHaveURL(/sort=/);
  await areaHeader.getByRole("button").click();
  await expect(areaHeader).toHaveAttribute("aria-sort", /descending|ascending/);

  // Следующая страница.
  await page.getByRole("button", { name: "Следующая страница" }).click();
  await expect(page.getByText(/^51–/)).toBeVisible();

  // Карточка: вкладки, доступность.
  await table.getByRole("row").nth(1).getByRole("button").first().click();
  const drawer = page.getByRole("dialog");
  await expect(drawer.getByRole("tab", { name: "Обзор" })).toBeVisible();
  await expect(drawer.getByText("Площадь застройки")).toBeVisible();
  await drawer.getByRole("tab", { name: "Доступность" }).click();
  await expect(drawer.getByRole("columnheader", { name: /МКАД/ })).toBeVisible();
  await page.screenshot({ path: info.outputPath("registry-card.png") });
  expect(await axeViolations(page)).toEqual([]);
  await drawer.getByRole("tab", { name: "Источники" }).click();
  await expect(drawer.getByText("OpenStreetMap", { exact: true })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page).not.toHaveURL(/open=/);
  expect(errors).toEqual([]);
});
