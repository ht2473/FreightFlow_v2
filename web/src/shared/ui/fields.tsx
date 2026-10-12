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
