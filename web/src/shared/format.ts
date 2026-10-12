import { format, formatDistanceToNowStrict } from "date-fns";
import { ru } from "date-fns/locale";

/** Дата и время: «12.10.2026, 14:05». */
export function dateTime(iso: string | undefined | null): string {
  return iso ? format(new Date(iso), "dd.MM.yyyy, HH:mm") : "—";
}

/** Дата: «12.10.2026». */
export function date(iso: string | undefined | null): string {
  return iso ? format(new Date(iso), "dd.MM.yyyy") : "—";
}

/** Относительное время: «5 минут назад». */
export function ago(iso: string | undefined | null): string {
  return iso ? formatDistanceToNowStrict(new Date(iso), { locale: ru, addSuffix: true }) : "—";
}

const numberFormats = new Map<number, Intl.NumberFormat>();

/** Число по-русски: пробел между разрядами, запятая в дробной части («48 000», «3,5»). */
export function num(n: number | null | undefined, fractionDigits = 0): string {
  if (n === null || n === undefined || Number.isNaN(n)) {
    return "—";
  }
  let f = numberFormats.get(fractionDigits);
  if (!f) {
    f = new Intl.NumberFormat("ru-RU", { maximumFractionDigits: fractionDigits });
    numberFormats.set(fractionDigits, f);
  }
  return f.format(n);
}
