import { createHmac } from "node:crypto";
import { createServer, type IncomingHttpHeaders } from "node:http";
import type { AddressInfo } from "node:net";
import { expect, test } from "@playwright/test";
import { account, axeViolations, login } from "./support";

const analyst = account("FF_E2E");

test.skip(!analyst.email || !analyst.password, "нужны FF_E2E_EMAIL и FF_E2E_PASSWORD");

const defaults = {
  emailMode: "instant",
  digestHour: 9,
  minSeverity: "info",
  subscribeAll: false,
  subscribeSeverity: "critical",
};

test("профиль: настройки уведомлений, вебхук с подписью и журналом", async ({ page }, info) => {
  test.setTimeout(120_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  // Приёмник вебхука на стенде: запоминает тела и заголовки.
  const received: { headers: IncomingHttpHeaders; body: string }[] = [];
  const server = createServer((req, res) => {
    let body = "";
    req.on("data", (c) => {
      body += c;
    });
    req.on("end", () => {
      received.push({ headers: req.headers, body });
      res.end("ok");
    });
  });
  await new Promise<void>((r) => server.listen(0, "127.0.0.1", r));
  const port = (server.address() as AddressInfo).port;
  try {
    await login(page, analyst, "/profile");
    await expect(page.getByRole("heading", { name: "Уведомления", exact: true })).toBeVisible();
    await page.getByRole("button", { name: /Письма об алертах/ }).click();
    await page.getByRole("option", { name: "Раз в сутки дайджестом" }).click();
    await page.getByRole("button", { name: /Час дайджеста/ }).click();
    await page.getByRole("option", { name: "08:00" }).click();
    await page.locator("label").filter({ hasText: "Тихие часы" }).click();
    await page.getByRole("button", { name: "Сохранить" }).last().click();
    await expect(page.getByText("Настройки уведомлений сохранены")).toBeVisible();
    await page.reload();
    await expect(page.getByRole("button", { name: /Письма об алертах/ })).toContainText("Раз в сутки дайджестом");
    await expect(page.getByRole("switch", { name: /Тихие часы/ })).toBeChecked();
    await expect(page.getByText(/Бот не настроен на сервере|Привязать Telegram|Привязан/).first()).toBeVisible();

    await page.getByRole("button", { name: "Добавить вебхук" }).click();
    const dlg = page.getByRole("dialog", { name: "Новый вебхук" });
    await dlg.getByRole("textbox", { name: "Название" }).fill("Приёмник e2e");
    await dlg.getByRole("textbox", { name: "Адрес" }).fill(`http://127.0.0.1:${port}/hook`);
    await dlg.getByRole("button", { name: "Добавить" }).click();
    const secretDlg = page.getByRole("dialog", { name: "Секрет подписи" });
    const secret = (await secretDlg.locator("code").innerText()).trim();
    expect(secret).toMatch(/^whsec_/);
    for (const scheme of ["light", "dark"] as const) {
      await page.emulateMedia({ colorScheme: scheme });
      await page.waitForTimeout(500);
      expect(await axeViolations(page)).toEqual([]);
    }
    await page.emulateMedia({ colorScheme: "light" });
    await secretDlg.getByRole("button", { name: "Готово" }).click();

    const row = page.getByRole("row").filter({ hasText: "Приёмник e2e" });
    await row.getByRole("button", { name: "Проверить" }).click();
    await expect(page.getByText("Проверка доставлена (200)")).toBeVisible({ timeout: 20_000 });
    // Подпись: HMAC-SHA256 секрета от «метка времени.тело».
    expect(received).toHaveLength(1);
    const r = received[0];
    if (!r) {
      throw new Error("вебхук не получен");
    }
    const ts = String(r.headers["x-freightflow-timestamp"]);
    const want = `sha256=${createHmac("sha256", secret).update(`${ts}.${r.body}`).digest("hex")}`;
    expect(r.headers["x-freightflow-signature"]).toBe(want);
    expect(r.headers["x-freightflow-event"]).toBe("ping");

    await row.getByRole("button", { name: "Журнал" }).click();
    const log = page.getByRole("dialog", { name: "Журнал: Приёмник e2e" });
    await expect(log.getByRole("cell", { name: "ping" })).toBeVisible();
    await expect(log.getByText("доставлено")).toBeVisible();
    await page.screenshot({ path: info.outputPath("webhook-log.png") });
    await page.keyboard.press("Escape");
    await row.getByRole("button", { name: "Удалить вебхук «Приёмник e2e»" }).click();
    await expect(row).toHaveCount(0);
    await page.screenshot({ path: info.outputPath("profile-notifications.png"), fullPage: true });
  } finally {
    server.close();
    await page.request.put("/api/v1/me/notification-settings", { data: defaults });
  }
  expect(errors).toEqual([]);
});

test("колокольчик: уведомление о новом алерте без перезагрузки, переход к алерту", async ({ page }, info) => {
  test.setTimeout(120_000);
  const errors: string[] = [];
  page.on("pageerror", (e) => errors.push(e.message));
  await login(page, analyst, "/dashboard");
  await page.request.post("/api/v1/me/notifications/read", { data: { all: true } });
  await page.reload();
  const bell = page.getByRole("button", { name: /^Уведомления/ });
  await expect(bell).toHaveAccessibleName("Уведомления");
  const created = await page.request.post("/api/v1/alert-rules", {
    data: {
      name: `e2e: колокольчик ${Date.now()}`,
      kind: "sources",
      params: { staleFactor: 1.5 },
      scope: { type: "none" },
      channels: ["in_app"],
      enabled: true,
    },
  });
  expect(created.status()).toBe(201);
  const rule = (await created.json()) as { id: string };
  try {
    const check = await page.request.post(`/api/v1/alert-rules/${rule.id}/check`);
    expect(check.status()).toBe(200);
    // alert.created → уведомления → notification.created по потоку SSE.
    await expect(bell).toHaveAccessibleName(/^Уведомления, непрочитанных: \d+/, { timeout: 20_000 });
    await bell.click();
    const panel = page.getByRole("dialog", { name: "Уведомления" });
    const item = panel.getByRole("button", { name: /Данные устарели:/ }).first();
    await expect(item).toBeVisible();
    expect(await axeViolations(page)).toEqual([]);
    await page.screenshot({ path: info.outputPath("bell.png") });
    await item.click();
    await expect(page).toHaveURL(/\/alerts\?alert=/);
    await expect(page.getByRole("heading", { level: 2, name: /^Данные устарели:/ })).toBeVisible();
    await bell.click();
    await panel.getByRole("button", { name: "Прочитать все" }).click();
    await expect(bell).toHaveAccessibleName("Уведомления");
  } finally {
    await page.request.delete(`/api/v1/alert-rules/${rule.id}`);
  }
  expect(errors).toEqual([]);
});
