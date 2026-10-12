import { ArrowDownRight, ArrowUpRight, Info } from "lucide-react";
import { Button as AriaButton } from "react-aria-components";
import { num } from "@/shared/format";
import { Badge } from "./index";
import s from "./KpiTile.module.css";
import { Tooltip } from "./overlays";

export interface KpiDelta {
  /** Изменение в единицах показателя за период. */
  value: number;
  /** Подпись периода: «за неделю». */
  period: string;
  /** Какое направление — улучшение. */
  better: "up" | "down";
}

/**
 * Плитка показателя: подпись, число, динамика (красная — ухудшение, зелёная — улучшение),
 * источник. Расчётные значения помечаются «модель».
 */
export function KpiTile({
  label,
  value,
  unit,
  fractionDigits = 0,
  delta,
  source,
  method,
  model,
}: {
  label: string;
  value: number | null;
  unit?: string;
  fractionDigits?: number;
  delta?: KpiDelta;
  source: string;
  method?: string;
  model?: boolean;
}) {
  const up = delta ? delta.value > 0 : false;
  const improved = delta ? (delta.better === "up" ? delta.value > 0 : delta.value < 0) : false;
  const tone = !delta || delta.value === 0 ? s.flat : improved ? s.better : s.worse;

  return (
    <div className={`card ${s.tile}`}>
      <div className={s.head}>
        <span className={s.label}>{label}</span>
        {model && (
          <Badge tone="info" plain>
            модель
          </Badge>
        )}
        {method && (
          <Tooltip content={method}>
            <AriaButton className={s.info} aria-label={`Методика: ${label}`}>
              <Info size={14} />
            </AriaButton>
          </Tooltip>
        )}
      </div>
      <div className={s.value}>
        {num(value, fractionDigits)}
        {unit && value !== null && <span className={s.unit}>{unit}</span>}
      </div>
      {delta && (
        <div className={`${s.delta} ${tone}`}>
          {delta.value !== 0 &&
            (up ? <ArrowUpRight size={14} aria-hidden /> : <ArrowDownRight size={14} aria-hidden />)}
          <span>
            {delta.value > 0 ? "+" : ""}
            {num(delta.value, fractionDigits)} {delta.period}
          </span>
          <span className="visually-hidden">{improved ? "(улучшение)" : delta.value === 0 ? "" : "(ухудшение)"}</span>
        </div>
      )}
      <div className={s.source}>Источник: {source}</div>
    </div>
  );
}
