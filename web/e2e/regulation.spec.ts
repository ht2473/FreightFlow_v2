import { expect, test } from "@playwright/test";
import { account, axeViolations, login } from "./support";

const analyst = account("FF_E2E");

test.skip(!analyst.email || !analyst.password, "нужны FF_E2E_EMAIL и FF_E2E_PASSWORD");

test("регулирование: зоны и правила с основаниями, грузовой каркас, документы", async ({ page }, info) => {
  test.setTimeout(60_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page, analyst, "/regulation");
  // Первый показ раздела в режиме разработки ждёт сборки модуля графиков.
  await expect(page.getByRole("heading", { level: 1, name: "Регулирование" })).toBeVisible({ timeout: 30_000 });

  const zones = page.locator("section", { has: page.getByRole("heading", { name: "Зоны и правила" }) });
  for (const zone of ["Садовое кольцо", "Третье транспортное кольцо", "МКАД"]) {
    await expect(zones.getByRole("heading", { level: 3, name: zone })).toBeVisible();
  }
  // У правила — действие, условия и правовое основание; неподтверждённые отмечены.
  await expect(zones.getByText("Ночью — пропуск для грузовиков более 3,5 т")).toBeVisible();
  await expect(
    zones.getByText("РММ более 3,5 т · 23:00–07:00 · ночной пропуск зоны «МКАД», «ТТК» или «СК»"),
  ).toBeVisible();
  await expect(zones.getByText(/Основание: 379-ПП, п\. 2, 2\.4/).first()).toBeVisible();
  await expect(zones.getByText("не подтверждено").first()).toBeVisible();
  await expect(page.getByText(/правил[а]? не подтвержден/)).toBeVisible();

  const docs = page.locator("section", {
    has: page.getByRole("heading", { name: "Правовые основания и официальные страницы" }),
  });
  await expect(docs.getByRole("link", { name: /Об ограничении движения грузового автотранспорта/ })).toBeVisible();

  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.waitForTimeout(800);
    expect(await axeViolations(page)).toEqual([]);
    await page.screenshot({ path: info.outputPath(`regulation-${scheme}.png`), fullPage: true });
  }
  await page.emulateMedia({ colorScheme: "light" });

  const signs = page.locator("section", { has: page.getByRole("heading", { name: "Знаки и ограничения" }) });
  await expect(signs.getByRole("cell", { name: /Движение грузовых автомобилей запрещено/ })).toBeVisible();
  await expect(signs.getByRole("cell", { name: "Знак есть, в OSM ограничения нет" }).first()).toBeVisible();
  const frame = page.locator("section", { has: page.getByRole("heading", { name: "Грузовой каркас" }) });
  const passes = page.locator("section", { has: page.getByRole("heading", { name: "Пропуска грузового транспорта" }) });
  const permits = page.locator("section", { has: page.getByRole("heading", { name: /Спецразрешения/ }) });
  for (const [name, section] of [
    ["signs", signs],
    ["frame", frame],
    ["passes", passes],
    ["permits", permits],
    ["documents", docs],
  ] as const) {
    await section.scrollIntoViewIfNeeded();
    await section.screenshot({ path: info.outputPath(`regulation-${name}.png`) });
  }

  // Грузовой каркас открывается на карте со своим слоем.
  if (await frame.getByText("Перечень улиц ещё не загружен").isVisible()) {
    return;
  }
  await frame.getByRole("link", { name: "На карте" }).click();
  await expect(page).toHaveURL(/\/map\?.*cargo_frame/);
  await page.getByRole("tab", { name: "Слои" }).click();
  await expect(page.getByRole("switch", { name: "Грузовой каркас" })).toBeChecked();
  await page.waitForTimeout(2500);
  await page.screenshot({ path: info.outputPath("cargo-frame-map.png") });
  expect(errors).toEqual([]);
});
