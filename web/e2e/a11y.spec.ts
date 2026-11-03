import { expect, type Page, test } from "@playwright/test";
import { account, axeViolations, login } from "./support";

const analyst = account("FF_E2E");
const admin = account("FF_DEV_ADMIN");

// Все разделы рабочего пространства, доступные аналитику, и страницы справки.
const analystPages = [
  "/dashboard",
  "/map",
  "/operations",
  "/alerts",
  "/alerts?tab=rules",
  "/alerts/rules/new",
  "/facilities",
  "/facilities/compare",
  "/territories",
  "/network",
  "/network/analysis",
  "/regulation",
  "/analytics",
  "/analytics?tab=indexes",
  "/analytics?tab=series",
  "/analytics?tab=spatial",
  "/analytics/methods",
  "/scenarios",
  "/scenarios/new",
  "/site-selection",
  "/reports",
  "/reports?tab=schedules",
  "/search?q=склад",
  "/api-docs",
  "/profile",
  "/help/guide",
  "/help/methods",
  "/help/sources",
  "/help/glossary",
  "/help/about",
];

// Разделы администратора и редактора данных.
const adminPages = ["/admin/users", "/admin/roles", "/admin/audit", "/data"];

async function check(page: Page, path: string) {
  await page.goto(path);
  await page.waitForLoadState("networkidle").catch(() => {});
  await expect(page.getByText("Не удалось показать страницу")).toHaveCount(0);
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    // Переход цветов темы и дорисовка карт.
    await page.waitForTimeout(600);
    expect.soft(await axeViolations(page), `${path} (${scheme})`).toEqual([]);
  }
  await page.emulateMedia({ colorScheme: "light" });
}

test("доступность: все разделы аналитика в обеих темах", async ({ page }) => {
  test.skip(!analyst.email || !analyst.password, "нужны FF_E2E_EMAIL и FF_E2E_PASSWORD");
  test.setTimeout(300_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(`${page.url()}: ${e.message}`));
  await login(page, analyst, "/dashboard");
  for (const path of analystPages) {
    await check(page, path);
  }
  expect(errors).toEqual([]);
});

test("доступность: разделы администратора в обеих темах", async ({ page }) => {
  test.skip(!admin.email || !admin.password, "нужны FF_DEV_ADMIN_EMAIL и FF_DEV_ADMIN_PASSWORD");
  test.setTimeout(120_000);
  await login(page, admin, "/admin/users");
  // Со вторым фактором вход в тестах невозможен; без него администратору предлагается его подключить.
  test.skip(page.url().includes("/login/2fa"), "у администратора включён второй фактор");
  for (const path of adminPages) {
    await check(page, path);
  }
});
