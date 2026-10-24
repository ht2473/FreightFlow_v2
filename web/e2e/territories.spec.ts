import { expect, test } from "@playwright/test";
import { account, axeViolations, login } from "./support";

const analyst = account("FF_E2E");

test.skip(!analyst.email || !analyst.password, "нужны FF_E2E_EMAIL и FF_E2E_PASSWORD");

test("территории: список с сортировкой, профиль округа и района", async ({ page }, info) => {
  test.setTimeout(90_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page, analyst, "/territories");
  await expect(page.getByRole("heading", { level: 1, name: "Территории" })).toBeVisible({ timeout: 30_000 });
  const districts = page.getByRole("table", { name: "Районы" });
  await expect(districts.getByRole("row")).toHaveCount(133, { timeout: 30_000 });

  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.waitForTimeout(400);
    expect(await axeViolations(page)).toEqual([]);
    await page.screenshot({ path: info.outputPath(`territories-${scheme}.png`) });
  }
  await page.emulateMedia({ colorScheme: "light" });

  // Сортировка по населению: первое нажатие — по убыванию, в адресе.
  const pop = districts.getByRole("columnheader", { name: /Население/ });
  await pop.getByRole("button").click();
  await expect(pop).toHaveAttribute("aria-sort", "descending");
  await expect(page).toHaveURL(/sort=-population/);
  const first = Number(
    (await districts.getByRole("row").nth(1).getByRole("cell").nth(3).innerText()).replace(/\D/g, ""),
  );
  const second = Number(
    (await districts.getByRole("row").nth(2).getByRole("cell").nth(3).innerText()).replace(/\D/g, ""),
  );
  expect(first).toBeGreaterThanOrEqual(second);

  // Округа и профиль округа: показатели, районы, карта.
  await page.getByRole("radio", { name: "Округа" }).click();
  const okrugs = page.getByRole("table", { name: "Округа" });
  await expect(okrugs.getByRole("row")).toHaveCount(13);
  await page.getByRole("textbox", { name: "Поиск по названию" }).fill("Юго-Восточный");
  await expect(okrugs.getByRole("row")).toHaveCount(2);
  await okrugs.getByRole("link").first().click();
  await expect(page.getByRole("heading", { level: 1, name: "Юго-Восточный административный округ" })).toBeVisible();
  await expect(page.getByText("Объектов реестра", { exact: true })).toBeVisible({ timeout: 30_000 });
  await expect(page.getByRole("table", { name: "Население по годам" })).toBeVisible();
  const children = page.getByRole("table", { name: "Районы округа" });
  await expect(children.getByRole("row").nth(1)).toBeVisible();
  await expect(page.getByRole("region", { name: "Карта территории" }).locator("canvas")).toBeVisible();
  await page.waitForTimeout(1500);
  expect(await axeViolations(page)).toEqual([]);
  await page.screenshot({ path: info.outputPath("territory-okrug.png"), fullPage: true });

  // Район из таблицы округа и переход к его объектам в реестре.
  await children.getByRole("link", { name: "Нижегородский" }).click();
  await expect(page.getByRole("heading", { level: 1, name: "Нижегородский" })).toBeVisible();
  await expect(page.getByText("Район · ЮВАО")).toBeVisible();
  await page.getByRole("link", { name: "Объекты в реестре" }).click();
  await expect(page).toHaveURL(/\/facilities\?district=/);
  await expect(page.getByRole("list", { name: "Активные фильтры" }).getByText("Район: 1")).toBeVisible();
  await expect(page.getByRole("table", { name: "Реестр объектов" }).getByRole("row").nth(1)).toBeVisible();
  expect(errors).toEqual([]);
});
