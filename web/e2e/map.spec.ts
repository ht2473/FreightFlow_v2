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

  // Выбор объекта из списка открывает карточку.
  await page.getByRole("tab", { name: "Объекты" }).click();
  await objects.getByRole("button").first().click();
  const card = page.getByRole("dialog");
  await expect(card).toBeVisible();
  await expect(card.getByText("Объекты логистики")).toBeVisible();
  await expect(card.getByText("© участники OpenStreetMap")).toBeVisible();
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
