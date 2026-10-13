import { createRootRoute, createRoute, createRouter, lazyRouteComponent, redirect } from "@tanstack/react-router";
import { mapSearchSchema } from "@/features/map/state";
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
  sectionRoute("/dashboard"),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/map",
    validateSearch: (search: Record<string, unknown>) => mapSearchSchema.parse(search),
    component: lazyRouteComponent(() => import("@/features/map/MapPage"), "MapPage"),
  }),
  sectionRoute("/operations"),
  sectionRoute("/alerts"),
  sectionRoute("/facilities"),
  sectionRoute("/network"),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/regulation",
    component: lazyRouteComponent(() => import("@/features/regulation/RegulationPage"), "RegulationPage"),
  }),
  sectionRoute("/analytics"),
  sectionRoute("/scenarios"),
  sectionRoute("/site-selection"),
  sectionRoute("/reports"),
  createRoute({
    getParentRoute: () => rootRoute,
    path: "/data",
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
