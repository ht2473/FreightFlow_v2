import { expect, test } from "@playwright/test";
import { account, axeViolations, login } from "./support";

const analyst = account("FF_E2E");

test.skip(!analyst.email || !analyst.password, "нужны FF_E2E_EMAIL и FF_E2E_PASSWORD");

test("правило по области: рисование на карте, сохранение, проверка", async ({ page }, info) => {
  test.setTimeout(120_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page, analyst, "/alerts/rules/new");
  const name = `e2e: ограничения ${Date.now()}`;
  await page.getByRole("textbox", { name: "Название" }).fill(name);
  await page.getByRole("button", { name: /Что отслеживать/ }).click();
  await page.getByRole("option", { name: "Ограничения" }).click();
  await page.getByRole("radio", { name: "Область" }).click();
  await expect(page.getByRole("radio", { name: "Область" })).toBeChecked();
  const canvas = page.locator(".maplibregl-canvas");
  await expect(canvas).toBeVisible({ timeout: 30_000 });
  const box = await canvas.boundingBox();
  if (!box) {
    throw new Error("нет карты");
  }
  const cx = box.x + box.width / 2;
  const cy = box.y + box.height / 2;
  await page.getByRole("button", { name: "Нарисовать область" }).click();
  for (const [dx = 0, dy = 0] of [
    [-120, -90],
    [120, -90],
    [120, 90],
    [-120, 90],
  ]) {
    await page.mouse.click(cx + dx, cy + dy);
    await page.waitForTimeout(200);
  }
  await page.mouse.click(cx - 120, cy - 90);
  await expect(page.getByText(/^Контур из 4 точек/)).toBeVisible();
  await page.locator("label").filter({ hasText: "ограничение высоты" }).click();
  await expect(page.getByRole("checkbox", { name: "ограничение высоты" })).toBeChecked();
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.waitForTimeout(800);
    expect(await axeViolations(page)).toEqual([]);
    await page.screenshot({ path: info.outputPath(`rule-editor-${scheme}.png`) });
  }
  await page.emulateMedia({ colorScheme: "light" });
  await page.getByRole("button", { name: "Сохранить" }).click();
  await page.waitForURL(/\/alerts\?tab=rules/);
  const row = page.getByRole("row").filter({ hasText: name });
  await expect(row).toBeVisible();
  await expect(row.getByText("Ограничения", { exact: true })).toBeVisible();
  await row.getByRole("button", { name: `Проверить «${name}» сейчас` }).click();
  await expect(page.getByText("Проверено: новых срабатываний нет")).toBeVisible({ timeout: 30_000 });

  // Правка: сохранённая область на карте, вид менять нельзя.
  await row.getByRole("link", { name: `Изменить «${name}»` }).click();
  await expect(page.getByRole("textbox", { name: "Название" })).toHaveValue(name);
  await expect(page.getByRole("button", { name: /Что отслеживать/ })).toBeDisabled();
  await page.getByRole("link", { name: "К правилам" }).click();

  await row.getByRole("button", { name: `Удалить «${name}»` }).click();
  await page.getByRole("dialog").getByRole("button", { name: "Удалить" }).click();
  await expect(row).toHaveCount(0);
  expect(errors).toEqual([]);
});

test("лента: алерт появляется без перезагрузки, подтверждение, комментарий, закрытие", async ({ page }, info) => {
  test.setTimeout(120_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page, analyst, "/alerts");
  // Правило по источникам с порогом устаревания 1,5 интервала — на стенде разработки
  // устаревшие наборы есть всегда; первая проверка пойдёт по запросу, пока лента открыта.
  const name = `e2e: источники ${Date.now()}`;
  const created = await page.request.post("/api/v1/alert-rules", {
    data: { name, kind: "sources", params: { staleFactor: 1.5 }, scope: { type: "none" }, enabled: true },
  });
  expect(created.status()).toBe(201);
  const rule = (await created.json()) as { id: string };
  try {
    await page.getByRole("checkbox", { name: "Назначенные мне" }).waitFor();
    const feed = page.getByRole("list", { name: "Алерты" });
    const before = await feed
      .getByRole("listitem")
      .count()
      .catch(() => 0);
    const check = await page.request.post(`/api/v1/alert-rules/${rule.id}/check`);
    expect(check.status()).toBe(200);
    // Срабатывания может найти и фоновая проверка, поставленная при создании правила, — важно,
    // что алерт появляется в ленте без перезагрузки.
    // Событие alert.created по потоку SSE обновляет ленту без перезагрузки.
    const card = page
      .getByRole("button", { name: /^Данные устарели:/ })
      .filter({ hasText: name })
      .first();
    await expect(card).toBeVisible({ timeout: 15_000 });
    expect(await feed.getByRole("listitem").count()).toBeGreaterThan(before);

    await card.click();
    await expect(page).toHaveURL(/alert=/);
    await expect(page.getByRole("heading", { level: 2, name: /^Данные устарели:/ })).toBeVisible();
    await expect(page.getByText(/^Последняя успешная загрузка/).last()).toBeVisible();
    await page.getByRole("textbox", { name: "Комментарий" }).fill("Проверяю расписание загрузки");
    await page.getByRole("button", { name: "Подтвердить" }).click();
    await expect(page.getByText("Статус: подтверждён")).toBeVisible();
    const log = page.getByRole("list").filter({ has: page.getByText("Создан", { exact: true }) });
    await expect(log.getByText("Проверяю расписание загрузки")).toBeVisible();
    for (const scheme of ["light", "dark"] as const) {
      await page.emulateMedia({ colorScheme: scheme });
      await page.waitForTimeout(800);
      expect(await axeViolations(page)).toEqual([]);
      await page.screenshot({ path: info.outputPath(`alerts-feed-${scheme}.png`), fullPage: true });
    }
    await page.emulateMedia({ colorScheme: "light" });
    await page.getByRole("button", { name: "Закрыть", exact: true }).click();
    await expect(page.getByText("Статус: закрыт")).toBeVisible();
    await expect(log.getByText("Закрыт", { exact: true })).toBeVisible();
  } finally {
    await page.request.delete(`/api/v1/alert-rules/${rule.id}`);
  }
  expect(errors).toEqual([]);
});
