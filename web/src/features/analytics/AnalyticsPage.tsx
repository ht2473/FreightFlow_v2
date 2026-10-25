import { Link, useNavigate, useSearch } from "@tanstack/react-router";
import { BookOpen } from "lucide-react";
import { z } from "zod";
import { Guard } from "@/app/Guard";
import { PageHeader, Section } from "@/shared/ui";
import { Tabs } from "@/shared/ui/navigation";
import { IndexPanel } from "./IndexPanel";
import { MetricsPanel } from "./MetricsPanel";
import type { Level } from "./metrics";
import { SeriesPanel } from "./SeriesPanel";
import { SpatialPanel } from "./SpatialPanel";

/** Состояние раздела в адресе: вкладка, уровень и показатель, индекс и его уровень, ряд. */
export const analyticsSearchSchema = z.object({
  tab: z.enum(["metrics", "indexes", "series", "spatial"]).optional().catch(undefined),
  lv: z.enum(["district", "okrug", "h3_8", "h3_9", "facility", "segment"]).optional().catch(undefined),
  m: z.string().max(80).optional().catch(undefined),
  ix: z.string().max(80).optional().catch(undefined),
  il: z.enum(["district", "h3_8"]).optional().catch(undefined),
  sm: z.string().max(80).optional().catch(undefined),
});

type AnalyticsSearch = z.infer<typeof analyticsSearchSchema>;

export function AnalyticsPage() {
  return (
    <Guard permission="analytics.view">
      <Analytics />
    </Guard>
  );
}

function Analytics() {
  const search = useSearch({ from: "/analytics" });
  const navigate = useNavigate({ from: "/analytics" });
  const set = (patch: Partial<AnalyticsSearch>) =>
    navigate({ search: (prev: AnalyticsSearch) => ({ ...prev, ...patch }), replace: true });
  return (
    <>
      <PageHeader
        title="Аналитика"
        subtitle="Показатели по территориям и ячейкам H3, сводные индексы, ряды с прогнозами, пространственный анализ"
        actions={
          <Link to="/analytics/methods" className="btn btn-sm">
            <BookOpen size={14} aria-hidden /> Методики
          </Link>
        }
      />
      <Section>
        <Tabs
          label="Разделы аналитики"
          selected={search.tab ?? "metrics"}
          onChange={(tab) => set({ tab: tab as AnalyticsSearch["tab"] })}
          items={[
            {
              id: "metrics",
              title: "Показатели",
              content: (
                <MetricsPanel
                  level={(search.lv ?? "district") as Level}
                  metric={search.m}
                  onChange={(lv, m) => set({ lv: lv as AnalyticsSearch["lv"], m })}
                />
              ),
            },
            {
              id: "indexes",
              title: "Индексы",
              content: (
                <IndexPanel code={search.ix} level={search.il ?? "district"} onChange={(ix, il) => set({ ix, il })} />
              ),
            },
            {
              id: "series",
              title: "Ряды и прогнозы",
              content: <SeriesPanel metric={search.sm} onChange={(sm) => set({ sm })} />,
            },
            { id: "spatial", title: "Пространственный анализ", content: <SpatialPanel /> },
          ]}
        />
      </Section>
    </>
  );
}
