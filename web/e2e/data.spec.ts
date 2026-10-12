import { expect, test } from "@playwright/test";
import { account, axeViolations, login } from "./support";

const steward = account("FF_DEV_STEWARD");

test.skip(!steward.email || !steward.password, "нужны FF_DEV_STEWARD_EMAIL и FF_DEV_STEWARD_PASSWORD");

// Запуск загрузки здесь не нажимается: он ставит в очередь настоящую загрузку источника.
test("источники данных: список, карточка, описание; обе темы", async ({ page }, info) => {
  await login(page, steward, "/data");
  await expect(page).toHaveURL(/\/data$/);
  await expect(page.getByRole("heading", { name: "Источники данных" })).toBeVisible();

  const row = page.getByRole("row", { name: /OpenStreetMap/ });
  await expect(row).toBeVisible();
  await expect(row.getByRole("button", { name: "Загрузить" })).toBeVisible();
  await expect(row).toContainText("Раз в неделю");

  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    expect(await axeViolations(page)).toEqual([]);
    await page.screenshot({ path: info.outputPath(`sources-${scheme}.png`), fullPage: true });
  }

  // Меню действий не открывает карточку.
  await row.getByRole("button", { name: "Ещё: OpenStreetMap" }).click();
  await expect(page.getByRole("menuitem", { name: "Выключить расписание" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.getByRole("dialog")).toHaveCount(0);

  await row.getByText("OpenStreetMap", { exact: true }).click();
  const drawer = page.getByRole("dialog", { name: "OpenStreetMap" });
  await expect(drawer).toBeVisible();
  await drawer.getByRole("tab", { name: "Описание" }).click();
  await expect(drawer.getByText("ODbL 1.0")).toBeVisible();
  await expect(drawer.getByText("© участники OpenStreetMap")).toBeVisible();
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    expect(await axeViolations(page)).toEqual([]);
    await page.screenshot({ path: info.outputPath(`source-drawer-${scheme}.png`) });
  }
});
