import { expect, test } from "@playwright/test";
import { account, login } from "./support";

const analyst = account("FF_E2E");

test.skip(!analyst.email || !analyst.password, "нужны FF_E2E_EMAIL и FF_E2E_PASSWORD");

// Сквозной сценарий аналитика: фильтр реестра → карта → карточка → сравнение → выгрузка;
// выделение области; сохранение и применение вида.
test("сквозной сценарий рабочего пространства", async ({ page }, info) => {
  test.setTimeout(150_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  const view = `Склады на карте e2e ${Date.now()}`;

  // 1. Фильтр реестра.
  await login(page, analyst, "/facilities");
  await page.evaluate(() => localStorage.removeItem("ff.facilities.compare"));
  await page.reload();
  const table = page.getByRole("table", { name: "Реестр объектов" });
  await expect(table.getByRole("row").nth(1)).toBeVisible({ timeout: 30_000 });
  await page
    .getByRole("complementary", { name: "Фильтры" })
    .locator("label")
    .filter({ hasText: "Склад" })
    .first()
    .click();
  await expect(page).toHaveURL(/type=warehouse/);

  // 2. Та же выборка на карте; объект из списка видимой области — карточка реестра, в сравнение.
  await page.getByRole("link", { name: "На карте" }).click();
  await expect(page).toHaveURL(/\/map\?.*ft=warehouse/);
  await expect(page.getByText("Объекты — по фильтру реестра")).toBeVisible();
  const objects = page.getByRole("tabpanel", { name: "Объекты" });
  await expect(objects.getByText(/В видимой области · [1-9]/)).toBeVisible({ timeout: 30_000 });
  await expect(objects.getByText(/^Склад/).first()).toBeVisible();
  await objects.getByRole("button").first().click();
  const card = page.getByRole("dialog");
  await expect(card.getByRole("tab", { name: "Обзор" })).toBeVisible();
  await card.getByRole("button", { name: "Сравнить" }).click();
  await expect(card.getByRole("button", { name: "Убрать из сравнения" })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(card).toHaveCount(0);

  // 3. Выделение области вокруг объекта — сводка.
  await page.waitForTimeout(1500);
  const box = await page.locator(".maplibregl-canvas").boundingBox();
  if (!box) {
    throw new Error("нет карты");
  }
  const cx = box.x + box.width / 2;
  const cy = box.y + box.height / 2;
  await page.getByRole("button", { name: "Выделить область" }).click();
  for (const [dx, dy] of [
    [-160, -120],
    [160, -120],
    [160, 120],
    [-160, 120],
    [-160, -120],
  ] as const) {
    await page.mouse.click(cx + dx, cy + dy);
    await page.waitForTimeout(200);
  }
  const summary = page.getByRole("region", { name: "Сводка по области" });
  await expect(summary.getByText("Объектов реестра")).toBeVisible({ timeout: 30_000 });
  await page.screenshot({ path: info.outputPath("flow-area.png") });
  await summary.getByRole("button", { name: "Закрыть сводку" }).click();

  // 4. Сохранение вида карты с фильтром реестра.
  await page.getByRole("button", { name: "Виды" }).click();
  await page.getByRole("button", { name: "Сохранить текущий вид" }).click();
  const dlg = page.getByRole("dialog", { name: "Сохранить вид" });
  await dlg.getByRole("textbox", { name: "Название" }).fill(view);
  await dlg.getByRole("button", { name: "Сохранить" }).click();
  await expect(page.getByText("Вид сохранён")).toBeVisible();

  // 5. Второй объект из реестра — сравнение.
  await page.goto("/facilities?type=warehouse&sort=-area");
  await expect(table.getByRole("row").nth(2)).toBeVisible({ timeout: 30_000 });
  for (const n of [1, 2]) {
    await table.getByRole("row").nth(n).getByRole("button").first().click();
    const drawer = page.getByRole("dialog");
    const add = drawer.getByRole("button", { name: "Сравнить" });
    await expect(add.or(drawer.getByRole("button", { name: "Убрать из сравнения" }))).toBeVisible();
    if (await add.isVisible()) {
      await add.click();
    }
    await page.keyboard.press("Escape");
  }
  await expect(page.getByText(/В сравнении: [23] из 4/)).toBeVisible();
  await page.getByRole("link", { name: "Сравнить" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Сравнение объектов" })).toBeVisible();
  await expect(page.getByRole("rowheader", { name: "До МКАД (20 т), мин" })).toBeVisible({ timeout: 30_000 });
  await page.screenshot({ path: info.outputPath("flow-compare.png"), fullPage: true });

  // 6. Выгрузка выборки.
  await page.goto("/facilities?type=warehouse&sort=-area");
  await expect(table.getByRole("row").nth(1)).toBeVisible({ timeout: 30_000 });
  await page.getByRole("button", { name: "Выгрузить" }).click();
  await page.getByRole("menuitem", { name: /CSV/ }).click();
  const link = page.getByRole("link", { name: /^Скачать: / });
  await expect(link).toBeVisible({ timeout: 30_000 });
  const [download] = await Promise.all([page.waitForEvent("download"), link.click()]);
  expect(download.suggestedFilename()).toMatch(/^objects-.*\.csv$/);

  // 7. Сохранённый вид восстанавливает карту с фильтром; удаление вида.
  await page.goto("/map");
  await expect(page.getByText("Объекты — по фильтру реестра")).toHaveCount(0);
  await page.getByRole("button", { name: "Виды" }).click();
  await page.getByRole("button", { name: new RegExp(`^${view}`) }).click();
  await expect(page).toHaveURL(/ft=warehouse/);
  await expect(page.getByText("Объекты — по фильтру реестра")).toBeVisible();
  await page.getByRole("button", { name: "Виды" }).click();
  await page.getByRole("button", { name: `Удалить вид «${view}»` }).click();
  await expect(page.getByText("Вид удалён")).toBeVisible();
  expect(errors).toEqual([]);
});
