import { Link, Outlet, useNavigate, useRouterState } from "@tanstack/react-router";
import { Bell, BellOff, Check, LogOut, Search, User } from "lucide-react";
import { useEffect } from "react";
import {
  Button as AriaButton,
  Dialog,
  DialogTrigger,
  Menu,
  MenuItem,
  MenuTrigger,
  Popover,
  Separator,
} from "react-aria-components";
import { navGroups, titleFor } from "@/app/nav";
import { can, initials, useMe } from "@/shared/auth";
import { roleTitle } from "@/shared/names";
import type { Theme } from "@/shared/theme";
import { useThemePreference, useThemeSync } from "@/shared/themePreference";
import { IconButton, menuItemClass, menuSeparatorClass, popoverClass } from "@/shared/ui";
import { ToastRegion } from "@/shared/ui/toast";
import { CommandPalette, useCommandPalette } from "./CommandPalette";
import s from "./shell.module.css";

const themes: { id: Theme; title: string }[] = [
  { id: "light", title: "Светлая тема" },
  { id: "dark", title: "Тёмная тема" },
  { id: "system", title: "Как в системе" },
];

/** Оболочка рабочего пространства: меню разделов, верхняя строка, содержимое. */
export function AppShell() {
  const me = useMe();
  const pathname = useRouterState({ select: (st) => st.location.pathname });
  const title = titleFor(pathname);
  const palette = useCommandPalette();
  useThemeSync();

  useEffect(() => {
    document.title = `${title} — FreightFlow`;
  }, [title]);

  if (me.isPending) {
    return <div className={s.loading}>Загрузка…</div>;
  }
  if (me.isError || !me.data) {
    return <div className={s.loading}>Не удалось загрузить профиль. Обновите страницу.</div>;
  }
  const user = me.data;
  const role = user.roles[0] ? roleTitle(user.roles[0]) : "";
  // Разделы с картой во всю высоту рабочей области.
  // Страницы с картой во всю высоту: карта, маршруты, конструктор и результаты сценария, оценка локации.
  const fullBleed =
    pathname.startsWith("/map") ||
    pathname === "/network" ||
    pathname.startsWith("/scenarios/") ||
    pathname.startsWith("/site-selection");

  return (
    <div className={s.app}>
      <aside className={s.sidebar}>
        <Link to="/dashboard" className={s.brand}>
          <span className={s.brandMark} aria-hidden>
            FF
          </span>
          <span className={s.brandText}>FreightFlow</span>
        </Link>
        <nav className={s.nav} aria-label="Разделы">
          {navGroups.map((g) => {
            const items = g.items.filter((it) => can(user, it.permission));
            if (items.length === 0) {
              return null;
            }
            return (
              <div key={g.title}>
                <div className={s.groupTitle}>{g.title}</div>
                {items.map((it) => (
                  <Link key={it.to} to={it.to} className={s.navLink} title={it.title}>
                    <it.icon size={17} strokeWidth={1.7} aria-hidden />
                    <span className={s.navLabel}>{it.title}</span>
                  </Link>
                ))}
              </div>
            );
          })}
        </nav>
        <Link to="/profile" className={s.user}>
          <span className={s.avatar} aria-hidden>
            {initials(user.name)}
          </span>
          <span className={s.userText}>
            <span className={s.userName}>{user.name}</span>
            <span className={s.userRole}>{role}</span>
          </span>
        </Link>
      </aside>

      <div className={s.main}>
        <header className={s.topbar}>
          <div className={s.crumb}>
            Москва / <b>{title}</b>
          </div>
          <AriaButton className={s.search} onPress={() => palette.setOpen(true)}>
            <Search size={15} aria-hidden />
            <span className={s.searchLabel}>Поиск по системе</span>
            <kbd>Ctrl K</kbd>
          </AriaButton>
          <DialogTrigger>
            <IconButton label="Уведомления">
              <Bell size={18} strokeWidth={1.7} />
            </IconButton>
            <Popover className={popoverClass} placement="bottom end">
              <Dialog className={s.notifications} aria-label="Уведомления">
                <BellOff size={18} strokeWidth={1.7} aria-hidden />
                <div>Новых уведомлений нет</div>
              </Dialog>
            </Popover>
          </DialogTrigger>
          <UserMenu name={user.name} />
        </header>
        <main className={fullBleed ? s.contentFull : s.content}>
          <Outlet />
        </main>
      </div>
      <CommandPalette me={user} isOpen={palette.isOpen} onOpenChange={palette.setOpen} />
      <ToastRegion />
    </div>
  );
}

function UserMenu({ name }: { name: string }) {
  const { theme, change } = useThemePreference();
  const navigate = useNavigate();

  const onAction = (key: React.Key) => {
    const k = String(key);
    if (k === "logout") {
      void logout();
      return;
    }
    if (k === "profile") {
      void navigate({ to: "/profile" });
      return;
    }
    const t = themes.find((x) => x.id === k);
    if (t) {
      change(t.id);
    }
  };

  return (
    <MenuTrigger>
      <AriaButton className={s.menuTrigger} aria-label="Меню пользователя">
        {/* Инициалы — из атрибута через CSS: в имя кнопки входит только «Меню пользователя». */}
        <span className={s.avatar} data-initials={initials(name)} aria-hidden />
      </AriaButton>
      <Popover className={popoverClass} placement="bottom end">
        <Menu onAction={onAction} aria-label="Меню пользователя">
          <MenuItem id="profile" className={menuItemClass}>
            <User size={15} aria-hidden /> Профиль
          </MenuItem>
          <Separator className={menuSeparatorClass} />
          {themes.map((t) => (
            <MenuItem key={t.id} id={t.id} className={menuItemClass}>
              <span style={{ width: 15, display: "inline-grid" }}>
                {theme === t.id && <Check size={15} aria-hidden />}
              </span>
              {t.title}
            </MenuItem>
          ))}
          <Separator className={menuSeparatorClass} />
          <MenuItem id="logout" className={menuItemClass}>
            <LogOut size={15} aria-hidden /> Выйти
          </MenuItem>
        </Menu>
      </Popover>
    </MenuTrigger>
  );
}

async function logout() {
  await fetch("/logout", { method: "POST", credentials: "same-origin", redirect: "manual" });
  window.location.assign("/login");
}
