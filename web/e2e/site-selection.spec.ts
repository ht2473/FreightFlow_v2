import { expect, test } from "@playwright/test";
import { account, axeViolations, login } from "./support";

const analyst = account("FF_E2E");

test.skip(!analyst.email || !analyst.password, "нужны FF_E2E_EMAIL и FF_E2E_PASSWORD");

// Нужны показатели H3 оценки локации (доступность ячеек, население, доли зон).
test("оценка локации: критерии, TOPSIS, МАИ, сравнение и сохранение", async ({ page }, info) => {
  test.setTimeout(120_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page, analyst, "/site-selection");
  await expect(page.getByRole("heading", { level: 1, name: "Оценка локации" })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByRole("checkbox", { name: /Время до МКАД/ })).toBeChecked();

  await page.getByRole("button", { name: "Оценить" }).click();
  const candidates = page.locator("section").filter({ has: page.getByRole("heading", { name: "Кандидаты" }) });
  await expect(candidates.getByRole("listitem").first()).toBeVisible({ timeout: 30_000 });
  expect(await candidates.getByRole("listitem").count()).toBeGreaterThan(5);
  await expect(page.getByText(/Оценено [\d\s ]+ ячеек из/)).toBeVisible();
  await expect(page.getByRole("heading", { name: "Сравнение кандидатов" })).toBeVisible();
  await page.waitForTimeout(1500);
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.waitForTimeout(800);
    expect(await axeViolations(page)).toEqual([]);
    await page.screenshot({ path: info.outputPath(`site-${scheme}.png`) });
  }
  await page.emulateMedia({ colorScheme: "light" });

  // Попарное сравнение: время до МКАД значительно важнее населения — суждения согласованы.
  await page.getByRole("radio", { name: "Попарное сравнение (МАИ)" }).click();
  const pair = page.getByRole("button", { name: /Время до МКАД — Население в 5 км/ });
  await pair.click();
  await page.getByRole("option", { name: "7 — первый значительно важнее" }).click();
  await page.getByRole("button", { name: "Оценить" }).click();
  await expect(page.getByText(/Согласованность суждений: CR =/)).toBeVisible({ timeout: 30_000 });
  await page.screenshot({ path: info.outputPath("site-ahp.png") });

  // Сохранение и удаление.
  const name = `Проверка оценки ${Date.now()}`;
  await page.getByLabel("Название оценки").fill(name);
  await page.getByRole("button", { name: "Сохранить" }).click();
  await expect(page.getByText("Оценка сохранена")).toBeVisible();
  await page.getByRole("button", { name: "Удалить сохранённую" }).click();
  await expect(page.getByText("Оценка удалена")).toBeVisible();
  expect(errors).toEqual([]);
});
