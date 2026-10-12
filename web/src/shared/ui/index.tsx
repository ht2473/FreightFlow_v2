import { Check, ChevronDown } from "lucide-react";
import type { ReactNode } from "react";
import {
  Button as AriaButton,
  type ButtonProps as AriaButtonProps,
  Checkbox as AriaCheckbox,
  Modal as AriaModal,
  Select as AriaSelect,
  Switch as AriaSwitch,
  TextField as AriaTextField,
  type TextFieldProps as AriaTextFieldProps,
  type CheckboxProps,
  Dialog,
  FieldError,
  Heading,
  Input,
  Label,
  ListBox,
  ListBoxItem,
  ModalOverlay,
  Popover,
  SelectValue,
  type SwitchProps,
  Text,
} from "react-aria-components";
import s from "./ui.module.css";

type Variant = "default" | "primary" | "ghost" | "danger";

export interface ButtonProps extends AriaButtonProps {
  variant?: Variant;
  size?: "md" | "sm";
  block?: boolean;
}

/** Кнопка дизайн-системы. Одна основная (primary) кнопка на экран. */
export function Button({ variant = "default", size = "md", block, className, ...props }: ButtonProps) {
  const cls = [
    "btn",
    variant !== "default" && `btn-${variant}`,
    size === "sm" && "btn-sm",
    block && "btn-block",
    typeof className === "string" ? className : undefined,
  ]
    .filter(Boolean)
    .join(" ");
  return <AriaButton {...props} className={cls} />;
}

/** Кнопка-значок; подпись обязательна для экранных читалок. */
export function IconButton({ label, children, ...props }: AriaButtonProps & { label: string; children: ReactNode }) {
  return (
    <AriaButton {...props} aria-label={label} className={s.iconButton}>
      {children}
    </AriaButton>
  );
}

export interface TextFieldProps extends AriaTextFieldProps {
  label: string;
  description?: string;
  errorMessage?: string;
  placeholder?: string;
}

/** Текстовое поле с подписью, подсказкой и ошибкой. */
export function TextField({ label, description, errorMessage, placeholder, ...props }: TextFieldProps) {
  return (
    <AriaTextField {...props} isInvalid={props.isInvalid ?? Boolean(errorMessage)} className="field">
      <Label className="label">{label}</Label>
      <Input className="input" placeholder={placeholder} />
      {description && !errorMessage && (
        <Text slot="description" className="field-hint">
          {description}
        </Text>
      )}
      <FieldError className="field-error">{errorMessage}</FieldError>
    </AriaTextField>
  );
}

export interface Option {
  id: string;
  label: string;
}

/** Выпадающий список. */
export function Select({
  label,
  options,
  value,
  onChange,
  isDisabled,
}: {
  label: string;
  options: Option[];
  value: string | null;
  onChange: (id: string) => void;
  isDisabled?: boolean;
}) {
  return (
    <AriaSelect
      className={s.select}
      selectedKey={value}
      onSelectionChange={(key) => key !== null && onChange(String(key))}
      isDisabled={isDisabled}
    >
      <Label className="label">{label}</Label>
      <AriaButton className={s.selectButton}>
        <SelectValue />
        <ChevronDown size={16} aria-hidden />
      </AriaButton>
      <Popover className={s.popover}>
        <ListBox className={s.listBox} items={options}>
          {(o) => (
            <ListBoxItem id={o.id} className={s.option}>
              {o.label}
            </ListBoxItem>
          )}
        </ListBox>
      </Popover>
    </AriaSelect>
  );
}

/** Переключатель. */
export function Switch({ children, ...props }: SwitchProps & { children: ReactNode }) {
  return (
    <AriaSwitch {...props} className={s.switch}>
      <span className={s.switchTrack} aria-hidden />
      {children}
    </AriaSwitch>
  );
}

/** Флажок. */
export function Checkbox({ children, ...props }: CheckboxProps & { children: ReactNode }) {
  return (
    <AriaCheckbox {...props} className={s.checkbox}>
      {({ isSelected }) => (
        <>
          <span className={s.checkboxBox} aria-hidden>
            {isSelected && <Check size={12} strokeWidth={3} />}
          </span>
          {children}
        </>
      )}
    </AriaCheckbox>
  );
}

type Tone = "ok" | "warn" | "crit" | "info" | "neutral";

/** Метка статуса: точка и слово (цвет не единственный носитель смысла). */
export function Badge({ tone = "neutral", plain, children }: { tone?: Tone; plain?: boolean; children: ReactNode }) {
  return <span className={`badge badge-${tone}${plain ? " badge-plain" : ""}`}>{children}</span>;
}

/** Модальное окно с заголовком и действиями. */
export function Modal({
  isOpen,
  onOpenChange,
  title,
  description,
  children,
  actions,
}: {
  isOpen: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  description?: string;
  children?: ReactNode;
  actions?: ReactNode;
}) {
  return (
    <ModalOverlay isOpen={isOpen} onOpenChange={onOpenChange} isDismissable className={s.overlay}>
      <AriaModal className={s.modal}>
        <Dialog className={s.dialog}>
          <Heading slot="title" className={s.dialogTitle}>
            {title}
          </Heading>
          {description && <p className="muted">{description}</p>}
          {children && <div className={s.dialogBody}>{children}</div>}
          {actions && <div className={s.dialogActions}>{actions}</div>}
        </Dialog>
      </AriaModal>
    </ModalOverlay>
  );
}

/** Заглушка загрузки на месте содержимого. */
export function Skeleton({ width = "100%", height = 14 }: { width?: number | string; height?: number }) {
  return <div className={s.skeleton} style={{ width, height }} aria-hidden />;
}

/** Пустое состояние: что здесь будет и что сделать. */
export function EmptyState({
  icon,
  title,
  children,
  action,
}: {
  icon?: ReactNode;
  title: string;
  children?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className={s.empty}>
      {icon && <div className={s.emptyIcon}>{icon}</div>}
      <div className={s.emptyTitle}>{title}</div>
      {children && <div>{children}</div>}
      {action && <div className={s.emptyAction}>{action}</div>}
    </div>
  );
}

/** Заголовок страницы с подписью и действиями. */
export function PageHeader({ title, subtitle, actions }: { title: string; subtitle?: string; actions?: ReactNode }) {
  return (
    <div className={s.pageHeader}>
      <div>
        <h1>{title}</h1>
        {subtitle && <p>{subtitle}</p>}
      </div>
      {actions && <div className={s.pageActions}>{actions}</div>}
    </div>
  );
}

/** Карточка-раздел страницы: заголовок, пояснение и действия справа. */
export function Section({
  title,
  description,
  actions,
  flush,
  children,
}: {
  title?: string;
  description?: ReactNode;
  actions?: ReactNode;
  /** Содержимое без внутренних отступов (таблицы). */
  flush?: boolean;
  children: ReactNode;
}) {
  return (
    <section className={`card ${s.sectionCard}${flush ? ` ${s.sectionFlush}` : ""}`}>
      {(title || actions) && (
        <div className={s.sectionHead}>
          <div>
            {title && <h2 className={s.sectionTitle}>{title}</h2>}
            {description && <p className={s.sectionDescription}>{description}</p>}
          </div>
          {actions && <div className={s.pageActions}>{actions}</div>}
        </div>
      )}
      {children}
    </section>
  );
}

export const tableClass = s.table;
/** Выравнивание ячеек таблицы: числа и действия — справа. */
export const cellClass = { center: s.cellCenter, right: s.cellRight };
export const menuItemClass = s.menuItem;
export const menuSeparatorClass = s.menuSeparator;
export const popoverClass = s.popover;
