import { createRootRoute, createRoute, createRouter, lazyRouteComponent, redirect } from "@tanstack/react-router";
import { analyticsSearchSchema } from "@/features/analytics/AnalyticsPage";
import { compareSearchSchema, facilitySearchSchema } from "@/features/facilities/search";
import { mapSearchSchema } from "@/features/map/state";
import { networkSearchSchema } from "@/features/network/search";
import { territorySearchSchema } from "@/features/territories/search";
import { ErrorPage, PendingPage } from "./ErrorPage";
import { NotFound } from "./NotFound";
import { SectionPage } from "./SectionPage";
import { sections } from "./sections";
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

/** Раздел, наполнение которого появляется вместе с данными; пока — описание и пустое состояние. */
function sectionRoute<P extends string>(path: P) {
  const section = sections.find((sec) => sec.path === path);
  if (!section) {
    throw new Error(`нет описания раздела ${path}`);
  }
  return createRoute({
    getParentRoute: () => rootRoute,
    path,
    component: () => <SectionPage section={section} />,
  });
}

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
  sectionRoute("/operations"),
  sectionRoute("/alerts"),
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
  sectionRoute("/scenarios"),
  sectionRoute("/site-selection"),
  sectionRoute("/reports"),
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
