import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import {
  BarChart3, LineChart, Scale, TrendingUp, Wallet, FileSpreadsheet, Banknote,
} from 'lucide-react';
import { useParams, useNavigate } from '@tanstack/react-router';
import { PageHeader } from '@/components/PageHeader';
import { Card, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { Input } from '@/components/Input';
import { StatusPill } from '@/components/StatusPill';
import { Skeleton } from '@/components/EmptyState';
import { api } from '@/lib/api';
import { money } from '@/lib/format';
import { cn } from '@/lib/cn';

interface ReportKind {
  slug: string; label: string; icon: typeof BarChart3;
  endpoint: (params: ReportParams) => string;
  body: (data: any) => React.ReactNode;
  query: 'period' | 'asof';
}

interface ReportParams { from: string; to: string; asof: string; companyId?: string; }

const todayISO = () => new Date().toISOString().slice(0, 10);
const yearStartISO = () => `${new Date().getFullYear()}-01-01`;

// ============================================================
// Report renderers
// ============================================================
function TrialBalance({ data }: { data: any }) {
  return (
    <ReportTable
      head={['Account', 'Type', 'Opening Dr', 'Opening Cr', 'Period Dr', 'Period Cr', 'Closing Dr', 'Closing Cr']}
      rows={(data.rows ?? []).map((r: any) => [
        <span className="text-text-primary">{r.account_name}</span>,
        <StatusPill tone="neutral" withDot={false}>{r.root_type}</StatusPill>,
        moneyOrDash(r.opening_debit),
        moneyOrDash(r.opening_credit),
        moneyOrDash(r.period_debit),
        moneyOrDash(r.period_credit),
        moneyOrDash(r.closing_debit),
        moneyOrDash(r.closing_credit),
      ])}
      foot={[
        'Totals', '',
        moneyOrDash(data.totals?.opening_debit),  moneyOrDash(data.totals?.opening_credit),
        moneyOrDash(data.totals?.period_debit),   moneyOrDash(data.totals?.period_credit),
        moneyOrDash(data.totals?.closing_debit),  moneyOrDash(data.totals?.closing_credit),
      ]}
      balanced={data.balanced}
    />
  );
}

function ProfitAndLoss({ data }: { data: any }) {
  return (
    <div className="space-y-4">
      <Subtable title="Income"  rows={data.income ?? []} total={money(data.total_income)} tone="success" />
      <Subtable title="Expense" rows={data.expense ?? []} total={money(data.total_expense)} tone="danger" />
      <Card className="!p-4 flex items-center justify-between">
        <CardTitle>Net profit</CardTitle>
        <div className={cn(
          'text-page-title num font-semibold',
          Number(data.net_profit) >= 0 ? 'text-success' : 'text-danger',
        )}>
          {money(data.net_profit)}
        </div>
      </Card>
    </div>
  );
}

function BalanceSheet({ data }: { data: any }) {
  return (
    <div className="space-y-4">
      <Subtable title="Assets" rows={data.assets ?? []} total={money(data.total_assets)} tone="info" />
      <Subtable title="Liabilities" rows={data.liabilities ?? []} total={money(data.total_liabilities)} tone="warning" />
      <Subtable
        title="Equity"
        rows={[
          ...(data.equity ?? []),
          { account_name: 'Period Net Profit', amount: data.period_net_profit },
        ]}
        total={money(data.total_equity)}
        tone="accent"
      />
      <Card className={cn(
        '!p-4 flex items-center justify-between',
        data.balanced ? 'bg-success/5 border-success/30' : 'bg-danger/5 border-danger/30',
      )}>
        <span className="text-body font-medium">Assets = Liabilities + Equity</span>
        <span>{data.balanced ? <StatusPill tone="success">Balanced</StatusPill> : <StatusPill tone="danger">IMBALANCED</StatusPill>}</span>
      </Card>
    </div>
  );
}

function AgeingReport({ data }: { data: any }) {
  return (
    <ReportTable
      head={['Party', 'Current', '0–30', '31–60', '61–90', '90+', 'Total']}
      rows={(data.rows ?? []).map((r: any) => [
        <span className="text-text-primary">{r.party_name}</span>,
        moneyOrDash(r.current),
        moneyOrDash(r.d_0_30),
        moneyOrDash(r.d_31_60),
        moneyOrDash(r.d_61_90),
        moneyOrDash(r.d_90_plus),
        <span className="font-medium">{money(r.total_outstanding)}</span>,
      ])}
      foot={[
        'Totals',
        moneyOrDash(data.totals?.current),
        moneyOrDash(data.totals?.d_0_30),
        moneyOrDash(data.totals?.d_31_60),
        moneyOrDash(data.totals?.d_61_90),
        moneyOrDash(data.totals?.d_90_plus),
        moneyOrDash(data.totals?.total_outstanding),
      ]}
    />
  );
}

function CashFlow({ data }: { data: any }) {
  const sections = [
    { name: 'Operating',  ...data.operating },
    { name: 'Investing',  ...data.investing },
    { name: 'Financing',  ...data.financing },
  ];
  return (
    <div className="space-y-4">
      <Card className="!p-4">
        <CardTitle>Cash position</CardTitle>
        <div className="mt-2 grid grid-cols-3 gap-4 text-body">
          <div><div className="text-text-secondary">Opening</div><div className="text-section-head num">{money(data.opening_cash)}</div></div>
          <div><div className="text-text-secondary">Net change</div><div className={cn('text-section-head num', Number(data.net_change) >= 0 ? 'text-success' : 'text-danger')}>{money(data.net_change)}</div></div>
          <div><div className="text-text-secondary">Closing</div><div className="text-section-head num">{money(data.closing_cash)}</div></div>
        </div>
      </Card>
      <ReportTable
        head={['Section', 'Inflow', 'Outflow', 'Net']}
        rows={sections.map((s) => [
          <span className="text-text-primary">{s.name}</span>,
          moneyOrDash(s.inflow),
          moneyOrDash(s.outflow),
          <span className={cn('num font-medium', Number(s.net ?? 0) >= 0 ? 'text-success' : 'text-danger')}>{money(s.net)}</span>,
        ])}
      />
    </div>
  );
}

function PPNSummary({ data }: { data: any }) {
  return (
    <div className="space-y-4">
      <div className="grid grid-cols-3 gap-3">
        <Card className="!p-4"><div className="text-text-secondary text-caption">Output VAT (Keluaran)</div><div className="text-page-title num text-success">{money(data.total_output_vat)}</div></Card>
        <Card className="!p-4"><div className="text-text-secondary text-caption">Input VAT (Masukan)</div><div className="text-page-title num text-info">{money(data.total_input_vat)}</div></Card>
        <Card className="!p-4"><div className="text-text-secondary text-caption">Net payable</div><div className={cn('text-page-title num font-semibold', Number(data.net_payable) >= 0 ? 'text-danger' : 'text-success')}>{money(data.net_payable)}</div></Card>
      </div>
      <Subtable title="Output VAT accounts" rows={data.output_vat ?? []} total={money(data.total_output_vat)} tone="success" />
      <Subtable title="Input VAT accounts"  rows={data.input_vat ?? []}  total={money(data.total_input_vat)}  tone="info" />
    </div>
  );
}

const reports: ReportKind[] = [
  { slug: 'trial-balance',  label: 'Trial Balance',   icon: Scale,
    endpoint: (p) => `/accounting/reports/trial-balance?from_date=${p.from}&to_date=${p.to}`,
    body: (d) => <TrialBalance data={d} />, query: 'period' },
  { slug: 'profit-and-loss',label: 'Profit & Loss',   icon: TrendingUp,
    endpoint: (p) => `/accounting/reports/profit-and-loss?from_date=${p.from}&to_date=${p.to}`,
    body: (d) => <ProfitAndLoss data={d} />, query: 'period' },
  { slug: 'balance-sheet',  label: 'Balance Sheet',   icon: BarChart3,
    endpoint: (p) => `/accounting/reports/balance-sheet?as_of=${p.asof}`,
    body: (d) => <BalanceSheet data={d} />, query: 'asof' },
  { slug: 'cash-flow',      label: 'Cash Flow',       icon: Wallet,
    endpoint: (p) => `/accounting/reports/cash-flow?from_date=${p.from}&to_date=${p.to}`,
    body: (d) => <CashFlow data={d} />, query: 'period' },
  { slug: 'ar-ageing',      label: 'AR Ageing',       icon: LineChart,
    endpoint: (p) => `/accounting/reports/accounts-receivable-ageing?as_of=${p.asof}`,
    body: (d) => <AgeingReport data={d} />, query: 'asof' },
  { slug: 'ap-ageing',      label: 'AP Ageing',       icon: Banknote,
    endpoint: (p) => `/accounting/reports/accounts-payable-ageing?as_of=${p.asof}`,
    body: (d) => <AgeingReport data={d} />, query: 'asof' },
  { slug: 'ppn-summary',    label: 'PPN Summary',     icon: FileSpreadsheet,
    endpoint: (p) => `/accounting/reports/ppn-summary?from_date=${p.from}&to_date=${p.to}`,
    body: (d) => <PPNSummary data={d} />, query: 'period' },
];

export function ReportsPage() {
  const params = useParams({ strict: false }) as { slug?: string };
  const navigate = useNavigate();
  const slug = params.slug ?? 'trial-balance';
  const report = reports.find((r) => r.slug === slug) ?? reports[0]!;

  const [from, setFrom] = useState(yearStartISO());
  const [to, setTo] = useState(todayISO());
  const [asof, setAsof] = useState(todayISO());

  const reportParams = { from, to, asof };
  const { data, isLoading, isError, error } = useQuery({
    queryKey: ['report', report.slug, reportParams],
    queryFn: () => api<any>(report.endpoint(reportParams)),
  });

  return (
    <>
      <PageHeader
        crumbs={[{ label: 'Finance', to: '/accounting' }, { label: 'Reports' }]}
        title={report.label}
        subtitle="Derived from gl_entry — the source of truth for the books."
        actions={null}
      />

      <div className="flex-1 px-6 lg:px-8 pb-8 space-y-4 max-w-[1400px]">
        {/* Report switcher */}
        <Card padded={false}>
          <div className="flex flex-wrap items-center gap-1.5 p-3 border-b border-border">
            {reports.map((r) => {
              const Icon = r.icon;
              const active = r.slug === report.slug;
              return (
                <button
                  key={r.slug}
                  onClick={() => navigate({ to: `/accounting/reports/${r.slug}` as never })}
                  className={cn(
                    'inline-flex items-center gap-2 px-3 h-8 rounded-md text-body transition-colors',
                    active ? 'bg-accent-soft text-accent font-medium' : 'text-text-secondary hover:bg-bg-subtle hover:text-text-primary',
                  )}
                >
                  <Icon className="size-4" /> {r.label}
                </button>
              );
            })}
          </div>
          <div className="flex flex-wrap items-end gap-3 p-3">
            {report.query === 'period' ? (
              <>
                <div>
                  <div className="text-caption text-text-secondary mb-1">From</div>
                  <Input type="date" value={from} onChange={(e) => setFrom(e.target.value)} />
                </div>
                <div>
                  <div className="text-caption text-text-secondary mb-1">To</div>
                  <Input type="date" value={to} onChange={(e) => setTo(e.target.value)} />
                </div>
              </>
            ) : (
              <div>
                <div className="text-caption text-text-secondary mb-1">As of</div>
                <Input type="date" value={asof} onChange={(e) => setAsof(e.target.value)} />
              </div>
            )}
            <div className="flex gap-2 ml-auto">
              <Button variant="secondary">Print</Button>
              <Button variant="secondary">Export CSV</Button>
            </div>
          </div>
        </Card>

        {/* Body */}
        {isLoading ? (
          <Card><div className="space-y-3"><Skeleton className="h-4 w-1/4" /><Skeleton className="h-3 w-3/4" /><Skeleton className="h-3 w-3/4" /><Skeleton className="h-3 w-2/3" /></div></Card>
        ) : isError ? (
          <Card className="!p-6 text-danger">
            Failed to load: {(error as any)?.message ?? 'unknown error'}
          </Card>
        ) : (
          report.body(data)
        )}
      </div>
    </>
  );
}

// ---------- helpers ----------
function moneyOrDash(v: string | number | null | undefined) {
  if (v === null || v === undefined || v === '' || Number(v) === 0) return <span className="text-text-tertiary">—</span>;
  return <span className="num">{money(v)}</span>;
}

function ReportTable({ head, rows, foot, balanced }: { head: React.ReactNode[]; rows: React.ReactNode[][]; foot?: React.ReactNode[]; balanced?: boolean }) {
  return (
    <Card padded={false}>
      <div className="overflow-x-auto">
        <table className="w-full text-dense">
          <thead className="bg-bg-subtle/50 border-b border-border">
            <tr>
              {head.map((h, i) => (
                <th key={i} className={cn(
                  'px-4 py-2.5 font-medium text-text-secondary',
                  i === 0 ? 'text-left' : 'text-right',
                )}>{h}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r, ri) => (
              <tr key={ri} className="border-b border-border last:border-0 hover:bg-bg-subtle/40">
                {r.map((c, ci) => (
                  <td key={ci} className={cn('px-4 py-2', ci === 0 ? 'text-left' : 'text-right')}>{c}</td>
                ))}
              </tr>
            ))}
          </tbody>
          {foot && (
            <tfoot className="bg-bg-subtle border-t border-border-strong font-medium">
              <tr>
                {foot.map((f, i) => (
                  <td key={i} className={cn('px-4 py-2.5', i === 0 ? 'text-left' : 'text-right num')}>{f}</td>
                ))}
              </tr>
            </tfoot>
          )}
        </table>
      </div>
      {typeof balanced === 'boolean' && (
        <div className={cn(
          'px-4 py-2 border-t flex justify-end items-center gap-2',
          balanced ? 'border-success/30 bg-success/5' : 'border-danger/30 bg-danger/5',
        )}>
          {balanced ? <StatusPill tone="success">Balanced</StatusPill> : <StatusPill tone="danger">IMBALANCED</StatusPill>}
        </div>
      )}
    </Card>
  );
}

function Subtable({ title, rows, total, tone }: {
  title: string; rows: { account_name?: string; name?: string; amount: string | number }[]; total: string;
  tone: 'success' | 'danger' | 'info' | 'warning' | 'accent';
}) {
  if (!rows || rows.length === 0) {
    return (
      <Card>
        <CardTitle>{title}</CardTitle>
        <p className="mt-2 text-body text-text-tertiary">No activity in this period.</p>
      </Card>
    );
  }
  return (
    <Card padded={false}>
      <div className="flex items-center justify-between p-4 pb-3 border-b border-border">
        <CardTitle>{title}</CardTitle>
        <div className="text-section-head font-semibold num">{total}</div>
      </div>
      <ul className="divide-y divide-border">
        {rows.map((r, i) => (
          <li key={i} className="px-4 py-2 flex justify-between hover:bg-bg-subtle/40">
            <span className="text-text-primary">{r.account_name ?? r.name}</span>
            <span className="num font-medium">{money(r.amount)}</span>
          </li>
        ))}
      </ul>
      <_StatusBorder tone={tone} />
    </Card>
  );
}
function _StatusBorder({ tone }: { tone: 'success' | 'danger' | 'info' | 'warning' | 'accent' }) {
  // Cosmetic bottom strip in the section's tone.
  const cls = { success: 'bg-success/40', danger: 'bg-danger/40', info: 'bg-info/40', warning: 'bg-warning/40', accent: 'bg-accent/40' }[tone];
  return <div className={cn('h-0.5', cls)} />;
}
