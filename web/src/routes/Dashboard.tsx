import { useEffect, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import { motion } from 'framer-motion';
import {
  Receipt, Wallet, ShoppingBag, ArrowUpRight, Activity, Plus, Scale, Sparkles,
  Users, Package,
} from 'lucide-react';
import { Link } from '@tanstack/react-router';
import { PageHeader } from '@/components/PageHeader';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { StatusPill, DocstatusPill } from '@/components/StatusPill';
import { Kbd } from '@/components/Kbd';
import { Skeleton } from '@/components/EmptyState';
import { api } from '@/lib/api';
import { money, date } from '@/lib/format';
import { me } from '@/lib/auth';
import { useUI } from '@/store/ui';
import { cn } from '@/lib/cn';
import { AgentDraftsCard } from '@/components/AgentDraftsCard';

interface AgeingReport { totals: { total_outstanding: string } }
interface BalanceSheet { assets: { account_name: string; amount: string }[]; balanced: boolean }
interface ListWrap<T> { items: T[] }
interface SI { id: string; name: string; posting_date: string; grand_total: string; docstatus: number; customer_id: string }
interface PI { id: string; name: string; posting_date: string; grand_total: string; docstatus: number; supplier_id: string }
interface Customer { id: string; display_name: string }
interface Supplier { id: string; display_name: string }

const todayISO = () => new Date().toISOString().slice(0, 10);

export function DashboardPage() {
  const [name, setName] = useState<string | null>(null);
  const togglePalette = useUI((s) => s.togglePalette);

  useEffect(() => {
    me().then((u) => setName(u.full_name || u.email.split('@')[0]!)).catch(() => setName(null));
  }, []);

  const today = todayISO();
  const arQ = useQuery({ queryKey: ['report','ar',today], queryFn: () => api<AgeingReport>(`/accounting/reports/accounts-receivable-ageing?as_of=${today}`) });
  const apQ = useQuery({ queryKey: ['report','ap',today], queryFn: () => api<AgeingReport>(`/accounting/reports/accounts-payable-ageing?as_of=${today}`) });
  const bsQ = useQuery({ queryKey: ['report','bs',today], queryFn: () => api<BalanceSheet>(`/accounting/reports/balance-sheet?as_of=${today}`) });
  const siQ = useQuery({ queryKey: ['si-recent'], queryFn: () => api<ListWrap<SI>>('/accounting/sales-invoices') });
  const piQ = useQuery({ queryKey: ['pi-recent'], queryFn: () => api<ListWrap<PI>>('/accounting/purchase-invoices') });
  const cuQ = useQuery({ queryKey: ['customers'], queryFn: () => api<ListWrap<Customer>>('/accounting/customers') });
  const suQ = useQuery({ queryKey: ['suppliers'], queryFn: () => api<ListWrap<Supplier>>('/accounting/suppliers') });

  const arOut   = arQ.data?.totals.total_outstanding ?? '0';
  const apOut   = apQ.data?.totals.total_outstanding ?? '0';
  const cashBal = (bsQ.data?.assets ?? [])
    .filter((a) => /kas|cash|bank/i.test(a.account_name))
    .reduce((acc, a) => acc + Number(a.amount), 0).toString();

  const customerName = (id: string) => cuQ.data?.items.find((c) => c.id === id)?.display_name ?? id;
  const supplierName = (id: string) => suQ.data?.items.find((s) => s.id === id)?.display_name ?? id;

  const draftSI = (siQ.data?.items ?? []).filter((i) => i.docstatus === 0).slice(0, 3);
  const draftPI = (piQ.data?.items ?? []).filter((i) => i.docstatus === 0).slice(0, 2);
  const recentSubmitted = [
    ...(siQ.data?.items ?? []).filter((i) => i.docstatus === 1).slice(0, 3).map((i) => ({
      kind: 'SI' as const, name: i.name, when: i.posting_date, who: customerName(i.customer_id), amount: i.grand_total,
    })),
    ...(piQ.data?.items ?? []).filter((i) => i.docstatus === 1).slice(0, 3).map((i) => ({
      kind: 'PI' as const, name: i.name, when: i.posting_date, who: supplierName(i.supplier_id), amount: i.grand_total,
    })),
  ].sort((a, b) => b.when.localeCompare(a.when)).slice(0, 5);

  const stats = [
    { label: 'Outstanding AR', value: money(arOut),                       icon: Receipt,     link: '/accounting/reports/ar-ageing',       loading: arQ.isLoading },
    { label: 'Outstanding AP', value: money(apOut),                       icon: ShoppingBag, link: '/accounting/reports/ap-ageing',       loading: apQ.isLoading },
    { label: 'Cash on hand',   value: money(cashBal),                     icon: Wallet,      link: '/accounting/reports/balance-sheet',   loading: bsQ.isLoading },
    { label: 'Customers',      value: cuQ.data?.items.length ?? '—',      icon: Users,       link: '/accounting/customers',                loading: cuQ.isLoading },
  ];

  return (
    <>
      <PageHeader
        title={<span>{name ? <>Hello, <span className="text-ink">{name}</span> 👋</> : 'Hello 👋'}</span>}
        subtitle="Here's what's happening in your workspace today."
        actions={
          <>
            <Button variant="secondary" onClick={togglePalette}>
              <span>Quick actions</span><Kbd>⌘K</Kbd>
            </Button>
            <Button asChild>
              <Link to={'/accounting/sales-invoices/new' as never}><Plus className="size-4" /> New Invoice</Link>
            </Button>
          </>
        }
      />

      <div className="flex-1 px-6 lg:px-8 pt-6 pb-8 space-y-6">

        <AgentDraftsCard />

        {/* KPI cards — neutral surface tiles, mint only on hover state */}
        <motion.div
          initial="hidden" animate="visible"
          variants={{ visible: { transition: { staggerChildren: 0.04 } } }}
          className="grid grid-cols-1 sm:grid-cols-2 lg:grid-cols-4 gap-4"
        >
          {stats.map((s) => (
            <motion.div key={s.label} variants={{ hidden: { opacity: 0, y: 8 }, visible: { opacity: 1, y: 0 } }}>
              <Link to={s.link as never} className="block group">
                <Card className="hover:border-stone/40 transition-colors cursor-pointer">
                  <div className="flex items-start justify-between mb-3">
                    <div className="inline-flex size-9 items-center justify-center rounded-lg bg-surface text-ink group-hover:text-brand-green-deep transition-colors">
                      <s.icon className="size-4" />
                    </div>
                    <ArrowUpRight className="size-3.5 text-stone group-hover:text-ink transition-colors" />
                  </div>
                  <div className="text-caption text-stone">{s.label}</div>
                  <div className="mt-1 text-heading-3 num text-ink">
                    {s.loading ? <Skeleton className="h-7 w-32 mt-1" /> : s.value}
                  </div>
                </Card>
              </Link>
            </motion.div>
          ))}
        </motion.div>

        {/* Two-column: drafts / recent activity */}
        <div className="grid grid-cols-1 lg:grid-cols-3 gap-4">
          <Card className="lg:col-span-2">
            <div className="flex items-start justify-between mb-4">
              <div>
                <CardTitle>Drafts waiting on you</CardTitle>
                <CardDescription>Sales and purchase invoices not yet submitted.</CardDescription>
              </div>
              <Button variant="ghost" size="sm" asChild>
                <Link to={'/accounting/sales-invoices' as never}>All invoices <ArrowUpRight className="size-3.5" /></Link>
              </Button>
            </div>
            {(draftSI.length + draftPI.length) === 0 ? (
              <div className="text-center py-10 text-stone">
                <Sparkles className="mx-auto size-6 text-brand-green mb-2" />
                <div className="text-body-sm">No drafts — clean inbox.</div>
              </div>
            ) : (
              <ul className="divide-y divide-hairline -mx-5">
                {[
                  ...draftSI.map((i) => ({ kind: 'SI' as const, doc: i, who: customerName(i.customer_id) })),
                  ...draftPI.map((i) => ({ kind: 'PI' as const, doc: i, who: supplierName(i.supplier_id) })),
                ].map((row) => (
                  <li key={row.doc.id} className="px-5 py-2.5 flex items-center gap-3 hover:bg-surface-soft transition-colors">
                    <Link
                      to={`${row.kind === 'SI' ? '/accounting/sales-invoices' : '/accounting/purchase-invoices'}/${row.doc.id}` as never}
                      className="flex items-center gap-3 w-full"
                    >
                      <div className="size-8 rounded-md bg-surface inline-flex items-center justify-center text-steel">
                        {row.kind === 'SI' ? <Receipt className="size-4" /> : <ShoppingBag className="size-4" />}
                      </div>
                      <div className="min-w-0 flex-1">
                        <div className="font-medium text-ink">{row.doc.name}</div>
                        <div className="text-caption text-stone truncate">{row.who}</div>
                      </div>
                      <div className="text-body-sm num text-ink text-right">{money(row.doc.grand_total)}</div>
                      <DocstatusPill docstatus={row.doc.docstatus} />
                    </Link>
                  </li>
                ))}
              </ul>
            )}
          </Card>

          <Card>
            <div className="flex items-start justify-between mb-4">
              <div>
                <CardTitle>Recent activity</CardTitle>
                <CardDescription>Submitted documents.</CardDescription>
              </div>
              <Activity className="size-4 text-stone" />
            </div>
            {recentSubmitted.length === 0 ? (
              <div className="text-center py-8 text-stone text-body-sm">No recent submissions.</div>
            ) : (
              <ol className="space-y-3">
                {recentSubmitted.map((row, i) => (
                  <li key={i} className="flex gap-3">
                    <div className="relative flex flex-col items-center">
                      <div className="size-2 rounded-full bg-brand-green mt-1.5" />
                      {i < recentSubmitted.length - 1 && <div className="absolute top-3 bottom-[-12px] w-px bg-hairline" />}
                    </div>
                    <div className="flex-1 pb-1 min-w-0">
                      <div className="text-body-sm text-ink truncate">
                        <span className="font-medium">{row.name}</span>
                        <span className="text-slate"> · {row.who}</span>
                      </div>
                      <div className="text-caption text-stone flex items-center gap-2">
                        <span>{date(row.when)}</span>
                        <span className="num">{money(row.amount)}</span>
                      </div>
                    </div>
                  </li>
                ))}
              </ol>
            )}
          </Card>
        </div>

        {/* Quick links */}
        <div className="grid grid-cols-2 lg:grid-cols-4 gap-4">
          {[
            { to: '/accounting/sales-invoices/new', icon: Receipt, label: 'New Sales Invoice' },
            { to: '/accounting/journal-entries/new', icon: Scale,   label: 'New Journal Entry' },
            { to: '/accounting/reports/trial-balance', icon: Scale, label: 'Trial Balance' },
            { to: '/accounting/items', icon: Package,               label: 'Items' },
          ].map((q) => (
            <Link key={q.to} to={q.to as never}
              className="bg-canvas border border-hairline rounded-lg p-4 hover:border-stone/40 transition-colors group block">
              <div className="flex items-center gap-3">
                <div className="size-9 rounded-lg bg-surface text-ink inline-flex items-center justify-center group-hover:bg-primary group-hover:text-primary-fg transition-colors">
                  <q.icon className="size-4" />
                </div>
                <span className="text-body-sm text-ink font-medium">{q.label}</span>
                <ArrowUpRight className="ml-auto size-3.5 text-stone group-hover:text-ink" />
              </div>
            </Link>
          ))}
        </div>

        {/* Books health */}
        {bsQ.data && (
          <Card className={cn('!p-4 flex items-center justify-between',
            bsQ.data.balanced ? 'bg-success/5 border-success/30' : 'bg-danger/5 border-danger/30')}>
            <div className="flex items-center gap-3">
              <Scale className={cn('size-5', bsQ.data.balanced ? 'text-success' : 'text-danger')} />
              <div>
                <div className="text-body-sm font-medium text-ink">Books health</div>
                <div className="text-caption text-stone">Assets = Liabilities + Equity (Period Net Profit included)</div>
              </div>
            </div>
            {bsQ.data.balanced ? <StatusPill tone="success">Balanced</StatusPill> : <StatusPill tone="danger">IMBALANCED</StatusPill>}
          </Card>
        )}
      </div>
    </>
  );
}
