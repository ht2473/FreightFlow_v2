import {
  Bell,
  Building2,
  ChartLine,
  Construction,
  Database,
  FileText,
  GitBranch,
  KeyRound,
  Landmark,
  LayoutDashboard,
  type LucideIcon,
  Map as MapIcon,
  MapPinned,
  Route,
  Scale,
  ScrollText,
  Users,
} from "lucide-react";
import type { Permission } from "@/shared/auth";

export interface NavItem {
  to: string;
  title: string;
  icon: LucideIcon;
  permission: Permission;
}

export interface NavGroup {
  title: string;
  items: NavItem[];
}

/** Разделы приложения в боковом меню (DESIGN.md, раздел 5). */
export const navGroups: NavGroup[] = [
  {
    title: "Мониторинг",
    items: [
      { to: "/dashboard", title: "Дашборд", icon: LayoutDashboard, permission: "analytics.view" },
      { to: "/map", title: "Карта", icon: MapIcon, permission: "map.view" },
      { to: "/operations", title: "Обстановка", icon: Construction, permission: "map.view" },
      { to: "/alerts", title: "Алерты", icon: Bell, permission: "alerts.own" },
    ],
  },
  {
    title: "Анализ",
    items: [
      { to: "/facilities", title: "Объекты", icon: Building2, permission: "map.view" },
      { to: "/territories", title: "Территории", icon: Landmark, permission: "map.view" },
      { to: "/network", title: "Транспортная сеть", icon: Route, permission: "routing.use" },
      { to: "/regulation", title: "Регулирование", icon: Scale, permission: "map.view" },
      { to: "/analytics", title: "Аналитика", icon: ChartLine, permission: "analytics.view" },
      { to: "/scenarios", title: "Сценарии", icon: GitBranch, permission: "scenarios.manage" },
      { to: "/site-selection", title: "Оценка локации", icon: MapPinned, permission: "scenarios.manage" },
    ],
  },
  {
    title: "Данные",
    items: [
      { to: "/reports", title: "Отчёты", icon: FileText, permission: "reports.view" },
      { to: "/data", title: "Источники данных", icon: Database, permission: "data.manage" },
    ],
  },
  {
    title: "Администрирование",
    items: [
      { to: "/admin/users", title: "Пользователи", icon: Users, permission: "users.manage" },
      { to: "/admin/roles", title: "Роли и права", icon: KeyRound, permission: "users.manage" },
      { to: "/admin/audit", title: "Журнал аудита", icon: ScrollText, permission: "audit.view" },
    ],
  },
];

const navItems = navGroups.flatMap((g) => g.items);

/** Пункт меню, которому принадлежит адрес. */
export function navItemFor(pathname: string): NavItem | undefined {
  return navItems.find((it) => pathname === it.to || pathname.startsWith(`${it.to}/`));
}

/** Название раздела по адресу (для хлебных крошек и заголовка вкладки). */
export function titleFor(pathname: string): string {
  if (pathname.startsWith("/profile")) {
    return "Профиль";
  }
  if (pathname.startsWith("/search")) {
    return "Поиск";
  }
  if (pathname.startsWith("/dev/ui")) {
    return "Компоненты";
  }
  return navItemFor(pathname)?.title ?? "FreightFlow";
}
