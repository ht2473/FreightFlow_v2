import AxeBuilder from "@axe-core/playwright";
import type { Page } from "@playwright/test";

export type Account = { email: string; password: string };

/** Учётная запись из окружения (`.env` подключает Taskfile). */
export function account(prefix: string): Account {
  return { email: process.env[`${prefix}_EMAIL`] ?? "", password: process.env[`${prefix}_PASSWORD`] ?? "" };
}

export async function login(page: Page, acc: Account, next = "/") {
  await page.goto(`/login?next=${encodeURIComponent(next)}`);
  await page.getByLabel("Электронная почта").fill(acc.email);
  await page.getByLabel("Пароль", { exact: true }).fill(acc.password);
  await page.getByRole("button", { name: "Войти" }).click();
}

/** Серьёзные нарушения доступности на странице. */
export async function axeViolations(page: Page) {
  const res = await new AxeBuilder({ page }).analyze();
  return res.violations.filter((v) => v.impact === "serious" || v.impact === "critical").map((v) => v.id);
}
