/** Шкала Саати: значение суждения «первый критерий важнее второго». */
export const saatyScale: { value: number; label: string }[] = [
  { value: 9, label: "9 — первый абсолютно важнее" },
  { value: 7, label: "7 — первый значительно важнее" },
  { value: 5, label: "5 — первый существенно важнее" },
  { value: 3, label: "3 — первый немного важнее" },
  { value: 1, label: "1 — равноценны" },
  { value: 1 / 3, label: "1/3 — второй немного важнее" },
  { value: 1 / 5, label: "1/5 — второй существенно важнее" },
  { value: 1 / 7, label: "1/7 — второй значительно важнее" },
  { value: 1 / 9, label: "1/9 — второй абсолютно важнее" },
];

/** Ключ суждения по паре критериев (i < j). */
export const pairKey = (a: string, b: string) => `${a}|${b}`;

/** Идентификатор значения шкалы для выпадающего списка. */
export function scaleId(v: number): string {
  return v >= 1 ? String(Math.round(v)) : `1/${Math.round(1 / v)}`;
}

export function scaleValue(id: string): number {
  return id.startsWith("1/") ? 1 / Number(id.slice(2)) : Number(id);
}

/**
 * Обратносимметричная матрица попарных сравнений: над диагональю — суждения (по умолчанию 1),
 * под диагональю — обратные значения, на диагонали — единицы.
 */
export function pairwiseMatrix(metrics: string[], judgments: Record<string, number>): number[][] {
  return metrics.map((a, i) =>
    metrics.map((b, j) => {
      if (i === j) {
        return 1;
      }
      if (i < j) {
        return judgments[pairKey(a, b)] ?? 1;
      }
      return 1 / (judgments[pairKey(b, a)] ?? 1);
    }),
  );
}

/** Пары критериев для суждений: (i, j) при i < j. */
export function pairs<T>(list: T[]): [T, T][] {
  const out: [T, T][] = [];
  for (let i = 0; i < list.length; i++) {
    for (let j = i + 1; j < list.length; j++) {
      const a = list[i];
      const b = list[j];
      if (a !== undefined && b !== undefined) {
        out.push([a, b]);
      }
    }
  }
  return out;
}
