import { ChevronRight, Home } from 'lucide-react';
import { Link } from '@tanstack/react-router';
import { cn } from '@/lib/cn';

interface Crumb {
  label: string;
  to?: string;
}

interface PageHeaderProps {
  crumbs?: Crumb[];
  title: React.ReactNode;
  subtitle?: React.ReactNode;
  actions?: React.ReactNode;
  status?: React.ReactNode;
  className?: string;
}

/**
 * Page header — no gradient, no chrome. White canvas, hairline divider below,
 * generous breathing room. Sits flush with the TopChrome above it.
 */
export function PageHeader({ crumbs, title, subtitle, actions, status, className }: PageHeaderProps) {
  return (
    <div className={cn('bg-canvas border-b border-hairline', className)}>
      <div className="px-6 lg:px-8 pt-3 pb-6">
        {crumbs && crumbs.length > 0 && (
          <nav className="flex items-center flex-wrap gap-1 text-caption text-stone mb-3" aria-label="Breadcrumb">
            <Link to={'/' as never} className="hover:text-ink inline-flex items-center">
              <Home className="size-3" />
            </Link>
            {crumbs.map((c, i) => (
              <span key={i} className="inline-flex items-center gap-1">
                <ChevronRight className="size-3 text-muted shrink-0" />
                {c.to
                  ? <Link to={c.to as never} className="hover:text-ink truncate max-w-[200px]">{c.label}</Link>
                  : <span className="text-ink truncate max-w-[280px]">{c.label}</span>}
              </span>
            ))}
          </nav>
        )}
        <div className="flex items-end justify-between gap-4 flex-wrap">
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-3 min-w-0">
              {typeof title === 'string'
                ? <h1 className="text-heading-3 text-ink truncate">{title}</h1>
                : title}
              {status}
            </div>
            {subtitle && <div className="mt-1.5 text-body-sm text-slate">{subtitle}</div>}
          </div>
          {actions && <div className="flex items-center gap-2 shrink-0 flex-wrap justify-end">{actions}</div>}
        </div>
      </div>
    </div>
  );
}
