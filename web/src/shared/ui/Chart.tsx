import { BarChart, LineChart } from "echarts/charts";
import { GridComponent, LegendComponent, TooltipComponent } from "echarts/components";
import * as echarts from "echarts/core";
import { CanvasRenderer } from "echarts/renderers";
import { FileSpreadsheet, ImageDown } from "lucide-react";
import { useEffect, useRef } from "react";
import { num } from "@/shared/format";
import { useResolvedTheme } from "@/shared/useResolvedTheme";
import s from "./Chart.module.css";
import { IconButton } from "./index";

echarts.use([LineChart, BarChart, GridComponent, LegendComponent, TooltipComponent, CanvasRenderer]);

/** Ряд графика: цвет — роль токена (acc, cat1…cat8), у нескольких рядов — ещё и форма маркера. */
export interface ChartSeries {
  name: string;
  kind: "line" | "bar";
  color: string;
  data: (number | null)[];
  stack?: string;
  /** Пунктир — модельные значения (прогноз). */
  dashed?: boolean;
}

/** Полоса интервала (например, прогноза): нижняя и верхняя граница по подписям оси. */
export interface ChartBand {
  name: string;
  lo: (number | null)[];
  hi: (number | null)[];
}

const markers = ["circle", "rect", "triangle", "diamond", "roundRect", "pin", "arrow", "emptyCircle"];

function token(name: string): string {
  return getComputedStyle(document.documentElement).getPropertyValue(`--${name}`).trim();
}

/** Сохранение файла из браузера. */
function download(name: string, href: string) {
  const a = document.createElement("a");
  a.href = href;
  a.download = name;
  a.click();
}

/** Данные графика в CSV (UTF-8 с BOM — Excel распознаёт кодировку; разделитель — точка с запятой). */
export function chartCsv(labels: string[], series: ChartSeries[], band?: ChartBand): string {
  const cols = [...series.map((x) => x.name), ...(band ? [`${band.name}: от`, `${band.name}: до`] : [])];
  const cell = (v: number | null | undefined) => (v === null || v === undefined ? "" : String(v).replace(".", ","));
  const quote = (v: string) => (/[;"\n]/.test(v) ? `"${v.replaceAll('"', '""')}"` : v);
  const rows = labels.map((l, i) =>
    [quote(l), ...series.map((x) => cell(x.data[i])), ...(band ? [cell(band.lo[i]), cell(band.hi[i])] : [])].join(";"),
  );
  return `﻿${[["Период", ...cols].map(quote).join(";"), ...rows].join("\r\n")}\r\n`;
}

/**
 * График по месяцам или категориям (DESIGN.md, раздел 8): сетка — line, подписи — tx2,
 * несколько рядов — палитра cat и форма маркера; прогноз — пунктир и полоса интервала.
 * Подпись для чтения с экрана — label; exportName — кнопки выгрузки PNG и CSV.
 */
export function Chart({
  labels,
  series,
  band,
  label,
  unit,
  height = 260,
  exportName,
  compact,
}: {
  labels: string[];
  series: ChartSeries[];
  band?: ChartBand;
  label: string;
  unit?: string;
  height?: number;
  exportName?: string;
  /** Без легенды (узкая карточка: пояснение — под графиком). */
  compact?: boolean;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const chartRef = useRef<echarts.ECharts | null>(null);
  const theme = useResolvedTheme();

  // Цвета берутся из токенов текущей темы: при смене темы график строится заново.
  // biome-ignore lint/correctness/useExhaustiveDependencies: theme меняет значения токенов
  useEffect(() => {
    const el = ref.current;
    if (!el) {
      return;
    }
    const chart = echarts.init(el, undefined, { renderer: "canvas" });
    chartRef.current = chart;
    const text = token("tx2");
    const line = token("line");
    const reduced = window.matchMedia("(prefers-reduced-motion: reduce)").matches;
    const fmt = (v: unknown) => (typeof v === "number" ? `${num(v)}${unit ? ` ${unit}` : ""}` : "—");
    // Полоса — два ряда в стопке: нижняя граница без заливки и ширина интервала с заливкой.
    const bandSeries = band
      ? [
          {
            name: `${band.name}: от`,
            type: "line",
            data: band.lo,
            stack: "band",
            symbol: "none",
            lineStyle: { opacity: 0 },
            tooltip: { show: false },
          },
          {
            name: band.name,
            type: "line",
            data: band.hi.map((h, i) => (h === null || band.lo[i] === null ? null : h - (band.lo[i] ?? 0))),
            stack: "band",
            symbol: "none",
            lineStyle: { opacity: 0 },
            areaStyle: { color: token("acc"), opacity: 0.15 },
            tooltip: { show: false },
          },
        ]
      : [];
    const legend = !compact && (series.length > 1 || band);
    chart.setOption({
      animation: !reduced,
      textStyle: { fontFamily: getComputedStyle(el).fontFamily, color: text },
      grid: { left: 8, right: 16, top: legend || exportName ? 40 : 12, bottom: 8, containLabel: true },
      legend: legend
        ? {
            top: 0,
            left: 0,
            right: exportName ? 72 : 0,
            textStyle: { color: text, fontSize: 12 },
            itemGap: 16,
            data: [...series.map((x) => x.name), ...(band ? [band.name] : [])],
          }
        : undefined,
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
      series: [
        ...bandSeries,
        ...series.map((x, i) => ({
          name: x.name,
          type: x.kind,
          data: x.data,
          stack: x.stack,
          symbol: series.length > 1 ? markers[i % markers.length] : "circle",
          symbolSize: 6,
          showSymbol: x.data.length <= 40,
          itemStyle: { color: token(x.color) },
          lineStyle: { width: 2, type: x.dashed ? "dashed" : "solid" },
          barMaxWidth: 18,
        })),
      ],
    });
    const ro = new ResizeObserver(() => chart.resize());
    ro.observe(el);
    return () => {
      ro.disconnect();
      chart.dispose();
      chartRef.current = null;
    };
  }, [labels, series, band, unit, theme, compact, exportName]);

  return (
    <div className={s.wrap}>
      {exportName && (
        <div className={s.tools}>
          <IconButton
            label="Скачать изображение графика"
            onPress={() => {
              const url = chartRef.current?.getDataURL({ type: "png", pixelRatio: 2, backgroundColor: token("panel") });
              if (url) {
                download(`${exportName}.png`, url);
              }
            }}
          >
            <ImageDown size={15} />
          </IconButton>
          <IconButton
            label="Скачать данные графика"
            onPress={() => {
              const blob = new Blob([chartCsv(labels, series, band)], { type: "text/csv;charset=utf-8" });
              const url = URL.createObjectURL(blob);
              download(`${exportName}.csv`, url);
              setTimeout(() => URL.revokeObjectURL(url), 1000);
            }}
          >
            <FileSpreadsheet size={15} />
          </IconButton>
        </div>
      )}
      <div ref={ref} role="img" aria-label={label} style={{ width: "100%", height }} />
    </div>
  );
}
