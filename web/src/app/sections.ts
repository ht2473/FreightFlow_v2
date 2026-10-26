import type { Permission } from "@/shared/auth";

/** Раздел, который наполняется по мере подключения данных и расчётов. */
export interface Section {
  path: string;
  title: string;
  permission: Permission;
  summary: string;
  points: string[];
}

export const sections: Section[] = [
  {
    path: "/operations",
    title: "Оперативная обстановка",
    permission: "map.view",
    summary: "Дорожные работы и ограничения движения с жизненным циклом записей.",
    points: [
      "Плановые и аварийные работы, ограничения на период работ",
      "Влияние на значимые участки, мосты и подъезды к объектам",
      "Лента изменений по ежедневным снимкам",
    ],
  },
  {
    path: "/alerts",
    title: "Алерты",
    permission: "alerts.own",
    summary: "Правила наблюдения и уведомления об изменениях.",
    points: [
      "Работы на коридоре, у объекта или в области",
      "Изменение ограничений и доступности объектов",
      "Каналы: в системе, почта, Telegram, вебхуки",
    ],
  },
  {
    path: "/site-selection",
    title: "Оценка локации",
    permission: "scenarios.manage",
    summary: "Поиск мест под логистический объект по критериям и весам.",
    points: [
      "Критерии из каталога показателей и исключающие зоны",
      "Веса напрямую или попарным сравнением",
      "Ранжирование ячеек H3 с объяснением вклада критериев",
    ],
  },
  {
    path: "/reports",
    title: "Отчёты",
    permission: "reports.view",
    summary: "Отчёты и выгрузки по шаблонам.",
    points: [
      "Паспорт объекта, сводка по округу, проходимость, работы",
      "PDF, XLSX, CSV, GeoJSON, GeoPackage",
      "Расписание и рассылка",
    ],
  },
];
