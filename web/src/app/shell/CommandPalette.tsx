import { useNavigate } from "@tanstack/react-router";
import { ArrowRight, Clock, Search, User } from "lucide-react";
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
} from "react-aria-components";
import { type NavItem, navGroups } from "@/app/nav";
import { $api } from "@/shared/api/client";
import { can, type Me } from "@/shared/auth";
import { groupHits, type Hit, kindIcons, kindTitles, loadRecent, pushRecent, type Recent } from "@/shared/search";
import { useDebounced } from "@/shared/useDebounced";
import s from "./palette.module.css";
import { useOpenHit } from "./useOpenHit";

interface Command {
  id: string;
  title: string;
  icon: NavItem["icon"];
}

const hitKey = (h: Hit) => `hit:${h.kind}:${h.ref}`;
const normalize = (v: string) => v.toLowerCase().replaceAll("ё", "е");

/**
 * Палитра (Ctrl K): разделы и команды, подсказки поиска по группам (объекты, территории, улицы,
 * адреса, акты…), недавнее, переход ко всем результатам.
 */
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
  const openHit = useOpenHit();
  const [text, setText] = useState("");
  const q = useDebounced(text.trim(), 120);
  const [recent, setRecent] = useState<Recent[]>([]);
  useEffect(() => {
    if (isOpen) {
      setText("");
      setRecent(loadRecent());
    }
  }, [isOpen]);

  const searching = q.length >= 2 && can(me, "map.view");
  const suggest = $api.useQuery(
    "get",
    "/search/suggest",
    { params: { query: { q, limit: 10 } } },
    {
      enabled: isOpen && searching,
      staleTime: 30_000,
    },
  );
  // Отложенный запрос отстаёт от поля: при пустом поле — недавнее, а не прежние подсказки.
  const hits = searching && text.trim() !== "" ? (suggest.data ?? []) : [];

  const commands: Command[] = [
    ...navGroups.flatMap((g) =>
      g.items
        .filter((it) => can(me, it.permission))
        .map((it) => ({ id: `nav:${it.to}`, title: it.title, icon: it.icon })),
    ),
    { id: "nav:/profile", title: "Профиль", icon: User },
  ];
  const needle = normalize(text.trim());
  const matched = needle ? commands.filter((c) => normalize(c.title).includes(needle)).slice(0, 5) : commands;

  const close = () => onOpenChange(false);
  const onAction = (key: string) => {
    close();
    if (key === "all") {
      pushRecent({ q: text.trim() });
      void navigate({ to: "/search", search: { q: text.trim() } });
      return;
    }
    if (key.startsWith("nav:")) {
      void navigate({ to: key.slice(4) });
      return;
    }
    if (key.startsWith("q:")) {
      void navigate({ to: "/search", search: { q: key.slice(2) } });
      return;
    }
    const hit = [...hits, ...recent.flatMap((r) => (r.hit ? [r.hit] : []))].find((h) => hitKey(h) === key);
    if (hit) {
      pushRecent({ hit });
      void openHit(hit);
    }
  };

  return (
    <ModalOverlay isOpen={isOpen} onOpenChange={onOpenChange} isDismissable className={s.overlay}>
      <Modal className={s.modal}>
        <Dialog className={s.dialog} aria-label="Палитра команд">
          <Autocomplete inputValue={text} onInputChange={setText}>
            <SearchField aria-label="Команда или раздел" className={s.field} autoFocus>
              <Search size={16} aria-hidden />
              <Input placeholder="Объект, адрес, улица, район, акт или раздел" className={s.input} />
            </SearchField>
            <Menu
              className={s.menu}
              aria-label="Результаты"
              onAction={(key) => onAction(String(key))}
              renderEmptyState={() => (
                <div className={s.empty}>{suggest.isFetching ? "Поиск…" : "Ничего не найдено"}</div>
              )}
            >
              {!needle && recent.length > 0 && (
                <MenuSection className={s.section}>
                  <Header className={s.header}>Недавнее</Header>
                  {recent.map((r) =>
                    r.hit ? (
                      <MenuItem key={hitKey(r.hit)} id={hitKey(r.hit)} textValue={r.hit.title} className={s.item}>
                        <Clock size={16} strokeWidth={1.7} aria-hidden />
                        <span className={s.itemText}>{r.hit.title}</span>
                        <span className={s.itemNote}>{kindTitles[r.hit.kind]}</span>
                      </MenuItem>
                    ) : (
                      <MenuItem key={`q:${r.q}`} id={`q:${r.q}`} textValue={r.q} className={s.item}>
                        <Clock size={16} strokeWidth={1.7} aria-hidden />
                        <span className={s.itemText}>{r.q}</span>
                        <span className={s.itemNote}>Запрос</span>
                      </MenuItem>
                    ),
                  )}
                </MenuSection>
              )}
              {matched.length > 0 && (
                <MenuSection className={s.section}>
                  <Header className={s.header}>Разделы</Header>
                  {matched.map((c) => (
                    <MenuItem key={c.id} id={c.id} textValue={c.title} className={s.item}>
                      <c.icon size={16} strokeWidth={1.7} aria-hidden />
                      {c.title}
                    </MenuItem>
                  ))}
                </MenuSection>
              )}
              {groupHits(hits).map((g) => {
                const Icon = kindIcons[g.kind];
                return (
                  <MenuSection key={g.kind} className={s.section}>
                    <Header className={s.header}>{kindTitles[g.kind]}</Header>
                    {g.hits.map((h) => (
                      <MenuItem key={hitKey(h)} id={hitKey(h)} textValue={h.title} className={s.item}>
                        <Icon size={16} strokeWidth={1.7} aria-hidden />
                        <span className={s.itemText}>{h.title}</span>
                        {h.subtitle && <span className={s.itemNote}>{h.subtitle}</span>}
                      </MenuItem>
                    ))}
                  </MenuSection>
                );
              })}
              {searching && text.trim() !== "" && (
                <MenuSection className={s.section}>
                  <Header className={s.header}>Поиск</Header>
                  <MenuItem id="all" textValue={`Все результаты по «${text.trim()}»`} className={s.item}>
                    <ArrowRight size={16} strokeWidth={1.7} aria-hidden />
                    Все результаты по «{text.trim()}»
                  </MenuItem>
                </MenuSection>
              )}
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
