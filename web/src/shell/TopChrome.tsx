import { useEffect, useState } from 'react';
import { Search, Menu } from 'lucide-react';
import { useUI } from '@/store/ui';
import { Avatar } from '@/components/Avatar';
import { Kbd } from '@/components/Kbd';
import {
  DropdownMenu, DropdownMenuTrigger, DropdownMenuContent,
  DropdownMenuItem, DropdownMenuSeparator, DropdownMenuLabel,
} from '@/components/DropdownMenu';
import { NotificationsPopover } from '@/components/NotificationsPopover';
import { logout, me } from '@/lib/auth';

/**
 * Persistent top chrome. Mintlify look: 36px search pill, 32px circular
 * icon buttons, generous gap to the page header below.
 */
export function TopChrome() {
  const togglePalette = useUI((s) => s.togglePalette);
  const toggleSidebar = useUI((s) => s.toggleSidebar);
  const toggleTheme = useUI((s) => s.toggleTheme);

  const [user, setUser] = useState<{ full_name: string; email: string } | null>(null);
  useEffect(() => {
    me()
      .then((u) => setUser({ full_name: u.full_name || u.email, email: u.email }))
      .catch(() => setUser(null));
  }, []);

  return (
    <div className="flex items-center gap-3 px-6 lg:px-8 h-14 bg-canvas">
      <button
        onClick={toggleSidebar}
        className="lg:hidden text-steel hover:text-ink p-1 rounded-md hover:bg-surface"
        aria-label="Toggle sidebar"
      >
        <Menu className="size-5" />
      </button>

      {/* Search pill — 36px, hairline, opens ⌘K palette */}
      <button
        onClick={togglePalette}
        className="hidden md:flex items-center gap-2 px-3 h-9 rounded-full bg-surface border border-hairline hover:border-stone/40 hover:bg-canvas transition-colors w-[360px] text-left"
      >
        <Search className="size-4 text-stone" />
        <span className="text-body-sm text-stone">Search or run a command…</span>
        <span className="ml-auto flex items-center gap-1">
          <Kbd>⌘</Kbd><Kbd>K</Kbd>
        </span>
      </button>

      <div className="ml-auto flex items-center gap-2">
        <NotificationsPopover />

        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button className="rounded-full focus:outline-none focus-visible:ring-2 focus-visible:ring-brand-green/40">
              <Avatar name={user?.full_name} size="md" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end" className="min-w-[220px]">
            <DropdownMenuLabel>
              <div className="text-body-sm font-medium text-ink">{user?.full_name ?? 'Loading…'}</div>
              <div className="text-caption font-normal text-stone">{user?.email}</div>
            </DropdownMenuLabel>
            <DropdownMenuSeparator />
            <DropdownMenuItem onClick={() => toggleTheme()}>
              Switch theme
              <span className="ml-auto kbd">T</span>
            </DropdownMenuItem>
            <DropdownMenuItem>Account settings</DropdownMenuItem>
            <DropdownMenuSeparator />
            <DropdownMenuItem
              onClick={async () => { await logout(); window.location.href = '/login'; }}
              className="text-brand-error data-[highlighted]:bg-brand-error/10 data-[highlighted]:text-brand-error"
            >
              Log out
            </DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </div>
  );
}
