import { expect, test } from "@playwright/test";
import { account, axeViolations, login } from "./support";

const analyst = account("FF_E2E");
const steward = account("FF_DEV_STEWARD");

test.skip(
  !analyst.email || !analyst.password || !steward.email || !steward.password,
  "нужны FF_E2E_* и FF_DEV_STEWARD_*",
);

test("неточность: сообщение аналитика, правка и закрытие редактором", async ({ page, browser }, info) => {
  test.setTimeout(120_000);
  const message = `Проверка e2e ${Date.now()}: название устарело`;

  // Аналитик сообщает о неточности из карточки.
  const a = page;
  await login(a, analyst, "/facilities?sort=name");
  const table = a.getByRole("table", { name: "Реестр объектов" });
  await expect(table.getByRole("row").nth(1)).toBeVisible({ timeout: 30_000 });
  await table.getByRole("row").nth(1).getByRole("button").first().click();
  const card = a.getByRole("dialog");
  await card.getByRole("button", { name: "Сообщить о неточности" }).click();
  const modal = a.getByRole("dialog", { name: "Сообщить о неточности" });
  await modal.getByLabel("Что неверно").fill(message);
  await modal.getByRole("button", { name: "Отправить" }).click();
  await expect(a.getByText("Сообщение передано редактору")).toBeVisible();
  await card.getByRole("tab", { name: "История" }).click();
  await expect(card.getByText(message)).toBeVisible();

  // Редактор (отдельный сеанс): очередь → объект → правка названия → отмена → закрытие сообщения.
  const stewardContext = await browser.newContext({ baseURL: info.project.use.baseURL, locale: "ru-RU" });
  const e = await stewardContext.newPage();
  await login(e, steward, "/facilities");
  await e.getByRole("link", { name: /Сообщения о неточностях \(\d+\)/ }).click();
  const row = e.getByRole("row").filter({ hasText: message });
  await expect(row).toBeVisible({ timeout: 30_000 });
  expect(await axeViolations(e)).toEqual([]);
  await e.screenshot({ path: info.outputPath("reports-queue.png") });
  await row.getByRole("link").click();
  const drawer = e.getByRole("dialog");
  await drawer.getByRole("button", { name: "Уточнить" }).click();
  const edit = e.getByRole("dialog", { name: "Уточнить запись" });
  await edit.getByRole("textbox", { name: "Название" }).fill("Склад (уточнено e2e)");
  await edit.getByRole("textbox", { name: "Причина" }).fill("проверка уточнения");
  await edit.getByRole("button", { name: "Сохранить" }).click();
  await expect(drawer.getByText("Уточнено редактором: название")).toBeVisible({ timeout: 15_000 });
  await expect(drawer.getByRole("heading", { name: "Склад (уточнено e2e)" })).toBeVisible();
  await drawer.getByRole("tab", { name: "История" }).click();
  await expect(drawer.getByText("проверка уточнения", { exact: false })).toBeVisible();
  await e.screenshot({ path: info.outputPath("curation-history.png") });
  await drawer.getByRole("button", { name: "Отменить" }).first().click();
  await expect(e.getByText("Правка отменена")).toBeVisible();
  await expect(drawer.getByText("Уточнено редактором")).toHaveCount(0);
  await e.keyboard.press("Escape");

  await e.goto("/facilities/reports");
  const open = e.getByRole("row").filter({ hasText: message });
  await open.getByRole("button", { name: "Исправлено" }).click();
  await e.getByRole("dialog").getByRole("button", { name: "Закрыть сообщение" }).click();
  await expect(e.getByText("Сообщение закрыто")).toBeVisible();
  await expect(e.getByRole("row").filter({ hasText: message })).toHaveCount(0);
  await stewardContext.close();
});
