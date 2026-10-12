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
