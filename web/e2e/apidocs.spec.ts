import { expect, test } from "@playwright/test";
import { account, axeViolations, login } from "./support";

const analyst = account("FF_E2E");

test.skip(!analyst.email || !analyst.password, "нужны FF_E2E_EMAIL и FF_E2E_PASSWORD");

test("документация API: начало работы, вебхуки, поиск и описание операции", async ({ page }, info) => {
  test.setTimeout(60_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page, analyst, "/dashboard");
  await page.getByRole("navigation", { name: "Разделы" }).getByRole("link", { name: "API" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "API" })).toBeVisible();
  await expect(page.getByRole("heading", { name: "Вебхуки" })).toBeVisible();
  await expect(page.getByText(/Версия .+; операций: \d+/)).toBeVisible({ timeout: 15_000 });
  const spec = await page.request.get("/api/v1/openapi.json");
  expect(spec.status()).toBe(200);
  expect(((await spec.json()) as { openapi: string }).openapi).toMatch(/^3\./);

  await page.getByRole("textbox", { name: "Найти операцию" }).fill("правило");
  const op = page.locator("details").filter({ hasText: "/alert-rules/{ruleId}/check" });
  await expect(op).toBeVisible();
  await expect(page.locator("details").filter({ hasText: "/facilities/points" })).toHaveCount(0);
  await op.locator("summary").click();
  await expect(op.getByText("Право:")).toBeVisible();
  await expect(op.getByText(/curl -X POST/)).toBeVisible();
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.waitForTimeout(400);
    expect(await axeViolations(page)).toEqual([]);
    await page.screenshot({ path: info.outputPath(`api-docs-${scheme}.png`), fullPage: true });
  }
  expect(errors).toEqual([]);
});
