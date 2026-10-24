import AxeBuilder from "@axe-core/playwright";
import type { Cookie, Page } from "@playwright/test";

export type Account = { email: string; password: string };

/** Учётная запись из окружения (`.env` подключает Taskfile). */
export function account(prefix: string): Account {
  return { email: process.env[`${prefix}_EMAIL`] ?? "", password: process.env[`${prefix}_PASSWORD`] ?? "" };
}

// Сессии по учётным записям в пределах процесса тестов: вход ограничен по частоте, поэтому
// форма заполняется один раз, дальше подставляются куки сессии.
const sessions = new Map<string, Cookie[]>();

/** Вход и переход на next; fresh — всегда через форму (проверка самого входа). */
export async function login(page: Page, acc: Account, next = "/", opts: { fresh?: boolean } = {}) {
  const saved = opts.fresh ? undefined : sessions.get(acc.email);
  if (saved) {
    await page.context().addCookies(saved);
    // Перенаправление на вход делает SPA после загрузки — живость сессии проверяется запросом.
    if ((await page.request.get("/api/v1/me")).ok()) {
      await page.goto(next);
      return;
    }
    // Сессию отозвали (например, тест выхода) — входим заново.
    sessions.delete(acc.email);
    await page.context().clearCookies();
  }
  await page.goto(`/login?next=${encodeURIComponent(next)}`);
  await page.getByLabel("Электронная почта").fill(acc.email);
  await page.getByLabel("Пароль", { exact: true }).fill(acc.password);
  await page.getByRole("button", { name: "Войти" }).click();
  await page.waitForURL((u) => !u.pathname.startsWith("/login"));
  sessions.set(acc.email, await page.context().cookies());
}

/** Серьёзные нарушения доступности на странице. */
export async function axeViolations(page: Page) {
  const res = await new AxeBuilder({ page }).analyze();
  return res.violations.filter((v) => v.impact === "serious" || v.impact === "critical").map((v) => v.id);
}
