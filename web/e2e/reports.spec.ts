import { writeFile } from "node:fs/promises";
import { expect, type Page, test } from "@playwright/test";
import { account, axeViolations, login } from "./support";

const analyst = account("FF_E2E");

test.skip(!analyst.email || !analyst.password, "нужны FF_E2E_EMAIL и FF_E2E_PASSWORD");

async function firstId(
  page: Page,
  path: string,
  pick: (body: unknown) => string | undefined,
): Promise<string | undefined> {
  const r = await page.request.get(path);
  return r.ok() ? pick(await r.json()) : undefined;
}

/** Параметры шаблонов на данных стенда; шаблон без данных пропускается. */
async function templateParams(page: Page): Promise<Record<string, Record<string, string> | undefined>> {
  const facility = await firstId(
    page,
    "/api/v1/facilities?sort=-area&limit=1",
    (b) => (b as { items: { id: string }[] }).items[0]?.id,
  );
  const district = await firstId(
    page,
    "/api/v1/territories?kind=district",
    (b) => (b as { id: string; name: string }[]).find((t) => t.name.startsWith("Пресненский"))?.id,
  );
  const scenario = await firstId(
    page,
    "/api/v1/scenarios",
    (b) => (b as { id: string; status: string }[]).find((s) => s.status === "done")?.id,
  );
  const site = await firstId(page, "/api/v1/site-selection/runs", (b) => (b as { id: string }[])[0]?.id);
  return {
    facility: facility ? { facilityId: facility } : undefined,
    territory: district ? { territoryId: district } : undefined,
    passability: district ? { territoryId: district, profile: "truck_20" } : undefined,
    roadworks: { days: "30" },
    scenario: scenario ? { scenarioId: scenario } : undefined,
    site: site ? { siteRunId: site } : undefined,
    quality: {},
  };
}

test("страницы печати всех шаблонов: данные, карты, признак готовности", async ({ page }, info) => {
  test.setTimeout(240_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page, analyst, "/dashboard");
  const params = await templateParams(page);
  for (const [template, p] of Object.entries(params)) {
    if (!p) {
      continue;
    }
    await page.goto(`/print/${template}?${new URLSearchParams(p).toString()}`);
    await expect(page.locator("body"), template).toHaveAttribute("data-ready", "true", { timeout: 60_000 });
    await expect(page.getByRole("heading", { level: 1 })).toBeVisible();
    // Меню рабочего пространства на странице печати нет.
    await expect(page.getByRole("navigation", { name: "Разделы" })).toHaveCount(0);
    const report = await page.evaluate(() => window.__ffReport);
    expect(report?.sheets.length, template).toBeGreaterThan(0);
    await page.screenshot({ path: info.outputPath(`print-${template}.png`), fullPage: true });
  }
  expect(errors).toEqual([]);
});

test("отчёты всех шаблонов формируются в PDF и XLSX и скачиваются", async ({ page }, info) => {
  test.setTimeout(600_000);
  await login(page, analyst, "/dashboard");
  const params = await templateParams(page);
  const created: { id: string; template: string; format: string }[] = [];
  for (const [template, p] of Object.entries(params)) {
    if (!p) {
      continue;
    }
    for (const format of template === "facility" || template === "territory" ? ["pdf", "xlsx"] : ["pdf"]) {
      const r = await page.request.post("/api/v1/reports", { data: { template, format, params: p } });
      expect(r.status(), `${template} ${format}: ${await r.text()}`).toBe(202);
      created.push({ ...((await r.json()) as { id: string }), template, format });
    }
  }
  expect(created.length).toBeGreaterThanOrEqual(6);
  for (const rep of created) {
    await expect
      .poll(
        async () => {
          const r = await page.request.get(`/api/v1/reports/${rep.id}`);
          const body = (await r.json()) as { status: string; error?: string };
          if (body.status === "failed") {
            throw new Error(`${rep.template} ${rep.format}: ${body.error}`);
          }
          return body.status;
        },
        { timeout: 240_000, intervals: [2_000] },
      )
      .toBe("done");
    const file = await page.request.get(`/api/v1/reports/${rep.id}/file`);
    expect(file.status()).toBe(200);
    const body = await file.body();
    if (rep.format === "pdf") {
      expect(body.subarray(0, 5).toString()).toBe("%PDF-");
      expect(body.length).toBeGreaterThan(20_000);
    } else {
      expect(body.subarray(0, 2).toString()).toBe("PK");
    }
    await writeFile(info.outputPath(`${rep.template}.${rep.format}`), body);
  }
});

test("раздел отчётов: заказ из профиля района, готовность, скачивание, расписание", async ({ page }, info) => {
  test.setTimeout(180_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page, analyst, "/territories?kind=district");
  await page
    .getByRole("table", { name: "Районы" })
    .getByRole("link", { name: /^Пресненский/ })
    .click();
  await page.getByRole("link", { name: "Сводка PDF" }).click();
  const dlg = page.getByRole("dialog", { name: "Новый отчёт" });
  await expect(dlg.getByRole("button", { name: /Шаблон/ })).toContainText("Сводка по округу или району");
  await expect(dlg.getByRole("button", { name: /Территория/ })).toContainText("Пресненский");
  await dlg.getByRole("button", { name: "Сформировать" }).click();
  await expect(page.getByText(/Отчёт поставлен в очередь/)).toBeVisible();
  const row = page.getByRole("row").filter({ hasText: "Сводка: Пресненский район" }).filter({ hasText: "PDF" }).first();
  await expect(row.getByText("Готов")).toBeVisible({ timeout: 120_000 });
  const [file] = await Promise.all([
    page.waitForEvent("download"),
    row.getByRole("link", { name: /^Скачать/ }).click(),
  ]);
  expect(file.suggestedFilename()).toMatch(/^report-territory-.+\.pdf$/);
  for (const scheme of ["light", "dark"] as const) {
    await page.emulateMedia({ colorScheme: scheme });
    await page.waitForTimeout(500);
    expect(await axeViolations(page)).toEqual([]);
    await page.screenshot({ path: info.outputPath(`reports-${scheme}.png`), fullPage: true });
  }
  await page.emulateMedia({ colorScheme: "light" });

  // Расписание: еженедельно, получатель; удаление.
  await page.getByRole("tab", { name: "Расписания" }).click();
  await page.getByRole("button", { name: "Новое расписание" }).click();
  const sd = page.getByRole("dialog", { name: "Новое расписание" });
  await sd.getByRole("button", { name: /Шаблон/ }).click();
  await page.getByRole("option", { name: "Качество данных" }).click();
  await sd.getByRole("textbox", { name: /Получатели письма/ }).fill("ops@example.com");
  await sd.getByRole("button", { name: "Сохранить" }).click();
  const rows = page.getByRole("row").filter({ hasText: "Качество данных" }).filter({ hasText: "ops@example.com" });
  const sched = rows.first();
  await expect(sched.getByText("ops@example.com")).toBeVisible();
  await expect(sched.getByText(/по дням: понедельник, 08:00/)).toBeVisible();
  // Удаляются и созданное сейчас, и оставшееся от прерванного прогона.
  while ((await rows.count()) > 0) {
    const n = await rows.count();
    await rows.first().getByRole("button", { name: /^Удалить расписание/ }).click();
    await expect(rows).toHaveCount(n - 1);
  }
  expect(errors).toEqual([]);
});
