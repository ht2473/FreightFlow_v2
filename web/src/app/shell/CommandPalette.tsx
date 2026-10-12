import { useNavigate } from "@tanstack/react-router";
import { Search, User } from "lucide-react";
import { useEffect, useState } from "react";
import {
  Autocomplete,
  Dialog,
  Header,
  Input,
  Menu,
  MenuItem,
  MenuSection,
  Modal,
  ModalOverlay,
  SearchField,
  useFilter,
} from "react-aria-components";
import { type NavItem, navGroups } from "@/app/nav";
import { can, type Me } from "@/shared/auth";
import s from "./palette.module.css";

interface Command {
  id: string;
  title: string;
  icon: NavItem["icon"];
}

/** Палитра команд (Ctrl K): быстрый переход по разделам. */
export function CommandPalette({
  me,
  isOpen,
  onOpenChange,
}: {
  me: Me;
  isOpen: boolean;
  onOpenChange: (v: boolean) => void;
}) {
  const navigate = useNavigate();
  const { contains } = useFilter({ sensitivity: "base" });

  const groups = [
    ...navGroups.map((g) => ({
      title: g.title,
      items: g.items
        .filter((it) => can(me, it.permission))
        .map<Command>((it) => ({ id: it.to, title: it.title, icon: it.icon })),
    })),
    { title: "Личное", items: [{ id: "/profile", title: "Профиль", icon: User }] },
  ].filter((g) => g.items.length > 0);

  return (
    <ModalOverlay isOpen={isOpen} onOpenChange={onOpenChange} isDismissable className={s.overlay}>
      <Modal className={s.modal}>
        <Dialog className={s.dialog} aria-label="Палитра команд">
          <Autocomplete filter={contains}>
            <SearchField aria-label="Команда или раздел" className={s.field} autoFocus>
              <Search size={16} aria-hidden />
              <Input placeholder="Раздел или команда" className={s.input} />
            </SearchField>
            <Menu
              className={s.menu}
              aria-label="Результаты"
              onAction={(key) => {
                onOpenChange(false);
                void navigate({ to: String(key) });
              }}
              renderEmptyState={() => <div className={s.empty}>Ничего не найдено</div>}
            >
              {groups.map((g) => (
                <MenuSection key={g.title} className={s.section}>
                  <Header className={s.header}>{g.title}</Header>
                  {g.items.map((c) => (
                    <MenuItem key={c.id} id={c.id} textValue={c.title} className={s.item}>
                      <c.icon size={16} strokeWidth={1.7} aria-hidden />
                      {c.title}
                    </MenuItem>
                  ))}
                </MenuSection>
              ))}
            </Menu>
          </Autocomplete>
        </Dialog>
      </Modal>
    </ModalOverlay>
  );
}

/** Открытие палитры по Ctrl K / ⌘K. */
export function useCommandPalette() {
  const [isOpen, setOpen] = useState(false);
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setOpen((v) => !v);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, []);
  return { isOpen, setOpen };
}
