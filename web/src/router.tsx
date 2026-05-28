import {
  createRootRoute, createRoute, createRouter, redirect, Outlet, Link,
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
import { PurchaseInvoiceForm } from './routes/PurchaseInvoiceForm';
import { PurchaseOrderForm } from './routes/PurchaseOrderForm';
import { MaterialRequestForm } from './routes/MaterialRequestForm';
import { OpportunityBoard } from './routes/OpportunityBoard';
import { Button } from './components/Button';
import { Columns3 } from 'lucide-react';
import { PurchaseReceiptForm } from './routes/PurchaseReceiptForm';
import { JournalEntryForm } from './routes/JournalEntryForm';
import { ReportsPage } from './routes/Reports';
import { CreateFormPage } from './routes/CreateFormPage';
import { DetailView } from './routes/_DetailView';
import { MigrationWizard, MigrationWizardEntry } from './routes/MigrationWizard';
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

// Setup wizard runs full-screen, no AppShell — auth-gated but no sidebar.
const setupParent = createRoute({
  getParentRoute: () => rootRoute,
  id: 'setup-parent',
  beforeLoad: async () => {
    if (!getAccessToken()) {
      const r = await refresh();
      if (!r) throw redirect({ to: '/login' });
    }
  },
  component: Outlet,
});
const setupEntryRoute  = createRoute({ getParentRoute: () => setupParent, path: '/setup',              component: MigrationWizardEntry });
const setupSessionRoute = createRoute({ getParentRoute: () => setupParent, path: '/setup/$sessionId',  component: MigrationWizard });

// Static routes
const dashboardRoute = createRoute({ getParentRoute: () => appRoute, path: '/',                          component: DashboardPage });
const itemsRoute     = createRoute({ getParentRoute: () => appRoute, path: '/accounting/items',          component: ItemsPage });

// Full forms
const siNewRoute  = createRoute({ getParentRoute: () => appRoute, path: '/accounting/sales-invoices/new',   component: SalesInvoiceForm });
const siEditRoute = createRoute({ getParentRoute: () => appRoute, path: '/accounting/sales-invoices/$id',   component: SalesInvoiceForm });
const piNewRoute  = createRoute({ getParentRoute: () => appRoute, path: '/accounting/purchase-invoices/new', component: PurchaseInvoiceForm });
const piEditRoute = createRoute({ getParentRoute: () => appRoute, path: '/accounting/purchase-invoices/$id', component: PurchaseInvoiceForm });
const poNewRoute  = createRoute({ getParentRoute: () => appRoute, path: '/accounting/purchase-orders/new',   component: PurchaseOrderForm });
const poEditRoute = createRoute({ getParentRoute: () => appRoute, path: '/accounting/purchase-orders/$id',   component: PurchaseOrderForm });
const oppBoardRoute = createRoute({ getParentRoute: () => appRoute, path: '/crm/opportunities/board', component: OpportunityBoard });
const mrNewRoute  = createRoute({ getParentRoute: () => appRoute, path: '/accounting/material-requests/new', component: MaterialRequestForm });
const mrEditRoute = createRoute({ getParentRoute: () => appRoute, path: '/accounting/material-requests/$id', component: MaterialRequestForm });
const grnNewRoute  = createRoute({ getParentRoute: () => appRoute, path: '/stock/purchase-receipts/new', component: PurchaseReceiptForm });
const grnEditRoute = createRoute({ getParentRoute: () => appRoute, path: '/stock/purchase-receipts/$id', component: PurchaseReceiptForm });
const jeNewRoute  = createRoute({ getParentRoute: () => appRoute, path: '/accounting/journal-entries/new',  component: JournalEntryForm });
const jeEditRoute = createRoute({ getParentRoute: () => appRoute, path: '/accounting/journal-entries/$id',  component: JournalEntryForm });

// Reports
const reportsIndex = createRoute({ getParentRoute: () => appRoute, path: '/accounting/reports',       component: ReportsPage });
const reportsKind  = createRoute({ getParentRoute: () => appRoute, path: '/accounting/reports/$slug', component: ReportsPage });

// Auto-built doctype list routes (skip the ones with custom pages/forms).
const SKIP_LIST = new Set([
  '/accounting/items',              // ItemsPage
  '/accounting/sales-invoices',     // (list registered separately so SI's `new` route doesn't collide)
  '/accounting/purchase-invoices',  // PI bespoke form needs its own list registration too
  '/accounting/purchase-orders',    // PO same pattern
  '/accounting/material-requests',  // MR same pattern
  '/stock/purchase-receipts',       // GRN same pattern
  '/accounting/journal-entries',    // same as SI
  '/crm/opportunities',             // bespoke list — adds Board view toggle
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
const siListRoute = createRoute({ getParentRoute: () => appRoute, path: '/accounting/sales-invoices',    component: () => <ListView config={doctypes.salesInvoices!} /> });
const piListRoute = createRoute({ getParentRoute: () => appRoute, path: '/accounting/purchase-invoices', component: () => <ListView config={doctypes.purchaseInvoices!} /> });
const poListRoute = createRoute({ getParentRoute: () => appRoute, path: '/accounting/purchase-orders',   component: () => <ListView config={doctypes.purchaseOrders!} /> });
const mrListRoute = createRoute({ getParentRoute: () => appRoute, path: '/accounting/material-requests', component: () => <ListView config={doctypes.materialRequests!} /> });
const grnListRoute = createRoute({ getParentRoute: () => appRoute, path: '/stock/purchase-receipts',     component: () => <ListView config={doctypes.purchaseReceipts!} /> });
const jeListRoute = createRoute({ getParentRoute: () => appRoute, path: '/accounting/journal-entries',   component: () => <ListView config={doctypes.journalEntries!} /> });
const oppListRoute = createRoute({
  getParentRoute: () => appRoute, path: '/crm/opportunities',
  component: () => (
    <ListView config={doctypes.opportunities!}
      extraActions={
        <Button variant="secondary" asChild>
          <Link to={'/crm/opportunities/board' as never}><Columns3 className="size-4" /> Board</Link>
        </Button>
      } />
  ),
});

// Auto-built /new routes for every doctype that has a create schema and no bespoke form.
const BESPOKE_NEW = new Set(['/accounting/sales-invoices', '/accounting/purchase-invoices', '/accounting/purchase-orders', '/accounting/material-requests', '/stock/purchase-receipts', '/accounting/journal-entries']);

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

// Auto-built /{id} detail routes for every non-bespoke doctype. SI/PI/JE have
// their own bespoke forms at /$id that double as detail pages, so they're
// skipped here.
const doctypeDetailRoutes = Object.values(doctypes)
  .filter((dt) => !BESPOKE_NEW.has(`${dt.modulePath}/${dt.slug}`) && !!getCreateSchema(dt.modulePath, dt.slug))
  .map((dt) => {
    const schema = getCreateSchema(dt.modulePath, dt.slug)!;
    return createRoute({
      getParentRoute: () => appRoute,
      path: `${dt.modulePath}/${dt.slug}/$id`,
      component: () => <DetailView config={dt} schema={schema} />,
    });
  });

// Auto-built /{id}/edit routes — reuses CreateFormPage in edit mode.
const doctypeEditRoutes = Object.values(doctypes)
  .filter((dt) => !BESPOKE_NEW.has(`${dt.modulePath}/${dt.slug}`) && !!getCreateSchema(dt.modulePath, dt.slug))
  .map((dt) => {
    const schema = getCreateSchema(dt.modulePath, dt.slug)!;
    return createRoute({
      getParentRoute: () => appRoute,
      path: `${dt.modulePath}/${dt.slug}/$id/edit`,
      component: () => <CreateFormPage config={dt} schema={schema} editMode />,
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
  setupParent.addChildren([setupEntryRoute, setupSessionRoute]),
  appRoute.addChildren([
    dashboardRoute,
    itemsRoute,
    siNewRoute, siEditRoute, siListRoute,
    piNewRoute, piEditRoute, piListRoute,
    poNewRoute, poEditRoute, poListRoute,
    mrNewRoute, mrEditRoute, mrListRoute,
    oppBoardRoute, oppListRoute,
    grnNewRoute, grnEditRoute, grnListRoute,
    jeNewRoute, jeEditRoute, jeListRoute,
    reportsIndex, reportsKind,
    ...doctypeListRoutes,
    ...doctypeNewRoutes,
    ...doctypeDetailRoutes,
    ...doctypeEditRoutes,
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
