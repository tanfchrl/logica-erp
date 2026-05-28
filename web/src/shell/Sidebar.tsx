import { Link, useRouterState } from '@tanstack/react-router';
import {
  Home, ShoppingBag, Wallet, BarChart3, Warehouse, Factory,
  Briefcase, Users, UserSquare, ClipboardList, Headphones, Settings, HelpCircle,
  ChevronsLeft, Star, Plus, Building2, ChevronsUpDown,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/cn';
import { useUI } from '@/store/ui';
import { useMyPermissions } from '@/lib/permissions';
import { useStarredMenu } from '@/lib/starredMenu';
import { doctypes } from '@/lib/doctypes';
import { Tooltip } from '@/components/Tooltip';
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, DropdownMenuSeparator } from '@/components/DropdownMenu';

interface NavItem {
  to: string;
  label: string;
  icon: React.ComponentType<{ className?: string }>;
  shortcut?: string;
}

interface NavSection {
  label?: string;
  items: NavItem[];
}

// requireAny: the user needs read access to AT LEAST ONE of these doctypes
// for the menu item to show. Items without `requireAny` are always visible
// (e.g. Home). Module umbrella items (e.g. /accounting) list every doctype
// that lives under that module — a Manager who can only read `customer`
// still sees /accounting because it has something inside.
type GuardedNavItem = NavItem & { requireAny?: string[] };

const sections: { label?: string; items: GuardedNavItem[] }[] = [
  {
    items: [
      { to: '/', label: 'Home', icon: Home, shortcut: 'G H' },
    ],
  },
  {
    label: 'Modules',
    items: [
      { to: '/accounting',     label: 'Finance',            icon: Wallet,         requireAny: ['sales_invoice','purchase_invoice','journal_entry','payment_entry','customer','supplier','item','tax_template','account'] },
      { to: '/stock',          label: 'Stock',              icon: Warehouse,      requireAny: ['warehouse'] },
      { to: '/buying',         label: 'Procurement',        icon: ShoppingBag,    requireAny: ['material_request','purchase_order','purchase_receipt','purchase_invoice','supplier'] },
      { to: '/selling',        label: 'Sales',              icon: BarChart3,      requireAny: ['sales_invoice','customer','lead'] },
      { to: '/manufacturing',  label: 'Production',         icon: Factory,        requireAny: ['bom','work_order'] },
      { to: '/projects',       label: 'Operations',         icon: Briefcase,      requireAny: ['project'] },
      { to: '/crm',            label: 'CRM',                icon: UserSquare,     requireAny: ['lead','customer'] },
      { to: '/hr',             label: 'HR & Payroll',       icon: Users,          requireAny: ['employee'] },
      { to: '/assets',         label: 'Asset & Inventory',  icon: ClipboardList,  requireAny: ['asset'] },
      { to: '/support',        label: 'Helpdesk',           icon: Headphones,     requireAny: ['issue'] },
    ],
  },
];

export function Sidebar() {
  const { sidebarCollapsed, toggleSidebar } = useUI();
  const router = useRouterState();
  const path = router.location.pathname;
  const { t: _t } = useTranslation();
  const perms = useMyPermissions();
  const { items: stars } = useStarredMenu();

  // Filter each section's items by the caller's read perms. Drop sections
  // that end up empty — a label with zero items is just noise.
  const visibleSections = sections
    .map((s) => ({
      ...s,
      items: s.items.filter((it) => !it.requireAny || perms.canRead_any(it.requireAny)),
    }))
    .filter((s) => s.items.length > 0);

  // Build the dynamic Starred section from the user's saved stars. Icon
  // resolution: prefer the matching DoctypeConfig.icon (so the sidebar
  // looks like the page header); fall back to a generic Star.
  const starredSection: { label: string; items: NavItem[] } | null = stars.length === 0 ? null : {
    label: 'Starred',
    items: stars.map((s) => ({
      to: s.path,
      label: s.label,
      icon: iconForPath(s.path),
    })),
  };

  return (
    <aside
      className={cn(
        'shrink-0 h-screen flex flex-col bg-canvas border-r border-hairline transition-[width] duration-150 ease-gentle',
        sidebarCollapsed ? 'w-[64px]' : 'w-[240px]',
      )}
    >
      {/* Workspace switcher */}
      <div className="px-3 pt-3 pb-2">
        <WorkspaceSwitcher collapsed={sidebarCollapsed} />
      </div>

      {/* Nav */}
      <nav className="flex-1 overflow-y-auto px-2 py-2 space-y-5">
        {starredSection && (
          <div>
            {!sidebarCollapsed && (
              <div className="px-2.5 pt-1 pb-1.5 flex items-center gap-1.5 text-stone text-micro-uppercase">
                <Star className="size-3" />
                {starredSection.label}
              </div>
            )}
            <ul className="space-y-0.5">
              {starredSection.items.map((item) => {
                const active = path === item.to || (item.to !== '/' && path.startsWith(item.to));
                const link = (
                  <Link
                    to={item.to as never}
                    className={cn(
                      'flex items-center gap-2.5 px-2.5 h-8 rounded-md text-body-sm transition-colors',
                      active
                        ? 'bg-surface text-ink font-medium'
                        : 'text-steel hover:bg-surface hover:text-ink',
                      sidebarCollapsed && 'justify-center px-0',
                    )}
                  >
                    {active && !sidebarCollapsed && (
                      <span className="absolute left-0 size-1.5 rounded-full bg-brand-green -ml-0.5" aria-hidden />
                    )}
                    <item.icon className={cn('size-4 shrink-0', active && 'text-ink')} />
                    {!sidebarCollapsed && <span className="truncate">{item.label}</span>}
                  </Link>
                );
                return (
                  <li key={item.to} className="group relative">
                    {sidebarCollapsed ? <Tooltip side="right" content={item.label}>{link}</Tooltip> : link}
                  </li>
                );
              })}
            </ul>
          </div>
        )}
        {visibleSections.map((section, sIdx) => (
          <div key={sIdx}>
            {section.label && !sidebarCollapsed && (
              <div className="px-2.5 pt-1 pb-1.5 flex items-center gap-1.5 text-stone text-micro-uppercase">
                {section.label === 'Starred' && <Star className="size-3" />}
                {section.label}
              </div>
            )}
            <ul className="space-y-0.5">
              {section.items.map((item) => {
                const active = path === item.to || (item.to !== '/' && path.startsWith(item.to));
                const link = (
                  <Link
                    to={item.to}
                    className={cn(
                      'flex items-center gap-2.5 px-2.5 h-8 rounded-md text-body-sm transition-colors',
                      active
                        ? 'bg-surface text-ink font-medium'
                        : 'text-steel hover:bg-surface hover:text-ink',
                      sidebarCollapsed && 'justify-center px-0',
                    )}
                  >
                    {/* Mint dot indicator on the leading edge when active */}
                    {active && !sidebarCollapsed && (
                      <span className="absolute left-0 size-1.5 rounded-full bg-brand-green -ml-0.5" aria-hidden />
                    )}
                    <item.icon className={cn('size-4 shrink-0', active && 'text-ink')} />
                    {!sidebarCollapsed && (
                      <>
                        <span className="truncate">{item.label}</span>
                        {item.shortcut && (
                          <span className="ml-auto kbd opacity-0 group-hover:opacity-100 transition-opacity">{item.shortcut}</span>
                        )}
                      </>
                    )}
                  </Link>
                );
                return (
                  <li key={item.to} className="group relative">
                    {sidebarCollapsed ? <Tooltip side="right" content={item.label}>{link}</Tooltip> : link}
                  </li>
                );
              })}
            </ul>
          </div>
        ))}
      </nav>

      {/* Footer */}
      <div className="px-2 py-2 border-t border-hairline space-y-0.5">
        {[
          { to: '/settings', label: 'Settings', icon: Settings },
          { to: '/help',     label: 'Help',     icon: HelpCircle },
        ].map((it) => (
          <Link
            key={it.to}
            to={it.to}
            className={cn(
              'flex items-center gap-2.5 px-2.5 h-8 rounded-md text-body-sm text-steel hover:bg-surface hover:text-ink transition-colors',
              sidebarCollapsed && 'justify-center px-0',
            )}
          >
            <it.icon className="size-4 shrink-0" />
            {!sidebarCollapsed && <span className="truncate">{it.label}</span>}
          </Link>
        ))}
        <button
          type="button"
          onClick={toggleSidebar}
          className={cn(
            'flex items-center gap-2.5 px-2.5 h-8 w-full rounded-md text-body-sm text-stone hover:bg-surface hover:text-ink transition-colors',
            sidebarCollapsed && 'justify-center px-0',
          )}
          aria-label="Toggle sidebar"
        >
          <ChevronsLeft className={cn('size-4 shrink-0 transition-transform', sidebarCollapsed && 'rotate-180')} />
          {!sidebarCollapsed && <span className="truncate text-caption">Collapse</span>}
        </button>
      </div>
    </aside>
  );
}

// Resolve the icon for a starred path by matching against the DoctypeConfig
// registry. Falls back to the Star icon for paths that aren't ListView pages
// (rare today but possible if a user stars a custom URL later).
function iconForPath(p: string): React.ComponentType<{ className?: string }> {
  for (const d of Object.values(doctypes)) {
    if (`${d.modulePath}/${d.slug}` === p) return d.icon;
  }
  return Star;
}

function WorkspaceSwitcher({ collapsed }: { collapsed: boolean }) {
  const brand = useUI((s) => s.brand);
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        className={cn(
          'flex items-center gap-2 w-full p-1.5 rounded-md hover:bg-surface transition-colors',
          'focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-green/40',
          collapsed && 'justify-center',
        )}
      >
        {/* Brand mark — uploaded logo, else the configured letter mark */}
        <span className="inline-flex items-center justify-center size-7 rounded-md bg-primary text-primary-fg font-semibold overflow-hidden">
          {brand.logoDataUrl
            ? <img src={brand.logoDataUrl} alt="" className="size-full object-contain" />
            : brand.mark}
        </span>
        {!collapsed && (
          <>
            <div className="flex-1 min-w-0 text-left">
              <div className="text-body-sm font-medium text-ink truncate">{brand.name}</div>
              <div className="text-caption text-stone truncate">{brand.tagline}</div>
            </div>
            <ChevronsUpDown className="size-3.5 text-stone shrink-0" />
          </>
        )}
      </DropdownMenuTrigger>
      <DropdownMenuContent side="right" align="start" className="min-w-[220px]">
        <div className="px-2.5 py-1.5 text-caption text-stone">Companies</div>
        <DropdownMenuItem>
          <Building2 className="size-4 text-stone" />
          <span>{brand.name}</span>
          <span className="ml-auto text-caption text-brand-green">● Active</span>
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem>
          <Plus className="size-4 text-stone" />
          <span>Create company</span>
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
