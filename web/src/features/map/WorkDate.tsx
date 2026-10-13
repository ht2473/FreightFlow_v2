import { Slider, SliderThumb, SliderTrack } from "react-aria-components";
import { date } from "@/shared/format";
import { Button } from "@/shared/ui";
import s from "./map.module.css";
import { m } from "./messages";

/** Диапазон шкалы: два года назад и полгода вперёд от сегодняшнего дня. */
const back = 730;
const ahead = 183;

/** Шкала времени слоя работ: день от сегодняшнего (0 — сегодня). */
export function WorkDate({ offset, onChange }: { offset: number; onChange: (days: number) => void }) {
  const day = new Date();
  day.setDate(day.getDate() + offset);
  return (
    <div className={`${s.float} ${s.workDate}`}>
      <Slider
        className={s.workDateSlider}
        aria-label={m.workDate}
        minValue={-back}
        maxValue={ahead}
        step={1}
        value={offset}
        onChange={(v) => onChange(Array.isArray(v) ? (v[0] ?? 0) : v)}
        formatOptions={{ style: "unit", unit: "day", signDisplay: "exceptZero" }}
      >
        <div className={s.workDateHead}>
          <span className={s.workDateLabel}>{m.workDate}</span>
          <b className="num">{date(day.toISOString())}</b>
        </div>
        <SliderTrack className={s.opacityTrack}>
          <SliderThumb className={s.opacityThumb} />
        </SliderTrack>
      </Slider>
      <Button size="sm" variant="ghost" isDisabled={offset === 0} onPress={() => onChange(0)}>
        {m.today}
      </Button>
    </div>
  );
}
