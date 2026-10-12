import createFetchClient from "openapi-fetch";
import createClient from "openapi-react-query";
import type { components, paths } from "./schema";

export type Schemas = components["schemas"];
export type Problem = Schemas["Problem"];

/** Перенаправляет на страницу входа с возвратом на текущий адрес. */
export function redirectToLogin() {
  const next = window.location.pathname + window.location.search;
  window.location.assign(`/login?next=${encodeURIComponent(next)}`);
}

export const fetchClient = createFetchClient<paths>({
  baseUrl: "/api/v1",
  credentials: "same-origin",
});

fetchClient.use({
  onResponse({ response }) {
    if (response.status === 401) {
      redirectToLogin();
    }
    return response;
  },
});

/** Хуки TanStack Query для операций API. */
export const $api = createClient(fetchClient);

/** Ошибка API в формате RFC 9457. */
export function isProblem(e: unknown): e is Problem {
  return typeof e === "object" && e !== null && "title" in e && "status" in e;
}

/** Текст ошибки для пользователя. */
export function problemMessage(e: unknown): string {
  if (isProblem(e)) {
    return e.detail ?? e.title;
  }
  return "Не удалось выполнить запрос. Проверьте соединение.";
}

/** Ошибки полей формы из ответа API. */
export function fieldErrors(e: unknown): Record<string, string> {
  if (!isProblem(e) || !e.errors) {
    return {};
  }
  return Object.fromEntries(e.errors.map((fe) => [fe.field, fe.message]));
}
