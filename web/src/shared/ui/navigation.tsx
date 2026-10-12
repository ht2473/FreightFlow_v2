import { ChevronLeft, ChevronRight } from "lucide-react";
import type { Key, ReactNode } from "react";
import {
  Breadcrumbs as AriaBreadcrumbs,
  Tabs as AriaTabs,
  Breadcrumb,
  Link,
  Tab,
  TabList,
  TabPanel,
  ToggleButton,
  ToggleButtonGroup,
} from "react-aria-components";
import { num } from "@/shared/format";
import { IconButton } from "./index";
import s from "./navigation.module.css";

export interface TabItem {
  id: string;
  title: ReactNode;
  content: ReactNode;
}

/** Вкладки: подчёркивание акцентом у активной. */
export function Tabs({
  label,
  items,
  selected,
  onChange,
}: {
  label: string;
  items: TabItem[];
  selected?: string;
  onChange?: (id: string) => void;
}) {
  return (
    <AriaTabs
      className={s.tabs}
      selectedKey={selected}
      onSelectionChange={onChange ? (k: Key) => onChange(String(k)) : undefined}
    >
      <TabList aria-label={label} className={s.tabList}>
        {items.map((it) => (
          <Tab key={it.id} id={it.id} className={s.tab}>
            {it.title}
          </Tab>
        ))}
      </TabList>
      {items.map((it) => (
        <TabPanel key={it.id} id={it.id} className={s.tabPanel}>
          {it.content}
        </TabPanel>
      ))}
    </AriaTabs>
  );
}

/** Сегментированный переключатель: выбор одного из нескольких режимов. */
export function SegmentedControl({
  label,
  options,
  value,
  onChange,
}: {
  label: string;
  options: { id: string; label: ReactNode }[];
  value: string;
  onChange: (id: string) => void;
}) {
  return (
    <ToggleButtonGroup
      aria-label={label}
      className={s.segmented}
      selectionMode="single"
      disallowEmptySelection
      selectedKeys={[value]}
      onSelectionChange={(keys) => {
        const k = [...keys][0];
        if (k !== undefined) {
          onChange(String(k));
        }
      }}
    >
      {options.map((o) => (
        <ToggleButton key={o.id} id={o.id} className={s.segment}>
          {o.label}
        </ToggleButton>
      ))}
    </ToggleButtonGroup>
  );
}

/** Хлебные крошки; последний элемент — текущая страница. */
export function Breadcrumbs({ items }: { items: { title: string; href?: string }[] }) {
  return (
    <AriaBreadcrumbs className={s.crumbs}>
      {items.map((it) => (
        <Breadcrumb key={it.title} className={s.crumb}>
          {it.href ? <Link href={it.href}>{it.title}</Link> : <span>{it.title}</span>}
        </Breadcrumb>
      ))}
    </AriaBreadcrumbs>
  );
}

/** Постраничность по смещению: «1–12 из 48» и переходы. */
export function Pagination({
  offset,
  limit,
  total,
  onChange,
}: {
  offset: number;
  limit: number;
  total: number;
  onChange: (offset: number) => void;
}) {
  const from = total === 0 ? 0 : offset + 1;
  const to = Math.min(offset + limit, total);
  return (
    <nav className={s.pagination} aria-label="Страницы">
      <span className={s.range}>
        {num(from)}–{num(to)} из {num(total)}
      </span>
      <IconButton
        label="Предыдущая страница"
        isDisabled={offset === 0}
        onPress={() => onChange(Math.max(0, offset - limit))}
      >
        <ChevronLeft size={16} />
      </IconButton>
      <IconButton label="Следующая страница" isDisabled={to >= total} onPress={() => onChange(offset + limit)}>
        <ChevronRight size={16} />
      </IconButton>
    </nav>
  );
}
