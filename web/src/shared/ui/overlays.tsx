import { X } from "lucide-react";
import type { ReactElement, ReactNode } from "react";
import {
  Tooltip as AriaTooltip,
  Dialog,
  Heading,
  Modal,
  ModalOverlay,
  OverlayArrow,
  TooltipTrigger,
} from "react-aria-components";
import { IconButton } from "./index";
import s from "./overlays.module.css";

/**
 * Ящик справа поверх страницы (карточка объекта): один и тот же из таблицы и с карты.
 * Esc и щелчок вне ящика закрывают его.
 */
export function Drawer({
  isOpen,
  onOpenChange,
  title,
  subtitle,
  children,
  footer,
}: {
  isOpen: boolean;
  onOpenChange: (open: boolean) => void;
  title: string;
  subtitle?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
}) {
  return (
    <ModalOverlay isOpen={isOpen} onOpenChange={onOpenChange} isDismissable className={s.drawerOverlay}>
      <Modal className={s.drawer}>
        <Dialog className={s.drawerDialog}>
          <header className={s.drawerHeader}>
            <div className={s.drawerTitles}>
              <Heading slot="title" className={s.drawerTitle}>
                {title}
              </Heading>
              {subtitle && <div className={s.drawerSubtitle}>{subtitle}</div>}
            </div>
            <IconButton label="Закрыть" onPress={() => onOpenChange(false)}>
              <X size={18} />
            </IconButton>
          </header>
          <div className={s.drawerBody}>{children}</div>
          {footer && <footer className={s.drawerFooter}>{footer}</footer>}
        </Dialog>
      </Modal>
    </ModalOverlay>
  );
}

/** Подсказка при наведении и фокусе. Триггер — фокусируемый элемент React Aria. */
export function Tooltip({ content, children }: { content: ReactNode; children: ReactElement }) {
  return (
    <TooltipTrigger delay={500} closeDelay={100}>
      {children}
      <AriaTooltip className={s.tooltip} offset={8}>
        <OverlayArrow className={s.tooltipArrow}>
          <svg width={8} height={8} viewBox="0 0 8 8" aria-hidden>
            <path d="M0 0 L4 4 L8 0" />
          </svg>
        </OverlayArrow>
        {content}
      </AriaTooltip>
    </TooltipTrigger>
  );
}
