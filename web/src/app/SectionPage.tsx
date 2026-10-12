import { Hourglass } from "lucide-react";
import { EmptyState, PageHeader } from "@/shared/ui";
import { Guard } from "./Guard";
import s from "./pages.module.css";
import type { Section } from "./sections";

/** Раздел без данных: назначение и состав, без имитации содержимого. */
export function SectionPage({ section }: { section: Section }) {
  return (
    <Guard permission={section.permission}>
      <PageHeader title={section.title} subtitle={section.summary} />
      <EmptyState icon={<Hourglass size={20} strokeWidth={1.7} />} title="Данные ещё не загружены">
        <p>Раздел заполнится после подключения источников и первых расчётов.</p>
        <ul className={s.points}>
          {section.points.map((p) => (
            <li key={p}>{p}</li>
          ))}
        </ul>
      </EmptyState>
    </Guard>
  );
}
