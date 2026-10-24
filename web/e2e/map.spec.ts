import { expect, test } from "@playwright/test";
import { account, axeViolations, login } from "./support";

const analyst = account("FF_E2E");

test.skip(!analyst.email || !analyst.password, "нужны FF_E2E_EMAIL и FF_E2E_PASSWORD");

// Центр Москвы на масштабе, где видны объекты и ограничения; данные — после `etl run osm`.
const view = "/map?c=37.68000,55.72000&z=12";

test("карта: подложка, объекты в видимой области, слои, карточка объекта", async ({ page }, info) => {
  test.setTimeout(60_000);
  const errors: string[] = [];
  page.on("console", (msg) => {
    if (msg.type() === "error") {
      errors.push(msg.text());
    }
  });
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page, analyst, view);
  await expect(page).toHaveURL(/\/map\?/);
  const canvas = page.locator(".maplibregl-canvas");
  await expect(canvas).toBeVisible();
  const box = await canvas.boundingBox();
  expect(box?.width ?? 0, `ошибки: ${errors.join("; ")}`).toBeGreaterThan(300);
  const objects = page.getByRole("tabpanel", { name: "Объекты" });
  // Список заполняется, когда карта дорисовала тайлы.
  await expect(objects.getByText(/В видимой области · [1-9]/)).toBeVisible({ timeout: 20_000 });

  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.waitForTimeout(1500);
    expect(await axeViolations(page)).toEqual([]);
    await page.screenshot({ path: info.outputPath(`map-${scheme}.png`) });
  }
  await page.emulateMedia({ colorScheme: "light" });

  // Слой включается, и это отражается в адресе.
  await page.getByRole("tab", { name: "Слои" }).click();
  const fuel = page.getByRole("switch", { name: "АЗС" });
  await expect(fuel).not.toBeChecked();
  await page.getByRole("tabpanel", { name: "Слои" }).getByText("АЗС", { exact: true }).click();
  await expect(fuel).toBeChecked();
  await expect(page).toHaveURL(/l=[^&]*fuel_stations/);
  await expect(page.getByRole("region", { name: "Легенда" }).getByText("АЗС")).toBeVisible();
  await page.screenshot({ path: info.outputPath("map-layers.png") });

  // Выбор объекта из списка открывает карточку реестра — ту же, что в разделе «Объекты».
  await page.getByRole("tab", { name: "Объекты" }).click();
  await objects.getByRole("button").first().click();
  const card = page.getByRole("dialog");
  await expect(card).toBeVisible();
  await expect(card.getByRole("tab", { name: "Обзор" })).toBeVisible();
  await expect(card.getByText("Площадь застройки", { exact: true })).toBeVisible();
  await card.getByRole("tab", { name: "Источники" }).click();
  await expect(card.getByText("OpenStreetMap", { exact: true })).toBeVisible();
  expect(await axeViolations(page)).toEqual([]);
  await page.screenshot({ path: info.outputPath("map-card.png") });
  await page.keyboard.press("Escape");
  await expect(card).toHaveCount(0);

  // Вид карты сохраняется в адресе после перезагрузки.
  await page.reload();
  await expect(page).toHaveURL(/fuel_stations/);
  await expect(page.getByRole("tab", { name: "Объекты" })).toBeVisible();
  expect(errors).toEqual([]);
});

test("карта: выделение области со сводкой и измерение", async ({ page }, info) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page, analyst, "/map?c=37.75000,55.73000&z=12");
  const canvas = page.locator(".maplibregl-canvas");
  await expect(canvas).toBeVisible({ timeout: 30_000 });
  await page.waitForTimeout(2000);
  const box = await canvas.boundingBox();
  if (!box) {
    throw new Error("нет карты");
  }
  const cx = box.x + box.width / 2;
  const cy = box.y + box.height / 2;

  await page.getByRole("button", { name: "Выделить область" }).click();
  for (const [dx = 0, dy = 0] of [
    [-200, -150],
    [200, -150],
    [200, 150],
    [-200, 150],
  ]) {
    await page.mouse.click(cx + dx, cy + dy);
    await page.waitForTimeout(200);
  }
  // Многоугольник закрывается щелчком в первую вершину.
  await page.mouse.click(cx - 200, cy - 150);
  const panel = page.getByRole("region", { name: "Сводка по области" });
  await expect(panel.getByText("Объектов реестра")).toBeVisible({ timeout: 30_000 });
  await expect(panel.getByText(/км²/)).toBeVisible();
  expect(await axeViolations(page)).toEqual([]);
  await page.screenshot({ path: info.outputPath("map-area.png") });
  await panel.getByRole("button", { name: "Закрыть сводку" }).click();

  await page.getByRole("button", { name: "Измерить расстояние" }).click();
  await page.mouse.click(cx - 150, cy);
  await page.waitForTimeout(200);
  await page.mouse.click(cx + 150, cy);
  await page.waitForTimeout(200);
  // Линия завершается повторным щелчком в последнюю точку.
  await page.mouse.click(cx + 150, cy);
  await expect(page.getByText(/^Расстояние: [\d, ]+ (м|км)/)).toBeVisible();
  expect(errors).toEqual([]);
});

test("карта: кластеры объектов на мелком масштабе", async ({ page }, info) => {
  test.setTimeout(60_000);
  await login(page, analyst, "/map?c=37.62000,55.75000&z=9");
  const objects = page.getByRole("tabpanel", { name: "Объекты" });
  await expect(objects.getByText(/В видимой области · [1-9]/)).toBeVisible({ timeout: 20_000 });
  await page.waitForTimeout(1000);
  await page.screenshot({ path: info.outputPath("map-clusters.png") });

  // Кластер находится наведением: подсказка с числом объектов; щелчок приближает карту.
  const box = await page.locator(".maplibregl-canvas").boundingBox();
  if (!box) {
    throw new Error("нет холста карты");
  }
  const cx = box.x + box.width / 2;
  const cy = box.y + box.height / 2;
  const tip = page.getByText(/^Объектов: \d/);
  let found: { x: number; y: number } | null = null;
  search: for (let r = 0; r < 300; r += 12) {
    for (let a = 0; a < 360; a += r === 0 ? 360 : Math.max(8, 600 / Math.max(r, 1))) {
      const x = cx + r * Math.cos((a * Math.PI) / 180);
      const y = cy + r * Math.sin((a * Math.PI) / 180);
      await page.mouse.move(x, y);
      if (await tip.isVisible()) {
        found = { x, y };
        break search;
      }
    }
  }
  expect(found, "кластер не найден").not.toBeNull();
  await page.mouse.click(found?.x ?? 0, found?.y ?? 0);
  await expect(page).toHaveURL(/z=(1[0-9]|9\.[1-9])/, { timeout: 5000 });
});

test("карта: изображение с легендой и источниками", async ({ page }, info) => {
  test.setTimeout(60_000);
  await login(page, analyst, view);
  const objects = page.getByRole("tabpanel", { name: "Объекты" });
  await expect(objects.getByText(/В видимой области · [1-9]/)).toBeVisible({ timeout: 20_000 });
  const [download] = await Promise.all([
    page.waitForEvent("download"),
    page.getByRole("button", { name: "Сохранить изображение карты" }).click(),
  ]);
  expect(download.suggestedFilename()).toMatch(/^map-\d{4}-\d{2}-\d{2}\.png$/);
  const file = info.outputPath("map-image.png");
  await download.saveAs(file);
  const size = await page.evaluate(
    async (b64) => {
      const img = new Image();
      img.src = `data:image/png;base64,${b64}`;
      await img.decode();
      return { w: img.naturalWidth, h: img.naturalHeight };
    },
    (await import("node:fs")).readFileSync(file).toString("base64"),
  );
  const canvas = await page.locator(".maplibregl-canvas").boundingBox();
  // Подпись с источниками добавляется под кадром карты.
  expect(size.w).toBeGreaterThanOrEqual(Math.floor(canvas?.width ?? 0));
  expect(size.h).toBeGreaterThan(Math.floor(canvas?.height ?? 0));
});
