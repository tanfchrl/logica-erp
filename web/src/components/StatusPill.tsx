import { cn } from '@/lib/cn';

type Tone = 'neutral' | 'info' | 'success' | 'warning' | 'danger' | 'accent';

/**
 * Status pills follow the Mintlify "tinted background + saturated text"
 * recipe. The mint accent tone is used very sparingly — typically for the
 * featured/highlighted row in a list.
 */
const tones: Record<Tone, string> = {
  neutral: 'bg-surface text-steel',
  info:    'bg-info/10 text-info',
  success: 'bg-success/10 text-success',
  warning: 'bg-warning/10 text-warning',
  danger:  'bg-danger/10 text-danger',
  accent:  'bg-brand-green-soft/30 text-brand-green-deep',
};

const dotTones: Record<Tone, string> = {
  neutral: 'bg-stone',
  info:    'bg-info',
  success: 'bg-success',
  warning: 'bg-warning',
  danger:  'bg-danger',
  accent:  'bg-brand-green',
};

export function StatusPill({ tone = 'neutral', children, withDot = true, className }: {
  tone?: Tone; children: React.ReactNode; withDot?: boolean; className?: string;
}) {
  return (
    <span className={cn('pill-base', tones[tone], className)}>
      {withDot ? <span className={cn('size-1.5 rounded-full', dotTones[tone])} /> : null}
      {children}
    </span>
  );
}

/** Map docstatus 0/1/2 → friendly label + tone. */
export function DocstatusPill({ docstatus }: { docstatus: number }) {
  if (docstatus === 1) return <StatusPill tone="success">Submitted</StatusPill>;
  if (docstatus === 2) return <StatusPill tone="danger">Cancelled</StatusPill>;
  return <StatusPill tone="neutral">Draft</StatusPill>;
}
