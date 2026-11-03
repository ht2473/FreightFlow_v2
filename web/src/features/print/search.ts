/** Параметры шаблона — строки адреса страницы печати. */
export function printSearchSchema(search: Record<string, unknown>): Record<string, string | undefined> {
  const out: Record<string, string | undefined> = {};
  for (const [k, v] of Object.entries(search)) {
    out[k] = v === undefined || v === null ? undefined : String(v);
  }
  return out;
}
