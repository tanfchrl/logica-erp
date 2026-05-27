import { Link, useParams } from '@tanstack/react-router';
import { PageHeader } from '@/components/PageHeader';
import { cn } from '@/lib/cn';
import { SECTIONS, GROUPS, findSection } from './sections';
import { useMyPermissions } from '@/lib/permissions';

/**
 * Settings shell — sidebar nav driven by the section registry, with the
 * active section rendered on the right. Each section is its own component
 * (see `./sections.tsx`).
 */
export function SettingsLayout() {
  const { section: sectionKey } = useParams({ strict: false }) as { section?: string };
  const active = findSection(sectionKey);
  const Section = active.component;
  const perms = useMyPermissions();
  // Filter sections by requireSystem. A non-system user landing on a
  // requireSystem section sees the 403 message from the underlying handler
  // anyway, but the nav hides the entry so they don't trip into it.
  const visibleSections = perms.isSystem
    ? SECTIONS
    : SECTIONS.filter((s) => !s.requireSystem);

  return (
    <>
      <PageHeader
        crumbs={[{ label: 'Settings', to: '/settings' }, { label: active.label }]}
        title={active.label}
        subtitle={active.description}
      />

      <div className="flex-1 px-6 lg:px-8 pt-6 pb-12">
        <div className="grid grid-cols-1 lg:grid-cols-[240px_1fr] gap-8 max-w-6xl">
          {/* Sidebar nav — grouped */}
          <nav className="space-y-5 lg:sticky lg:top-4 lg:self-start">
            {GROUPS.map((g) => {
              const items = visibleSections.filter((s) => s.group === g.key);
              if (items.length === 0) return null;
              return (
                <div key={g.key}>
                  <div className="px-2.5 pb-1.5 text-micro-uppercase text-stone">
                    {g.label}
                  </div>
                  <ul className="space-y-0.5">
                    {items.map((s) => {
                      const isActive = s.key === active.key;
                      return (
                        <li key={s.key}>
                          <Link
                            to={'/settings/$section' as never}
                            params={{ section: s.key } as never}
                            className={cn(
                              'flex items-center gap-2.5 w-full px-2.5 h-8 rounded-md text-body-sm transition-colors',
                              isActive
                                ? 'bg-surface text-ink font-medium'
                                : 'text-steel hover:bg-surface hover:text-ink',
                            )}
                          >
                            <s.icon className="size-4 shrink-0" />
                            <span className="truncate">{s.label}</span>
                          </Link>
                        </li>
                      );
                    })}
                  </ul>
                </div>
              );
            })}
          </nav>

          {/* Active section content */}
          <div className="min-w-0">
            <Section />
          </div>
        </div>
      </div>
    </>
  );
}
