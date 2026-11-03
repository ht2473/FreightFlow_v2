import { expect, test } from "@playwright/test";
import { account, axeViolations, login } from "./support";

const analyst = account("FF_E2E");

test.skip(!analyst.email || !analyst.password, "нужны FF_E2E_EMAIL и FF_E2E_PASSWORD");

test("справка: руководство, методики из каталога, источники, словарь, о системе", async ({ page }, info) => {
  test.setTimeout(90_000);
  await login(page, analyst, "/dashboard");
  await page.getByRole("button", { name: "Меню пользователя" }).click();
  await page.getByRole("menuitem", { name: "Справка" }).click();
  await expect(page).toHaveURL(/\/help\/guide$/);
  await expect(page.getByRole("heading", { level: 1, name: "Руководство" })).toBeVisible();
  const nav = page.getByRole("navigation", { name: "Разделы справки" });
  await expect(nav.getByRole("link", { name: "Руководство по разделам" })).toHaveAttribute("aria-current", "page");

  await nav.getByRole("link", { name: "Методики показателей" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Методики показателей" })).toBeVisible();
  await expect(page.locator('article[id="territory.population_coverage"]')).toBeVisible();
  expect(await page.locator("article.help-card").count()).toBeGreaterThan(20);

  await nav.getByRole("link", { name: "Источники и лицензии" }).click();
  await expect(page.getByRole("table", { name: "Источники данных" }).getByRole("row")).not.toHaveCount(1);
  await expect(page.getByText("CC BY 4.0").first()).toBeVisible();

  await nav.getByRole("link", { name: "Словарь" }).click();
  await expect(page.getByText("TOPSIS", { exact: true })).toBeVisible();
  await nav.getByRole("link", { name: "О системе" }).click();
  await expect(page.getByText("Лицензия MIT.")).toBeVisible();

  for (const path of ["/help/guide", "/help/methods", "/help/sources", "/help/glossary", "/help/about"]) {
    await page.goto(path);
    for (const scheme of ["light", "dark"] as const) {
      await page.emulateMedia({ colorScheme: scheme });
      expect(await axeViolations(page), `${path} ${scheme}`).toEqual([]);
    }
    await page.emulateMedia({ colorScheme: "light" });
  }
  await page.goto("/help/methods");
  await page.screenshot({ path: info.outputPath("help-methods.png") });

  // Без входа — на страницу входа с возвратом.
  await page.context().clearCookies();
  await page.goto("/help/glossary");
  await expect(page).toHaveURL(/\/login\?next=%2Fhelp%2Fglossary/);
});

test("знакомство с системой: при первом входе и из меню", async ({ page }) => {
  test.setTimeout(60_000);
  await login(page, analyst, "/dashboard", { tour: true });
  await page.evaluate(() => localStorage.removeItem("ff.tour"));
  await page.reload();
  const dlg = page.getByRole("dialog", { name: "Добро пожаловать в FreightFlow" });
  await expect(dlg).toBeVisible();
  expect(await axeViolations(page)).toEqual([]);
  await dlg.getByRole("button", { name: "Далее" }).click();
  await expect(page.getByRole("dialog", { name: "Поиск — Ctrl K" })).toBeVisible();
  await expect(page.getByText("Шаг 2 из 5")).toBeVisible();
  for (let i = 0; i < 3; i++) {
    await page.getByRole("dialog").getByRole("button", { name: "Далее" }).click();
  }
  await page.getByRole("dialog", { name: "Отчёты и справка" }).getByRole("button", { name: "Начать работу" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.reload();
  await expect(page.getByRole("heading", { level: 1, name: "Дашборд" })).toBeVisible();
  await expect(page.getByRole("dialog")).toHaveCount(0);
  await page.getByRole("button", { name: "Меню пользователя" }).click();
  await page.getByRole("menuitem", { name: "Знакомство с системой" }).click();
  await expect(page.getByRole("dialog", { name: "Добро пожаловать в FreightFlow" })).toBeVisible();
  await page.getByRole("dialog").getByRole("button", { name: "Не сейчас" }).click();
  await expect(page.getByRole("dialog")).toHaveCount(0);
});
