import { createRootRoute, createRoute, createRouter, lazyRouteComponent, redirect } from "@tanstack/react-router";
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
  sectionRoute("/map"),
  sectionRoute("/operations"),
  sectionRoute("/alerts"),
  sectionRoute("/facilities"),
  sectionRoute("/network"),
  sectionRoute("/regulation"),
  sectionRoute("/analytics"),
  sectionRoute("/scenarios"),
  sectionRoute("/site-selection"),
  sectionRoute("/reports"),
  sectionRoute("/data"),
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
]);

export const router = createRouter({
  routeTree,
  defaultPreload: "intent",
  scrollRestoration: true,
});

declare module "@tanstack/react-router" {
  interface Register {
    router: typeof router;
  }
}
