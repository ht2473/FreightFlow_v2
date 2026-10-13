import { BarChart, LineChart } from "echarts/charts";
import { GridComponent, LegendComponent, TooltipComponent } from "echarts/components";
import * as echarts from "echarts/core";
import { CanvasRenderer } from "echarts/renderers";
import { useEffect, useRef } from "react";
import { num } from "@/shared/format";
import { useResolvedTheme } from "@/shared/useResolvedTheme";

echarts.use([LineChart, BarChart, GridComponent, LegendComponent, TooltipComponent, CanvasRenderer]);

/** Ряд графика: цвет — роль токена (acc, cat1…cat8), у нескольких рядов — ещё и форма маркера. */
export interface ChartSeries {
  name: string;
  kind: "line" | "bar";
  color: string;
  data: (number | null)[];
  stack?: string;
}

const markers = ["circle", "rect", "triangle", "diamond", "roundRect", "pin", "arrow", "emptyCircle"];

function token(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(`--${name}`).trim();
}

/**
 * График по месяцам или категориям (DESIGN.md, раздел 8): сетка — line, подписи — tx2,
 * несколько рядов — палитра cat и форма маркера. Подпись для чтения с экрана — label.
 */
export function Chart({
  labels,
  series,
  label,
  unit,
  height = 260,
}: {
  labels: string[];
  series: ChartSeries[];
  label: string;
  unit?: string;
  height?: number;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const theme = useResolvedTheme();

  // Цвета берутся из токенов текущей темы: при смене темы график строится заново.
  // biome-ignore lint/correctness/useExhaustiveDependencies: theme меняет значения токенов
  useEffect(() => {
    const el = ref.current;
    if (!el) {
      return;
    }
    const chart = echarts.init(el, undefined, { renderer: "canvas" });
    const text = token("tx2");
    const line = token("line");
    const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    const fmt = (v: unknown) => (typeof v === "number" ? `${num(v)}${unit ? ` ${unit}` : ""}` : "—");
    chart.setOption({
      animation: !reduced,
      textStyle: { fontFamily: getComputedStyle(el).fontFamily, color: text },
      grid: { left: 8, right: 16, top: series.length > 1 ? 36 : 12, bottom: 8, containLabel: true },
      legend:
        series.length > 1 ? { top: 0, left: 0, textStyle: { color: text, fontSize: 12 }, itemGap: 16 } : undefined,
      tooltip: {
        trigger: "axis",
        valueFormatter: fmt,
        backgroundColor: token("panel"),
        borderColor: line,
        textStyle: { color: token("tx"), fontSize: 12 },
      },
      xAxis: {
        type: "category",
        data: labels,
        axisLine: { lineStyle: { color: line } },
        axisTick: { show: false },
        axisLabel: { color: text, fontSize: 11 },
      },
      yAxis: {
        type: "value",
        splitLine: { lineStyle: { color: line } },
        axisLabel: { color: text, fontSize: 11, formatter: (v: number) => num(v) },
      },
      series: series.map((s, i) => ({
        name: s.name,
        type: s.kind,
        data: s.data,
        stack: s.stack,
        symbol: series.length > 1 ? markers[i % markers.length] : "circle",
        symbolSize: 6,
        showSymbol: s.data.length <= 40,
        itemStyle: { color: token(s.color) },
        lineStyle: { width: 2 },
        barMaxWidth: 18,
      })),
    });
    const ro = new ResizeObserver(() => chart.resize());
    ro.observe(el);
    return () => {
      ro.disconnect();
      chart.dispose();
    };
  }, [labels, series, unit, theme]);

  return <div ref={ref} role="img" aria-label={label} style={{ width: "100%", height }} />;
}
