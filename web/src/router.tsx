import {
  createRootRoute, createRoute, createRouter, redirect, Outlet,
} from '@tanstack/react-router';
import { getAccessToken } from './lib/api';
import { refresh } from './lib/auth';
import { AppShell } from './shell/AppShell';
import { LoginPage } from './routes/Login';
import { DashboardPage } from './routes/Dashboard';
import { ItemsPage } from './routes/Items';
import { NotFoundPage } from './routes/NotFound';
import { SettingsLayout } from './routes/settings/SettingsLayout';
import { ListView } from './routes/_ListView';
import { ModuleLanding } from './routes/ModuleLanding';
import { ModuleStub } from './routes/ModuleStub';
import { SalesInvoiceForm } from './routes/SalesInvoiceForm';
import { JournalEntryForm } from './routes/JournalEntryForm';
import { ReportsPage } from './routes/Reports';
import { CreateFormPage } from './routes/CreateFormPage';
import { doctypes, modules } from './lib/doctypes';
import { getCreateSchema } from './lib/createSchema';

const rootRoute = createRootRoute({ component: () => <Outlet /> });

const loginRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/login',
  component: LoginPage,
});

const appRoute = createRoute({
  getParentRoute: () => rootRoute,
  id: 'app',
  beforeLoad: async () => {
    if (!getAccessToken()) {
      const r = await refresh();
      if (!r) throw redirect({ to: '/login' });
    }
  },
  component: AppShell,
});

// Static routes
const dashboardRoute = createRoute({ getParentRoute: () => appRoute, path: '/',                          component: DashboardPage });
const itemsRoute     = createRoute({ getParentRoute: () => appRoute, path: '/accounting/items',          component: ItemsPage });

// Full forms
const siNewRoute  = createRoute({ getParentRoute: () => appRoute, path: '/accounting/sales-invoices/new',   component: SalesInvoiceForm });
const siEditRoute = createRoute({ getParentRoute: () => appRoute, path: '/accounting/sales-invoices/$id',   component: SalesInvoiceForm });
const jeNewRoute  = createRoute({ getParentRoute: () => appRoute, path: '/accounting/journal-entries/new',  component: JournalEntryForm });
const jeEditRoute = createRoute({ getParentRoute: () => appRoute, path: '/accounting/journal-entries/$id',  component: JournalEntryForm });

// Reports
const reportsIndex = createRoute({ getParentRoute: () => appRoute, path: '/accounting/reports',       component: ReportsPage });
const reportsKind  = createRoute({ getParentRoute: () => appRoute, path: '/accounting/reports/$slug', component: ReportsPage });

// Auto-built doctype list routes (skip the ones with custom pages/forms).
const SKIP_LIST = new Set([
  '/accounting/items',           // ItemsPage
  '/accounting/sales-invoices',  // (list registered separately so SI's `new` route doesn't collide)
  '/accounting/journal-entries', // same as SI
]);

const doctypeListRoutes = Object.values(doctypes)
  .filter((dt) => !SKIP_LIST.has(`${dt.modulePath}/${dt.slug}`))
  .map((dt) =>
    createRoute({
      getParentRoute: () => appRoute,
      path: `${dt.modulePath}/${dt.slug}`,
      component: () => <ListView config={dt} />,
    }),
  );

// SI + JE need their own list routes since we skipped them above.
const siListRoute = createRoute({ getParentRoute: () => appRoute, path: '/accounting/sales-invoices',  component: () => <ListView config={doctypes.salesInvoices!} /> });
const jeListRoute = createRoute({ getParentRoute: () => appRoute, path: '/accounting/journal-entries', component: () => <ListView config={doctypes.journalEntries!} /> });

// Auto-built /new routes for every doctype that has a create schema and no bespoke form.
const BESPOKE_NEW = new Set(['/accounting/sales-invoices', '/accounting/journal-entries']);

const doctypeNewRoutes = Object.values(doctypes)
  .filter((dt) => !BESPOKE_NEW.has(`${dt.modulePath}/${dt.slug}`) && !!getCreateSchema(dt.modulePath, dt.slug))
  .map((dt) => {
    const schema = getCreateSchema(dt.modulePath, dt.slug)!;
    return createRoute({
      getParentRoute: () => appRoute,
      path: `${dt.modulePath}/${dt.slug}/new`,
      component: () => <CreateFormPage config={dt} schema={schema} />,
    });
  });

const moduleLandingRoutes = modules.map((m) =>
  createRoute({
    getParentRoute: () => appRoute,
    path: m.path,
    component: () => <ModuleLanding modulePath={m.path} />,
  }),
);

const settingsIndexRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/settings',
  beforeLoad: () => { throw redirect({ to: '/settings/$section' as never, params: { section: 'appearance' } as never }); },
});
const settingsSectionRoute = createRoute({
  getParentRoute: () => appRoute,
  path: '/settings/$section',
  component: SettingsLayout,
});
const helpRoute = createRoute({ getParentRoute: () => appRoute, path: '/help', component: () => <ModuleStub module="Help" /> });

const routeTree = rootRoute.addChildren([
  loginRoute,
  appRoute.addChildren([
    dashboardRoute,
    itemsRoute,
    siNewRoute, siEditRoute, siListRoute,
    jeNewRoute, jeEditRoute, jeListRoute,
    reportsIndex, reportsKind,
    ...doctypeListRoutes,
    ...doctypeNewRoutes,
    ...moduleLandingRoutes,
    settingsIndexRoute, settingsSectionRoute, helpRoute,
  ]),
]);

export const router = createRouter({
  routeTree,
  defaultPreload: 'intent',
  defaultNotFoundComponent: NotFoundPage,
});

declare module '@tanstack/react-router' {
  interface Register { router: typeof router; }
}
