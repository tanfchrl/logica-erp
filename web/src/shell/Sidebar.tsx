import { Link, useRouterState } from '@tanstack/react-router';
import {
  Home, Receipt, ShoppingBag, Wallet, BarChart3, Package, Warehouse, Factory,
  Briefcase, Users, UserSquare, ClipboardList, Headphones, Settings, HelpCircle,
  ChevronsLeft, Star, Plus, Building2, ChevronsUpDown,
} from 'lucide-react';
import { useTranslation } from 'react-i18next';
import { cn } from '@/lib/cn';
import { useUI } from '@/store/ui';
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

const sections: NavSection[] = [
  {
    items: [
      { to: '/', label: 'Home', icon: Home, shortcut: 'G H' },
    ],
  },
  {
    label: 'Starred',
    items: [
      { to: '/accounting/sales-invoices', label: 'Sales Invoices', icon: Receipt },
      { to: '/accounting/items', label: 'Items', icon: Package },
    ],
  },
  {
    label: 'Modules',
    items: [
      { to: '/accounting',     label: 'Finance',            icon: Wallet },
      { to: '/stock',          label: 'Stock',              icon: Warehouse },
      { to: '/buying',         label: 'Procurement',        icon: ShoppingBag },
      { to: '/selling',        label: 'Sales',              icon: BarChart3 },
      { to: '/manufacturing',  label: 'Production',         icon: Factory },
      { to: '/projects',       label: 'Operations',         icon: Briefcase },
      { to: '/crm',            label: 'CRM',                icon: UserSquare },
      { to: '/hr',             label: 'HR & Payroll',       icon: Users },
      { to: '/assets',         label: 'Asset & Inventory',  icon: ClipboardList },
      { to: '/support',        label: 'Helpdesk',           icon: Headphones },
    ],
  },
];

export function Sidebar() {
  const { sidebarCollapsed, toggleSidebar } = useUI();
  const router = useRouterState();
  const path = router.location.pathname;
  const { t: _t } = useTranslation();

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
        {sections.map((section, sIdx) => (
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
