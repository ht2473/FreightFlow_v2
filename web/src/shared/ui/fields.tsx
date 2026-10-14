import { ChevronDown, Minus, Plus } from "lucide-react";
import type { Key } from "react";
import {
  Button as AriaButton,
  ComboBox as AriaComboBox,
  NumberField as AriaNumberField,
  RadioGroup as AriaRadioGroup,
  FieldError,
  Group,
  Input,
  Label,
  ListBox,
  ListBoxItem,
  Popover,
  Radio,
  Text,
} from "react-aria-components";
import s from "./fields.module.css";
import type { Option } from "./index";
import ui from "./ui.module.css";

/** Числовое поле с шагом и единицей измерения в формате ru-RU. */
export function NumberField({
  label,
  value,
  onChange,
  minValue,
  maxValue,
  step,
  unit,
  description,
  errorMessage,
  isDisabled,
}: {
  label: string;
  value: number | null;
  onChange: (v: number | null) => void;
  minValue?: number;
  maxValue?: number;
  step?: number;
  unit?: string;
  description?: string;
  errorMessage?: string;
  isDisabled?: boolean;
}) {
  return (
    <AriaNumberField
      className="field"
      value={value ?? Number.NaN}
      onChange={(v) => onChange(Number.isNaN(v) ? null : v)}
      minValue={minValue}
      maxValue={maxValue}
      step={step}
      isDisabled={isDisabled}
      isInvalid={Boolean(errorMessage)}
      formatOptions={{ maximumFractionDigits: 3 }}
    >
      <Label className="label">{label}</Label>
      <Group className={s.numberGroup}>
        <Input className={s.numberInput} />
        {unit && <span className={s.unit}>{unit}</span>}
        <AriaButton slot="decrement" className={s.stepper} aria-label="Уменьшить">
          <Minus size={14} />
        </AriaButton>
        <AriaButton slot="increment" className={s.stepper} aria-label="Увеличить">
          <Plus size={14} />
        </AriaButton>
      </Group>
      {description && !errorMessage && (
        <Text slot="description" className="field-hint">
          {description}
        </Text>
      )}
      <FieldError className="field-error">{errorMessage}</FieldError>
    </AriaNumberField>
  );
}

/** Поле с подсказками: ввод и выбор из списка. */
export function ComboBox({
  label,
  options,
  value,
  onChange,
  placeholder,
  isDisabled,
}: {
  label: string;
  options: Option[];
  value: string | null;
  onChange: (id: string | null) => void;
  placeholder?: string;
  isDisabled?: boolean;
}) {
  return (
    <AriaComboBox
      className="field"
      defaultItems={options}
      selectedKey={value}
      onSelectionChange={(k: Key | null) => onChange(k === null ? null : String(k))}
      isDisabled={isDisabled}
      menuTrigger="focus"
    >
      <Label className="label">{label}</Label>
      <div className={s.comboGroup}>
        <Input className={`input ${s.comboInput}`} placeholder={placeholder} />
        <AriaButton className={s.comboButton} aria-label="Показать варианты">
          <ChevronDown size={16} />
        </AriaButton>
      </div>
      <Popover className={ui.popover}>
        <ListBox className={ui.listBox} renderEmptyState={() => <div className={s.empty}>Ничего не найдено</div>}>
          {(o: Option) => (
            <ListBoxItem id={o.id} className={ui.option}>
              {o.label}
            </ListBoxItem>
          )}
        </ListBox>
      </Popover>
    </AriaComboBox>
  );
}

export interface Suggestion {
  id: string;
  label: string;
  description?: string;
}

/**
 * Поле с подсказками, которые приходят по введённому тексту (поиск на сервере). Выбор подсказки
 * передаётся в onSelect, текст поля очищается.
 */
export function SuggestField({
  label,
  placeholder,
  text,
  onTextChange,
  suggestions,
  pending,
  onSelect,
}: {
  label: string;
  placeholder?: string;
  text: string;
  onTextChange: (v: string) => void;
  suggestions: Suggestion[];
  /** Ответ ещё не получен — пустой список не считается «ничего не найдено». */
  pending?: boolean;
  onSelect: (id: string) => void;
}) {
  return (
    <AriaComboBox
      className="field"
      items={suggestions}
      inputValue={text}
      onInputChange={onTextChange}
      selectedKey={null}
      onSelectionChange={(k: Key | null) => {
        if (k !== null) {
          onSelect(String(k));
          onTextChange("");
        }
      }}
      menuTrigger="input"
      // Пустой список («ничего не найдено») — только при введённом тексте: после выбора поле
      // очищается, и список закрывается.
      allowsEmptyCollection={text.trim().length >= 3}
      allowsCustomValue
    >
      <Label className="label">{label}</Label>
      <Input className="input" placeholder={placeholder} />
      <Popover className={ui.popover}>
        <ListBox
          className={ui.listBox}
          renderEmptyState={() => <div className={s.empty}>{pending ? "Поиск…" : "Ничего не найдено"}</div>}
        >
          {(o: Suggestion) => (
            <ListBoxItem id={o.id} textValue={o.label} className={`${ui.option} ${s.suggestion}`}>
              <span>{o.label}</span>
              {o.description && <span className={s.suggestionNote}>{o.description}</span>}
            </ListBoxItem>
          )}
        </ListBox>
      </Popover>
    </AriaComboBox>
  );
}

/** Группа переключателей для выбора одного варианта из немногих видимых. */
export function RadioGroup({
  label,
  options,
  value,
  onChange,
  orientation = "vertical",
}: {
  label: string;
  options: (Option & { description?: string })[];
  value: string;
  onChange: (id: string) => void;
  orientation?: "vertical" | "horizontal";
}) {
  return (
    <AriaRadioGroup className={`field ${s.radioGroup}`} value={value} onChange={onChange} orientation={orientation}>
      <Label className="label">{label}</Label>
      <div className={orientation === "horizontal" ? s.radiosRow : s.radios}>
        {options.map((o) => (
          <Radio key={o.id} value={o.id} className={s.radio}>
            <span className={s.radioDot} aria-hidden />
            <span>
              {o.label}
              {o.description && <span className={s.radioHint}>{o.description}</span>}
            </span>
          </Radio>
        ))}
      </div>
    </AriaRadioGroup>
  );
}
