import { useState } from "react";
import { num } from "@/shared/format";
import { Button } from "@/shared/ui";

/** Длинный список: первые 30 строк, кнопка — показать все (без прокручиваемой области). */
export function useRows<T>(rows: T[], first = 30) {
  const [all, setAll] = useState(false);
  const shown = all ? rows : rows.slice(0, first);
  const more =
    rows.length > first ? (
      <Button size="sm" variant="ghost" onPress={() => setAll(!all)}>
        {all ? "Свернуть" : `Показать все ${num(rows.length)}`}
      </Button>
    ) : null;
  return { shown, more };
}
