import { Link } from "@tanstack/react-router";
import { CircleHelp } from "lucide-react";
import { Dialog, DialogTrigger, Popover } from "react-aria-components";
import { dateTime } from "@/shared/format";
import { IconButton, popoverClass } from "@/shared/ui";
import s from "./analytics.module.css";
import type { MetricDef } from "./metrics";

/** Текст методики: `код` — моноширинным, остальное — как есть. */
export function MethodText({ text }: { text: string }) {
  return (
    <>
      {text.split(/(`[^`]+`)/).map((part, i) =>
        part.startsWith("`") ? (
          // biome-ignore lint/suspicious/noArrayIndexKey: части одного неизменного текста
          <code key={i}>{part.slice(1, -1)}</code>
        ) : (
          part
        ),
      )}
    </>
  );
}

/** Подсказка «?» у показателя: формула, методика, время расчёта, ссылка на страницу методик. */
export function MetricHelp({ def }: { def: MetricDef | undefined }) {
  if (!def) {
    return null;
  }
  return (
    <DialogTrigger>
      <IconButton label={`Методика: ${def.title}`}>
        <CircleHelp size={14} />
      </IconButton>
      <Popover className={popoverClass} placement="bottom start">
        <Dialog className={s.help} aria-label={`Методика: ${def.title}`}>
          <h3>{def.title}</h3>
          <p className={s.formula}>{def.formula}</p>
          <p>
            <MethodText text={def.method} />
          </p>
          <p className={s.note}>
            {def.computedAt ? `Рассчитано ${dateTime(def.computedAt)}` : "Ещё не рассчитано"} · версия {def.version}
          </p>
          <Link to="/analytics/methods" hash={def.code}>
            Все методики
          </Link>
        </Dialog>
      </Popover>
    </DialogTrigger>
  );
}
