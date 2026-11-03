import { createRootRoute, createRoute, createRouter, lazyRouteComponent, redirect } from "@tanstack/react-router";
import { alertsSearchSchema } from "@/features/alerts/search";
import { analyticsSearchSchema } from "@/features/analytics/search";
import { compareSearchSchema, facilitySearchSchema } from "@/features/facilities/search";
import { mapSearchSchema } from "@/features/map/state";
import { networkSearchSchema } from "@/features/network/search";
import { printSearchSchema } from "@/features/print/search";
import { reportsSearchSchema } from "@/features/reports/search";
import { territorySearchSchema } from "@/features/territories/search";
import { ErrorPage, PendingPage } from "./ErrorPage";
import { NotFound } from "./NotFound";
import { AppShell } from "./shell/AppShell";

const rootRoute = createRootRoute({
  component: AppShell,
  notFoundComponent: NotFound,
});

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: "/",
  beforeLoad: () => {
    throw redirect({ to: "/dashboard" });
  },
});

const routeTree = rootRoute.addChildren([
  indexRoute,
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/dashboard",
    component: lazyRouteComponent(() => import("@/features/dashboard/DashboardPage"), "DashboardPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/map",
    validateSearch: (search: Record<string, unknown>) => mapSearchSchema.parse(search),
    component: lazyRouteComponent(() => import("@/features/map/MapPage"), "MapPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/operations",
    component: lazyRouteComponent(() => import("@/features/operations/OperationsPage"), "OperationsPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/api-docs",
    component: lazyRouteComponent(() => import("@/features/apidocs/ApiDocsPage"), "ApiDocsPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/print/$template",
    validateSearch: printSearchSchema,
    component: lazyRouteComponent(() => import("@/features/print/PrintPage"), "PrintPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/alerts",
    validateSearch: alertsSearchSchema,
    component: lazyRouteComponent(() => import("@/features/alerts/AlertsPage"), "AlertsPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/alerts/rules/new",
    component: lazyRouteComponent(() => import("@/features/alerts/RuleEditor"), "RuleEditorPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/alerts/rules/$ruleId",
    component: lazyRouteComponent(() => import("@/features/alerts/RuleEditor"), "RuleEditorPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/facilities",
    validateSearch: (search: Record<string, unknown>) => facilitySearchSchema.parse(search),
    component: lazyRouteComponent(() => import("@/features/facilities/FacilitiesPage"), "FacilitiesPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/facilities/compare",
    validateSearch: (search: Record<string, unknown>) => compareSearchSchema.parse(search),
    component: lazyRouteComponent(() => import("@/features/facilities/ComparePage"), "ComparePage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/facilities/reports",
    component: lazyRouteComponent(() => import("@/features/facilities/ReportsPage"), "ReportsPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/facilities/$facilityId",
    component: lazyRouteComponent(() => import("@/features/facilities/FacilityPage"), "FacilityPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/search",
    validateSearch: (search: Record<string, unknown>) => ({
      q: typeof search.q === "string" ? search.q.slice(0, 300) : undefined,
    }),
    component: lazyRouteComponent(() => import("@/features/search/SearchPage"), "SearchPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/territories",
    validateSearch: (search: Record<string, unknown>) => territorySearchSchema.parse(search),
    component: lazyRouteComponent(() => import("@/features/territories/TerritoriesPage"), "TerritoriesPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/territories/$territoryId",
    component: lazyRouteComponent(() => import("@/features/territories/TerritoryPage"), "TerritoryPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/network",
    validateSearch: (search: Record<string, unknown>) => networkSearchSchema.parse(search),
    component: lazyRouteComponent(() => import("@/features/network/RoutePage"), "RoutePage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/network/analysis",
    component: lazyRouteComponent(() => import("@/features/network/AnalysisPage"), "AnalysisPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/regulation",
    component: lazyRouteComponent(() => import("@/features/regulation/RegulationPage"), "RegulationPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/analytics",
    validateSearch: (search: Record<string, unknown>) => analyticsSearchSchema.parse(search),
    component: lazyRouteComponent(() => import("@/features/analytics/AnalyticsPage"), "AnalyticsPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/analytics/methods",
    component: lazyRouteComponent(() => import("@/features/analytics/MethodsPage"), "MethodsPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/scenarios",
    component: lazyRouteComponent(() => import("@/features/scenarios/ScenariosPage"), "ScenariosPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/scenarios/new",
    validateSearch: (search: Record<string, unknown>) => ({
      from: typeof search.from === "string" ? search.from : undefined,
      element: typeof search.element === "number" ? search.element : undefined,
    }),
    component: lazyRouteComponent(() => import("@/features/scenarios/ScenarioBuilder"), "ScenarioBuilderPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/scenarios/$scenarioId",
    component: lazyRouteComponent(() => import("@/features/scenarios/ScenarioResult"), "ScenarioResultPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/site-selection",
    component: lazyRouteComponent(() => import("@/features/site-selection/SitePage"), "SitePage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/reports",
    validateSearch: reportsSearchSchema,
    component: lazyRouteComponent(() => import("@/features/reports/ReportsPage"), "ReportsPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/data",
    validateSearch: (search: Record<string, unknown>) => ({
      source: typeof search.source === "string" ? search.source : undefined,
    }),
    component: lazyRouteComponent(() => import("@/features/data/SourcesPage"), "SourcesPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/profile",
    component: lazyRouteComponent(() => import("@/features/profile/ProfilePage"), "ProfilePage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/admin/users",
    component: lazyRouteComponent(() => import("@/features/admin/UsersPage"), "UsersPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/admin/roles",
    component: lazyRouteComponent(() => import("@/features/admin/RolesPage"), "RolesPage"),
  }),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/admin/audit",
    component: lazyRouteComponent(() => import("@/features/admin/AuditPage"), "AuditPage"),
  }),
  // Витрина компонентов — только в сборке для разработки.
  ...(import.meta.env.DEV
    ? [
        createRoute({
          getParentRoute: () => rootRoute,
          path: "/dev/ui",
          component: lazyRouteComponent(() => import("@/features/dev/UiGallery"), "UiGallery"),
        }),
      ]
    : []),
]);

export const router = createRouter({
  routeTree,
  defaultPreload: "intent",
  defaultErrorComponent: ErrorPage,
  defaultPendingComponent: PendingPage,
  defaultPendingMs: 300,
  scrollRestoration: true,
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
