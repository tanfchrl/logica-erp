import Decimal from 'decimal.js';

// Indonesian formatting per §6.9 of the brief.
const idIDNumber = new Intl.NumberFormat('id-ID', { minimumFractionDigits: 0, maximumFractionDigits: 2 });
const idIDMoney  = new Intl.NumberFormat('id-ID', { minimumFractionDigits: 2, maximumFractionDigits: 2 });
const idIDDate   = new Intl.DateTimeFormat('id-ID', { year: 'numeric', month: 'short', day: '2-digit' });
const idIDDateLong = new Intl.DateTimeFormat('id-ID', { year: 'numeric', month: 'long', day: '2-digit' });
const idIDDateTime = new Intl.DateTimeFormat('id-ID', {
  year: 'numeric', month: 'short', day: '2-digit', hour: '2-digit', minute: '2-digit',
});

/** Format a money amount; default currency = IDR with 'Rp ' prefix. */
export function money(value: string | number | null | undefined, currency = 'IDR'): string {
  if (value === null || value === undefined || value === '') return '—';
  const d = new Decimal(typeof value === 'number' ? value.toString() : value);
  const formatted = idIDMoney.format(d.toNumber());
  if (currency === 'IDR') return `Rp ${formatted}`;
  return `${currency} ${formatted}`;
}

/** Format a quantity / non-money number. */
export function num(value: string | number | null | undefined): string {
  if (value === null || value === undefined || value === '') return '—';
  const d = new Decimal(typeof value === 'number' ? value.toString() : value);
  return idIDNumber.format(d.toNumber());
}

/** Format an ISO date string as Indonesian short date. */
export function date(value: string | Date | null | undefined): string {
  if (!value) return '—';
  const d = typeof value === 'string' ? new Date(value) : value;
  return idIDDate.format(d);
}

export function dateLong(value: string | Date | null | undefined): string {
  if (!value) return '—';
  const d = typeof value === 'string' ? new Date(value) : value;
  return idIDDateLong.format(d);
}

export function dateTime(value: string | Date | null | undefined): string {
  if (!value) return '—';
  const d = typeof value === 'string' ? new Date(value) : value;
  return idIDDateTime.format(d);
}

/** Truncate a long string with an ellipsis. */
export function truncate(s: string, n = 50): string {
  if (!s) return '';
  return s.length > n ? s.slice(0, n) + '…' : s;
}
