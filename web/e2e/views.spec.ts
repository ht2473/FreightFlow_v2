import { expect, test } from "@playwright/test";
import { account, axeViolations, login } from "./support";

const analyst = account("FF_E2E");

test.skip(!analyst.email || !analyst.password, "нужны FF_E2E_EMAIL и FF_E2E_PASSWORD");

test("сохранённый вид реестра: сохранить, применить, удалить", async ({ page }) => {
  test.setTimeout(60_000);
  const name = `Склады e2e ${Date.now()}`;
  await login(page, analyst, "/facilities?type=warehouse&sort=-area");
  await expect(page.getByRole("table", { name: "Реестр объектов" }).getByRole("row").nth(1)).toBeVisible({
    timeout: 30_000,
  });

  await page.getByRole("button", { name: "Виды" }).click();
  await page.getByRole("button", { name: "Сохранить текущий вид" }).click();
  const dlg = page.getByRole("dialog", { name: "Сохранить вид" });
  await dlg.getByRole("textbox", { name: "Название" }).fill(name);
  await dlg.getByRole("button", { name: "Сохранить" }).click();
  await expect(page.getByText("Вид сохранён")).toBeVisible();

  await page.goto("/facilities");
  await expect(page).not.toHaveURL(/type=/);
  await page.getByRole("button", { name: "Виды" }).click();
  expect(await axeViolations(page)).toEqual([]);
  await page.getByRole("button", { name: new RegExp(`^${name}`) }).click();
  await expect(page).toHaveURL(/type=warehouse/);
  await expect(page).toHaveURL(/sort=-area/);

  await page.getByRole("button", { name: "Виды" }).click();
  await page.getByRole("button", { name: `Удалить вид «${name}»` }).click();
  await expect(page.getByText("Вид удалён")).toBeVisible();
});
