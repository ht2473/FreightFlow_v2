import { useState } from "react";
import {
  Badge,
  Button,
  Checkbox,
  EmptyState,
  PageHeader,
  Section,
  Select,
  Skeleton,
  Switch,
  TextField,
} from "@/shared/ui";
import { ComboBox, NumberField, RadioGroup } from "@/shared/ui/fields";
import { KpiTile } from "@/shared/ui/KpiTile";
import { Breadcrumbs, Pagination, SegmentedControl, Tabs } from "@/shared/ui/navigation";
import { Drawer, Tooltip } from "@/shared/ui/overlays";
import { toast } from "@/shared/ui/toast";
import s from "./gallery.module.css";

const districts = [
  { id: "cao", label: "Центральный" },
  { id: "uvao", label: "Юго-Восточный" },
  { id: "yuao", label: "Южный" },
  { id: "vao", label: "Восточный" },
];

/**
 * Витрина компонентов дизайн-системы (только в режиме разработки).
 * Значения ниже — образцы оформления, а не данные системы.
 */
export function UiGallery() {
  const [mode, setMode] = useState("map");
  const [mass, setMass] = useState<number | null>(12);
  const [district, setDistrict] = useState<string | null>("uvao");
  const [profile, setProfile] = useState("heavy");
  const [offset, setOffset] = useState(0);
  const [drawer, setDrawer] = useState(false);

  return (
    <div className={s.page}>
      <PageHeader title="Компоненты" subtitle="Витрина дизайн-системы для разработки; значения — образцы оформления" />

      {/* Плитки — сами карточки, поэтому лежат на странице, а не в карточке раздела. */}
      <h2 className={s.heading}>Плитки показателей</h2>
      <div className={s.kpis}>
        <KpiTile
          label="Образец: рост — улучшение"
          value={48000}
          unit="м²"
          delta={{ value: 1200, period: "за месяц", better: "up" }}
          source="образец"
          method="Пояснение методики появляется по наведению и фокусу."
        />
        <KpiTile
          label="Образец: рост — ухудшение"
          value={37}
          delta={{ value: 5, period: "за неделю", better: "down" }}
          source="образец"
        />
        <KpiTile label="Образец: расчётный" value={3.46} fractionDigits={2} unit="мин" model source="образец" />
        <KpiTile label="Нет данных" value={null} source="образец" />
      </div>

      <Section title="Кнопки и метки">
        <div className={s.row}>
          <Button variant="primary">Основная</Button>
          <Button>Обычная</Button>
          <Button variant="ghost">Тихая</Button>
          <Button variant="danger">Опасная</Button>
          <Button size="sm">Малая</Button>
          <Button isDisabled>Недоступна</Button>
          <Tooltip content="Подсказка к действию">
            <Button>С подсказкой</Button>
          </Tooltip>
        </div>
        <div className={s.row}>
          <Badge tone="ok">Норма</Badge>
          <Badge tone="warn">Внимание</Badge>
          <Badge tone="crit">Критично</Badge>
          <Badge tone="info">Информация</Badge>
          <Badge>Нейтрально</Badge>
          <Badge tone="info" plain>
            модель
          </Badge>
        </div>
      </Section>

      <Section title="Поля">
        <div className={s.grid}>
          <TextField label="Текстовое поле" description="Подсказка под полем" placeholder="Введите текст" />
          <TextField label="С ошибкой" errorMessage="Поле обязательно" />
          <NumberField
            label="Масса ТС"
            unit="т"
            value={mass}
            onChange={setMass}
            minValue={0}
            maxValue={60}
            step={0.5}
          />
          <ComboBox
            label="Округ"
            options={districts}
            value={district}
            onChange={setDistrict}
            placeholder="Начните вводить"
          />
          <Select label="Список" options={districts} value={district} onChange={setDistrict} />
          <RadioGroup
            label="Профиль ТС"
            value={profile}
            onChange={setProfile}
            options={[
              { id: "light", label: "До 3,5 т" },
              { id: "heavy", label: "Грузовой", description: "Учитываются ограничения массы и габаритов" },
            ]}
          />
          <div className={s.stack}>
            <Switch defaultSelected>Переключатель</Switch>
            <Checkbox defaultSelected>Флажок</Checkbox>
          </div>
        </div>
      </Section>

      <Section title="Навигация">
        <div className={s.stack}>
          <Breadcrumbs
            items={[
              { title: "Москва", href: "/dashboard" },
              { title: "Объекты", href: "/facilities" },
              { title: "Карточка" },
            ]}
          />
          <SegmentedControl
            label="Вид"
            value={mode}
            onChange={setMode}
            options={[
              { id: "map", label: "Карта" },
              { id: "table", label: "Таблица" },
              { id: "split", label: "Вместе" },
            ]}
          />
          <Tabs
            label="Разделы карточки"
            items={[
              { id: "overview", title: "Обзор", content: <p>Содержимое вкладки «Обзор».</p> },
              { id: "history", title: "История", content: <p>Содержимое вкладки «История».</p> },
              { id: "links", title: "Связи", content: <p>Содержимое вкладки «Связи».</p> },
            ]}
          />
        </div>
      </Section>

      <Section title="Постраничность, загрузка, пусто" flush>
        <div className={s.pad}>
          <Skeleton height={14} width="60%" />
        </div>
        <EmptyState title="Ничего не найдено">Измените условия отбора.</EmptyState>
        <Pagination offset={offset} limit={12} total={48} onChange={setOffset} />
      </Section>

      <Section title="Плавающие элементы">
        <div className={s.row}>
          <Button onPress={() => setDrawer(true)}>Открыть ящик</Button>
          <Button onPress={() => toast.success("Сохранено")}>Уведомление</Button>
          <Button onPress={() => toast.error("Не удалось сохранить")}>Ошибка</Button>
        </div>
      </Section>

      <Drawer
        isOpen={drawer}
        onOpenChange={setDrawer}
        title="Карточка объекта"
        subtitle="Образец ящика справа"
        footer={
          <>
            <Button variant="ghost" onPress={() => setDrawer(false)}>
              Закрыть
            </Button>
            <Button variant="primary">Действие</Button>
          </>
        }
      >
        <Tabs
          label="Разделы"
          items={[
            { id: "o", title: "Обзор", content: <p>Обзор.</p> },
            { id: "h", title: "История", content: <p>История.</p> },
          ]}
        />
      </Drawer>
    </div>
  );
}
