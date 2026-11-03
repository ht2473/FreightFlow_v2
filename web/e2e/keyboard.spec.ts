import { expect, type Locator, type Page, test } from "@playwright/test";
import { account, login } from "./support";

const analyst = account("FF_E2E");

test.skip(!analyst.email || !analyst.password, "нужны FF_E2E_EMAIL и FF_E2E_PASSWORD");

/** Нажимает Tab, пока фокус не окажется на элементе (не больше limit раз). */
async function tabTo(page: Page, target: Locator, limit = 80) {
  for (let i = 0; i < limit; i++) {
    await page.keyboard.press("Tab");
    if (await target.evaluate((el) => el === document.activeElement)) {
      return;
    }
  }
  throw new Error("элемент не получил фокус с клавиатуры");
}

test("клавиатура: «К содержанию», палитра Ctrl K, переход по разделу", async ({ page }) => {
  await login(page, analyst, "/dashboard");
  await expect(page.getByRole("heading", { level: 1, name: "Дашборд" })).toBeVisible();
  // Первая по Tab — ссылка мимо меню; Enter переносит фокус в содержимое.
  await page.keyboard.press("Tab");
  await expect(page.getByRole("link", { name: "К содержанию" })).toBeFocused();
  await page.keyboard.press("Enter");
  await expect(page.locator("main#content")).toBeFocused();
  await expect(page).toHaveURL(/\/dashboard/);

  // Палитра: Ctrl K, запрос, Enter — переход в раздел.
  await page.keyboard.press("Control+k");
  const dialog = page.getByRole("dialog");
  await expect(dialog).toBeVisible();
  await page.keyboard.type("Территории");
  await expect(dialog.getByRole("menuitem", { name: /Территории/ }).first()).toBeVisible();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/\/territories/);
  await expect(page.getByRole("heading", { level: 1, name: "Территории" })).toBeVisible();
});

test("клавиатура: реестр — поиск, карточка объекта, закрытие с возвратом фокуса", async ({ page }) => {
  await login(page, analyst, "/facilities");
  const table = page.getByRole("table", { name: "Реестр объектов" });
  await expect(table.getByRole("row").nth(1)).toBeVisible({ timeout: 30_000 });
  await tabTo(page, page.getByLabel("Поиск по названию или адресу"));
  await page.keyboard.type("Склад");
  await expect(page).toHaveURL(/q=/);
  // Объект открывается кнопкой в строке таблицы.
  const first = table.getByRole("row").nth(1).getByRole("button").first();
  await first.focus();
  await page.keyboard.press("Enter");
  const drawer = page.getByRole("dialog");
  await expect(drawer).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(drawer).toHaveCount(0);
  await expect(first).toBeFocused();
});

test("клавиатура: дашборд — настройка и перестановка виджета", async ({ page }) => {
  await login(page, analyst, "/dashboard");
  await expect(page.getByText("Объектов логистики", { exact: true })).toBeVisible({ timeout: 30_000 });
  await tabTo(page, page.getByRole("button", { name: "Настроить" }));
  await page.keyboard.press("Enter");
  const toolbars = page.getByRole("toolbar", { name: /^Виджет «/ });
  const was = await toolbars.first().getAttribute("aria-label");
  await tabTo(page, page.getByRole("button", { name: "Переместить позже" }).first());
  await page.keyboard.press("Enter");
  await expect.poll(async () => toolbars.first().getAttribute("aria-label")).not.toBe(was);
  // Обратно — той же кнопкой на новом месте: «раньше» у второго виджета.
  await toolbars.nth(1).getByRole("button", { name: "Переместить раньше" }).focus();
  await page.keyboard.press("Enter");
  await expect.poll(async () => toolbars.first().getAttribute("aria-label")).toBe(was);
  await page.getByRole("button", { name: "Готово" }).focus();
  await page.keyboard.press("Enter");
  await expect(toolbars).toHaveCount(0);
});

test("клавиатура: алерты — выбор алерта в ленте", async ({ page }) => {
  await login(page, analyst, "/alerts");
  const feed = page.getByRole("list", { name: "Алерты" });
  const empty = page.getByText("Алертов нет");
  await expect(feed.or(empty)).toBeVisible({ timeout: 15_000 });
  test.skip(await empty.isVisible(), "алертов нет");
  await feed.getByRole("button").first().focus();
  await page.keyboard.press("Enter");
  await expect(page).toHaveURL(/alert=/);
  await expect(page.getByRole("heading", { name: "Журнал" })).toBeVisible();
});
