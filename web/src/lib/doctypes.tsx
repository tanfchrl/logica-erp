import type { ColumnDef } from '@tanstack/react-table';
import type { LucideIcon } from 'lucide-react';
import {
  Receipt, ShoppingBag, Wallet, FileText, Users, Building2, Package, Warehouse,
  PiggyBank, Tag, UserSquare, Briefcase, Settings, Factory, Wrench, ClipboardList,
  Headphones, Sparkles, ShoppingCart, BarChart3,
} from 'lucide-react';
import { money, date } from './format';
import { DocstatusPill, StatusPill } from '@/components/StatusPill';

/**
 * DoctypeConfig — the minimal record a generic ListView needs to render any doctype.
 * Per the brief §3.1 — metadata-driven where useful, hand-built where UX matters.
 */
export interface DoctypeConfig<T = any> {
  // routing + UI labels
  slug: string;                 // URL path under the module, e.g. 'sales-invoices'
  modulePath: string;           // module URL path, e.g. '/accounting'
  module: string;               // human module name
  doctype: string;              // server doctype, e.g. 'sales_invoice'
  title: string;                // plural display
  singular: string;             // singular display
  icon: LucideIcon;

  // server
  endpoint: string;             // GET-list endpoint relative to /api/v1, e.g. '/accounting/sales-invoices'

  // list view
  columns: ColumnDef<T>[];
  newPath?: string;             // optional: override "new" link path (defaults to {modulePath}/{slug}/new)
  hasNew?: boolean;             // show "New" toolbar action (default true)
}

// ---- column factories shared across doctypes ----

const codeCol = (key: string, header = 'Code'): ColumnDef<any> => ({
  accessorKey: key,
  header,
  cell: (info) => <span className="font-mono text-dense font-medium text-text-primary">{info.getValue<string>()}</span>,
});

const nameCol = (key = 'name', header = 'Name', icon?: LucideIcon): ColumnDef<any> => ({
  accessorKey: key,
  header,
  cell: (info) => (
    <div className="flex items-center gap-2.5 min-w-0">
      {icon ? (
        <div className="size-7 rounded-md bg-accent-soft text-accent inline-flex items-center justify-center shrink-0">
          {(() => { const I = icon; return <I className="size-3.5" />; })()}
        </div>
      ) : null}
      <span className="text-text-primary truncate">{info.getValue<string>()}</span>
    </div>
  ),
});

const dateCol = (key: string, header = 'Date'): ColumnDef<any> => ({
  accessorKey: key,
  header,
  cell: (info) => <span className="text-text-secondary">{date(info.getValue<string>())}</span>,
});

const moneyCol = (key: string, header: string): ColumnDef<any> => ({
  accessorKey: key,
  header,
  meta: { align: 'right' },
  cell: (info) => <span className="font-medium num">{money(info.getValue<string>())}</span>,
});

const docstatusCol: ColumnDef<any> = {
  accessorKey: 'docstatus',
  header: 'Status',
  cell: (info) => <DocstatusPill docstatus={info.getValue<number>()} />,
};

const partyCol = (key: string, header: string): ColumnDef<any> => ({
  accessorKey: key,
  header,
  cell: (info) => <span className="text-text-primary">{info.getValue<string>() || '—'}</span>,
});

// ---- configs ----

const items: DoctypeConfig = {
  slug: 'items', modulePath: '/accounting', module: 'Finance',
  doctype: 'item', title: 'Items', singular: 'Item', icon: Package,
  endpoint: '/accounting/items',
  columns: [
    codeCol('code'),
    nameCol('name', 'Name', Package),
    { accessorKey: 'stock_uom', header: 'UOM', cell: (i) => <span className="text-text-secondary">{i.getValue<string>()}</span> },
    {
      id: 'flags', header: 'Flags',
      cell: (i) => (
        <div className="flex items-center gap-1">
          {i.row.original.is_stock_item    && <StatusPill tone="info" withDot={false}>Stock</StatusPill>}
          {i.row.original.is_sales_item    && <StatusPill tone="success" withDot={false}>Sales</StatusPill>}
          {i.row.original.is_purchase_item && <StatusPill tone="accent" withDot={false}>Purchase</StatusPill>}
        </div>
      ),
    },
    moneyCol('standard_rate', 'Standard rate'),
  ],
};

const customers: DoctypeConfig = {
  slug: 'customers', modulePath: '/accounting', module: 'Finance',
  doctype: 'customer', title: 'Customers', singular: 'Customer', icon: Users,
  endpoint: '/accounting/customers',
  columns: [
    codeCol('name'),
    nameCol('display_name', 'Customer', Users),
    { accessorKey: 'npwp', header: 'NPWP', cell: (i) => <span className="font-mono text-dense text-text-secondary">{i.getValue<string>() || '—'}</span> },
    { accessorKey: 'is_individual', header: 'Type', cell: (i) => <StatusPill tone="neutral" withDot={false}>{i.getValue<boolean>() ? 'Individual' : 'Entity'}</StatusPill> },
    { accessorKey: 'email', header: 'Email', cell: (i) => <span className="text-text-secondary">{i.getValue<string>() || '—'}</span> },
  ],
};

const suppliers: DoctypeConfig = {
  slug: 'suppliers', modulePath: '/accounting', module: 'Finance',
  doctype: 'supplier', title: 'Suppliers', singular: 'Supplier', icon: Building2,
  endpoint: '/accounting/suppliers',
  columns: [
    codeCol('name'),
    nameCol('display_name', 'Supplier', Building2),
    { accessorKey: 'npwp', header: 'NPWP', cell: (i) => <span className="font-mono text-dense text-text-secondary">{i.getValue<string>() || '—'}</span> },
    { accessorKey: 'is_individual', header: 'Type', cell: (i) => <StatusPill tone="neutral" withDot={false}>{i.getValue<boolean>() ? 'Individual' : 'Entity'}</StatusPill> },
    { accessorKey: 'email', header: 'Email', cell: (i) => <span className="text-text-secondary">{i.getValue<string>() || '—'}</span> },
  ],
};

const taxTemplates: DoctypeConfig = {
  slug: 'tax-templates', modulePath: '/accounting', module: 'Finance',
  doctype: 'tax_template', title: 'Tax Templates', singular: 'Tax Template', icon: Tag,
  endpoint: '/accounting/tax-templates',
  columns: [
    nameCol('name', 'Name', Tag),
    { accessorKey: 'is_sales', header: 'Side', cell: (i) =>
        i.getValue<boolean>() ? <StatusPill tone="success" withDot={false}>Sales</StatusPill> : <StatusPill tone="info" withDot={false}>Purchase</StatusPill> },
    { accessorKey: 'is_default', header: 'Default', cell: (i) => i.getValue<boolean>() ? <StatusPill tone="accent" withDot={false}>Default</StatusPill> : <span className="text-text-tertiary">—</span> },
    dateCol('created_at', 'Created'),
  ],
};

const accounts: DoctypeConfig = {
  slug: 'accounts', modulePath: '/accounting', module: 'Finance',
  doctype: 'account', title: 'Chart of Accounts', singular: 'Account', icon: PiggyBank,
  endpoint: '/accounting/accounts',
  columns: [
    codeCol('account_number', 'No.'),
    nameCol('name', 'Account', PiggyBank),
    { accessorKey: 'root_type', header: 'Root', cell: (i) => <StatusPill tone="neutral" withDot={false}>{i.getValue<string>()}</StatusPill> },
    { accessorKey: 'account_type', header: 'Type', cell: (i) => <span className="text-text-secondary">{i.getValue<string>() || '—'}</span> },
    { accessorKey: 'account_currency', header: 'Currency', cell: (i) => <span className="font-mono text-dense text-text-secondary">{i.getValue<string>()}</span> },
  ],
};

const salesInvoices: DoctypeConfig = {
  slug: 'sales-invoices', modulePath: '/accounting', module: 'Finance',
  doctype: 'sales_invoice', title: 'Sales Invoices', singular: 'Sales Invoice', icon: Receipt,
  endpoint: '/accounting/sales-invoices',
  columns: [
    codeCol('name', 'No.'),
    dateCol('posting_date', 'Date'),
    partyCol('customer_id', 'Customer'),
    moneyCol('grand_total', 'Grand total'),
    moneyCol('outstanding_amount', 'Outstanding'),
    docstatusCol,
  ],
};

// PO status pill — ERPNext-style "what to do next" labels so the list view
// reads as a worklist, not just a doctype dump.
function POStatusPill({ value }: { value: string }) {
  switch (value) {
    case 'Draft':                return <StatusPill tone="neutral" withDot={false}>Draft</StatusPill>;
    case 'To Receive and Bill':  return <StatusPill tone="warning" withDot={false}>To Receive &amp; Bill</StatusPill>;
    case 'To Receive':           return <StatusPill tone="info"    withDot={false}>To Receive</StatusPill>;
    case 'To Bill':              return <StatusPill tone="info"    withDot={false}>To Bill</StatusPill>;
    case 'Completed':            return <StatusPill tone="success" withDot={false}>Completed</StatusPill>;
    case 'On Hold':              return <StatusPill tone="warning" withDot={false}>On Hold</StatusPill>;
    case 'Closed':               return <StatusPill tone="neutral" withDot={false}>Closed</StatusPill>;
    case 'Stopped':              return <StatusPill tone="danger"  withDot={false}>Stopped</StatusPill>;
    case 'Cancelled':            return <StatusPill tone="danger"  withDot={false}>Cancelled</StatusPill>;
    default:                     return <StatusPill tone="neutral" withDot={false}>{value || '—'}</StatusPill>;
  }
}

function GRNStatusPill({ value }: { value: string }) {
  switch (value) {
    case 'Draft':         return <StatusPill tone="neutral" withDot={false}>Draft</StatusPill>;
    case 'To Bill':       return <StatusPill tone="warning" withDot={false}>To Bill</StatusPill>;
    case 'Completed':     return <StatusPill tone="success" withDot={false}>Completed</StatusPill>;
    case 'Return Issued': return <StatusPill tone="info"    withDot={false}>Return Issued</StatusPill>;
    case 'Cancelled':     return <StatusPill tone="danger"  withDot={false}>Cancelled</StatusPill>;
    default:              return <StatusPill tone="neutral" withDot={false}>{value || '—'}</StatusPill>;
  }
}

const purchaseReceipts: DoctypeConfig = {
  slug: 'purchase-receipts', modulePath: '/stock', module: 'Stock',
  doctype: 'purchase_receipt', title: 'Purchase Receipts (GRN)', singular: 'Purchase Receipt', icon: Package,
  endpoint: '/stock/purchase-receipts',
  columns: [
    codeCol('name', 'No.'),
    dateCol('posting_date', 'Received'),
    partyCol('supplier_id', 'Supplier'),
    { accessorKey: 'against_purchase_order_id', header: 'PO', cell: (i) => {
        const v = i.getValue<string>();
        return v ? <span className="font-mono text-caption text-text-secondary">{v.slice(-8)}</span> : <span className="text-text-tertiary">—</span>;
    } },
    moneyCol('total_value', 'Value'),
    { accessorKey: 'status', header: 'Status', cell: (i) => <GRNStatusPill value={i.getValue<string>()} /> },
  ],
};

function MRStatusPill({ value }: { value: string }) {
  switch (value) {
    case 'Draft':              return <StatusPill tone="neutral" withDot={false}>Draft</StatusPill>;
    case 'Pending':            return <StatusPill tone="warning" withDot={false}>Pending</StatusPill>;
    case 'Partially Ordered':  return <StatusPill tone="info"    withDot={false}>Partially</StatusPill>;
    case 'Ordered':            return <StatusPill tone="info"    withDot={false}>Ordered</StatusPill>;
    case 'Issued':             return <StatusPill tone="success" withDot={false}>Issued</StatusPill>;
    case 'Transferred':        return <StatusPill tone="success" withDot={false}>Transferred</StatusPill>;
    case 'Received':           return <StatusPill tone="success" withDot={false}>Received</StatusPill>;
    case 'Stopped':            return <StatusPill tone="danger"  withDot={false}>Stopped</StatusPill>;
    case 'Cancelled':          return <StatusPill tone="danger"  withDot={false}>Cancelled</StatusPill>;
    default:                   return <StatusPill tone="neutral" withDot={false}>{value || '—'}</StatusPill>;
  }
}

const materialRequests: DoctypeConfig = {
  slug: 'material-requests', modulePath: '/accounting', module: 'Procurement',
  doctype: 'material_request', title: 'Material Requests', singular: 'Material Request', icon: ClipboardList,
  endpoint: '/accounting/material-requests',
  columns: [
    codeCol('name', 'No.'),
    dateCol('transaction_date', 'Requested'),
    dateCol('required_by_date', 'Required by'),
    { accessorKey: 'purpose', header: 'Purpose', cell: (i) => {
        const v = i.getValue<string>();
        const label = v === 'purchase' ? 'Purchase'
          : v === 'material_transfer' ? 'Transfer'
          : v === 'material_issue' ? 'Issue'
          : v === 'manufacture' ? 'Manufacture' : v;
        return <StatusPill tone="neutral" withDot={false}>{label}</StatusPill>;
    } },
    { accessorKey: 'status', header: 'Status', cell: (i) => <MRStatusPill value={i.getValue<string>()} /> },
  ],
};

const purchaseOrders: DoctypeConfig = {
  slug: 'purchase-orders', modulePath: '/accounting', module: 'Procurement',
  doctype: 'purchase_order', title: 'Purchase Orders', singular: 'Purchase Order', icon: ClipboardList,
  endpoint: '/accounting/purchase-orders',
  columns: [
    codeCol('name', 'No.'),
    dateCol('transaction_date', 'Order date'),
    dateCol('required_by_date', 'Required by'),
    partyCol('supplier_id', 'Supplier'),
    moneyCol('grand_total', 'Grand total'),
    { accessorKey: 'status', header: 'Status', cell: (i) => <POStatusPill value={i.getValue<string>()} /> },
  ],
};

const purchaseInvoices: DoctypeConfig = {
  slug: 'purchase-invoices', modulePath: '/accounting', module: 'Finance',
  doctype: 'purchase_invoice', title: 'Purchase Invoices', singular: 'Purchase Invoice', icon: ShoppingBag,
  endpoint: '/accounting/purchase-invoices',
  columns: [
    codeCol('name', 'No.'),
    dateCol('posting_date', 'Date'),
    partyCol('supplier_id', 'Supplier'),
    moneyCol('grand_total', 'Grand total'),
    moneyCol('outstanding_amount', 'Outstanding'),
    docstatusCol,
  ],
};

const paymentEntries: DoctypeConfig = {
  slug: 'payment-entries', modulePath: '/accounting', module: 'Finance',
  doctype: 'payment_entry', title: 'Payment Entries', singular: 'Payment Entry', icon: Wallet,
  endpoint: '/accounting/payment-entries',
  columns: [
    codeCol('name', 'No.'),
    dateCol('posting_date', 'Date'),
    { accessorKey: 'payment_type', header: 'Type', cell: (i) => {
        const v = i.getValue<string>();
        return v === 'receive'
          ? <StatusPill tone="success" withDot={false}>Receive</StatusPill>
          : v === 'pay'
            ? <StatusPill tone="info" withDot={false}>Pay</StatusPill>
            : <StatusPill tone="neutral" withDot={false}>Transfer</StatusPill>;
    } },
    moneyCol('paid_amount', 'Paid'),
    moneyCol('total_allocated_amount', 'Allocated'),
    docstatusCol,
  ],
};

const journalEntries: DoctypeConfig = {
  slug: 'journal-entries', modulePath: '/accounting', module: 'Finance',
  doctype: 'journal_entry', title: 'Journal Entries', singular: 'Journal Entry', icon: FileText,
  endpoint: '/accounting/journal-entries',
  columns: [
    codeCol('name', 'No.'),
    dateCol('posting_date', 'Date'),
    moneyCol('total_debit', 'Total debit'),
    moneyCol('total_credit', 'Total credit'),
    { accessorKey: 'user_remark', header: 'Remark', cell: (i) => <span className="text-text-secondary truncate">{i.getValue<string>() || '—'}</span> },
    docstatusCol,
  ],
};

const warehouses: DoctypeConfig = {
  slug: 'warehouses', modulePath: '/stock', module: 'Stock',
  doctype: 'warehouse', title: 'Warehouses', singular: 'Warehouse', icon: Warehouse,
  endpoint: '/stock/warehouses',
  columns: [
    codeCol('code', 'Code'),
    nameCol('name', 'Warehouse', Warehouse),
    { accessorKey: 'warehouse_type', header: 'Type', cell: (i) => <span className="text-text-secondary">{i.getValue<string>() || '—'}</span> },
    { accessorKey: 'is_group', header: 'Group?', cell: (i) => i.getValue<boolean>() ? <StatusPill tone="info" withDot={false}>Group</StatusPill> : <span className="text-text-tertiary">—</span> },
  ],
};

const posInvoices: DoctypeConfig = {
  slug: 'invoices', modulePath: '/pos', module: 'POS',
  doctype: 'pos_invoice', title: 'POS Invoices', singular: 'POS Invoice', icon: ShoppingCart,
  endpoint: '/pos/invoices', // NOTE: server has no list endpoint for POS; we'll handle gracefully
  columns: [
    codeCol('name'),
    dateCol('posting_date', 'Date'),
    moneyCol('grand_total', 'Total'),
    { accessorKey: 'is_offline', header: 'Source', cell: (i) => i.getValue<boolean>() ? <StatusPill tone="warning" withDot={false}>Offline</StatusPill> : <StatusPill tone="success" withDot={false}>Online</StatusPill> },
  ],
  hasNew: false,
};

const employees: DoctypeConfig = {
  slug: 'employees', modulePath: '/hr', module: 'HR & Payroll',
  doctype: 'employee', title: 'Employees', singular: 'Employee', icon: Users,
  endpoint: '/hr/employees',
  columns: [
    codeCol('name'),
    nameCol('employee_name', 'Name', Users),
    { accessorKey: 'ptkp_status', header: 'PTKP', cell: (i) => <span className="font-mono text-dense text-text-secondary">{i.getValue<string>() || '—'}</span> },
    { accessorKey: 'npwp', header: 'NPWP', cell: (i) => <span className="font-mono text-dense text-text-secondary">{i.getValue<string>() || '—'}</span> },
    { accessorKey: 'status', header: 'Status', cell: (i) => <StatusPill tone={i.getValue<string>() === 'Active' ? 'success' : 'neutral'}>{i.getValue<string>()}</StatusPill> },
  ],
};

const leads: DoctypeConfig = {
  slug: 'leads', modulePath: '/crm', module: 'CRM',
  doctype: 'lead', title: 'Leads', singular: 'Lead', icon: UserSquare,
  endpoint: '/crm/leads',
  columns: [
    codeCol('name'),
    nameCol('lead_name', 'Lead', UserSquare),
    { accessorKey: 'source', header: 'Source', cell: (i) => <span className="text-text-secondary">{i.getValue<string>() || '—'}</span> },
    { accessorKey: 'status', header: 'Status', cell: (i) => {
        const v = i.getValue<string>();
        return v === 'Converted' ? <StatusPill tone="success">{v}</StatusPill>
             : v === 'Lost'      ? <StatusPill tone="danger">{v}</StatusPill>
             : <StatusPill tone="neutral">{v}</StatusPill>;
    } },
    dateCol('created_at', 'Created'),
  ],
};

const projects: DoctypeConfig = {
  slug: 'projects', modulePath: '/projects', module: 'Operations',
  doctype: 'project', title: 'Projects', singular: 'Project', icon: Briefcase,
  endpoint: '/projects/projects',
  columns: [
    codeCol('name'),
    nameCol('project_name', 'Project', Briefcase),
    { accessorKey: 'status', header: 'Status', cell: (i) => <StatusPill tone={i.getValue<string>() === 'Completed' ? 'success' : 'accent'}>{i.getValue<string>()}</StatusPill> },
    moneyCol('total_billable_amount', 'Billable'),
    moneyCol('total_billed_amount', 'Billed'),
  ],
};

const boms: DoctypeConfig = {
  slug: 'boms', modulePath: '/manufacturing', module: 'Production',
  doctype: 'bom', title: 'BOMs', singular: 'BOM', icon: Wrench,
  endpoint: '/manufacturing/boms', // server has no list endpoint, will degrade
  columns: [
    codeCol('name'),
    nameCol('item_id', 'For item'),
    { accessorKey: 'quantity', header: 'Output qty', meta: { align: 'right' }, cell: (i) => <span className="num">{i.getValue<string>()}</span> },
    moneyCol('total_cost', 'Total cost'),
    docstatusCol,
  ],
  hasNew: false,
};

const workOrders: DoctypeConfig = {
  slug: 'work-orders', modulePath: '/manufacturing', module: 'Production',
  doctype: 'work_order', title: 'Work Orders', singular: 'Work Order', icon: Factory,
  endpoint: '/manufacturing/work-orders',
  columns: [
    codeCol('name'),
    dateCol('created_at', 'Created'),
    { accessorKey: 'qty', header: 'Qty', meta: { align: 'right' }, cell: (i) => <span className="num">{i.getValue<string>()}</span> },
    moneyCol('total_cost', 'Total cost'),
    { accessorKey: 'status', header: 'Status', cell: (i) => <StatusPill tone={i.getValue<string>() === 'Completed' ? 'success' : 'accent'}>{i.getValue<string>()}</StatusPill> },
  ],
  hasNew: false,
};

const assetLocations: DoctypeConfig = {
  slug: 'asset-locations', modulePath: '/assets', module: 'Asset & Inventory',
  doctype: 'asset_location', title: 'Asset Locations', singular: 'Asset Location', icon: Building2,
  endpoint: '/assets/asset-locations',
  columns: [
    nameCol('name', 'Location', Building2),
    { accessorKey: 'parent_id', header: 'Parent', cell: (i) => {
        const v = i.getValue<string>();
        return v ? <span className="font-mono text-caption text-text-secondary">{v.slice(-8)}</span> : <span className="text-text-tertiary">—</span>;
    } },
    { accessorKey: 'is_group', header: 'Group?', cell: (i) => i.getValue<boolean>() ? <StatusPill tone="info" withDot={false}>Group</StatusPill> : <span className="text-text-tertiary">—</span> },
    { accessorKey: 'address', header: 'Address', cell: (i) => <span className="text-text-secondary truncate">{i.getValue<string>() || '—'}</span> },
  ],
};

const assetMovements: DoctypeConfig = {
  slug: 'asset-movements', modulePath: '/assets', module: 'Asset & Inventory',
  doctype: 'asset_movement', title: 'Asset Movements', singular: 'Asset Movement', icon: ClipboardList,
  endpoint: '/assets/asset-movements',
  columns: [
    codeCol('name'),
    dateCol('movement_date', 'Date'),
    { accessorKey: 'movement_type', header: 'Type', cell: (i) => <StatusPill tone="info" withDot={false}>{i.getValue<string>()}</StatusPill> },
    { accessorKey: 'asset_id',     header: 'Asset',    cell: (i) => <span className="font-mono text-caption text-text-secondary">{(i.getValue<string>() || '').slice(-8)}</span> },
    { accessorKey: 'to_custodian', header: 'To custodian', cell: (i) => <span className="text-text-primary">{i.getValue<string>() || '—'}</span> },
    { accessorKey: 'to_location',  header: 'To location',  cell: (i) => <span className="text-text-secondary">{i.getValue<string>() || '—'}</span> },
    docstatusCol,
  ],
};

const assetCategories: DoctypeConfig = {
  slug: 'asset-categories', modulePath: '/assets', module: 'Asset & Inventory',
  doctype: 'asset_category', title: 'Asset Categories', singular: 'Asset Category', icon: Tag,
  endpoint: '/assets/asset-categories',
  columns: [
    nameCol('name', 'Category', Tag),
    { accessorKey: 'default_depreciation_method', header: 'Method',
      cell: (i) => <span className="text-text-secondary">{depreciationMethodLabel(i.getValue<string>())}</span> },
    { accessorKey: 'total_useful_life_months', header: 'Useful life (mo)',
      cell: (i) => <span className="num text-text-secondary">{i.getValue<number>()}</span>, meta: { align: 'right' } },
  ],
};

const assets: DoctypeConfig = {
  slug: 'assets', modulePath: '/assets', module: 'Asset & Inventory',
  doctype: 'asset', title: 'Assets', singular: 'Asset', icon: ClipboardList,
  endpoint: '/assets/assets',
  columns: [
    codeCol('name'),
    nameCol('asset_name', 'Asset', ClipboardList),
    dateCol('purchase_date', 'Purchased'),
    moneyCol('gross_purchase_amount', 'Gross'),
    moneyCol('accumulated_depreciation', 'Acc. depreciation'),
    { accessorKey: 'status', header: 'Status', cell: (i) => <StatusPill tone="accent">{i.getValue<string>()}</StatusPill> },
  ],
  hasNew: false,
};

const issues: DoctypeConfig = {
  slug: 'issues', modulePath: '/support', module: 'Helpdesk',
  doctype: 'issue', title: 'Issues', singular: 'Issue', icon: Headphones,
  endpoint: '/support/issues',
  columns: [
    codeCol('name'),
    nameCol('subject', 'Subject', Headphones),
    { accessorKey: 'priority', header: 'Priority', cell: (i) => {
      const v = i.getValue<string>();
      return v === 'Urgent' ? <StatusPill tone="danger">{v}</StatusPill>
           : v === 'High'   ? <StatusPill tone="warning">{v}</StatusPill>
           : <StatusPill tone="neutral">{v}</StatusPill>;
    } },
    { accessorKey: 'status', header: 'Status', cell: (i) => {
      const v = i.getValue<string>();
      return v === 'Resolved' || v === 'Closed' ? <StatusPill tone="success">{v}</StatusPill>
           : v === 'In Progress' ? <StatusPill tone="accent">{v}</StatusPill>
           : <StatusPill tone="neutral">{v}</StatusPill>;
    } },
    dateCol('opened_at', 'Opened'),
  ],
};

export const doctypes: Record<string, DoctypeConfig> = {
  items, customers, suppliers, taxTemplates, accounts,
  salesInvoices, materialRequests, purchaseOrders, purchaseReceipts, purchaseInvoices, paymentEntries, journalEntries,
  warehouses, posInvoices,
  employees, leads, projects, boms, workOrders, assets, assetCategories, assetLocations, assetMovements, issues,
};

// Pretty-prints the four depreciation method enum values for tables + forms.
export function depreciationMethodLabel(m: string): string {
  switch (m) {
    case 'straight_line':      return 'Straight line';
    case 'written_down_value': return 'Written down value';
    case 'declining_balance':  return 'Double declining';
    case 'manual':             return 'Manual';
    default:                   return m || '—';
  }
}

// Module index — for the module landing pages.
export const modules: { path: string; name: string; icon: LucideIcon; doctypes: DoctypeConfig[]; description: string }[] = [
  {
    path: '/accounting', name: 'Finance', icon: Wallet,
    description: 'Sales, purchases, payments, taxes, and the books.',
    doctypes: [salesInvoices, purchaseInvoices, paymentEntries, journalEntries, customers, suppliers, items, taxTemplates, accounts],
  },
  {
    path: '/stock', name: 'Stock', icon: Warehouse,
    description: 'Items, warehouses, and stock movements.',
    doctypes: [warehouses, purchaseReceipts, items],
  },
  {
    path: '/buying', name: 'Procurement', icon: ShoppingBag,
    description: 'Material requests, purchase orders, receipts, and bills.',
    doctypes: [materialRequests, purchaseOrders, purchaseReceipts, purchaseInvoices, suppliers, items],
  },
  {
    path: '/selling', name: 'Sales', icon: BarChart3,
    description: 'Customers, sales invoices, and the order-to-cash funnel.',
    doctypes: [salesInvoices, customers, items],
  },
  {
    path: '/hr', name: 'HR & Payroll', icon: Users,
    description: 'Employees, payroll runs, attendance, leave, and expense claims.',
    doctypes: [employees],
  },
  {
    path: '/crm', name: 'CRM', icon: UserSquare,
    description: 'Leads and the conversion funnel.',
    doctypes: [leads, customers],
  },
  {
    path: '/projects', name: 'Operations', icon: Briefcase,
    description: 'Projects, tasks, and timesheet billing.',
    doctypes: [projects],
  },
  {
    path: '/manufacturing', name: 'Production', icon: Factory,
    description: 'BOMs and work orders.',
    doctypes: [boms, workOrders],
  },
  {
    path: '/assets', name: 'Asset & Inventory', icon: ClipboardList,
    description: 'Fixed-asset register, depreciation, movements, locations.',
    doctypes: [assets, assetCategories, assetLocations, assetMovements],
  },
  {
    path: '/support', name: 'Helpdesk', icon: Headphones,
    description: 'Tickets, SLAs, and customer support.',
    doctypes: [issues],
  },
  {
    path: '/pos', name: 'POS', icon: ShoppingCart,
    description: 'Point-of-sale invoices and profiles.',
    doctypes: [posInvoices],
  },
];

// Convenience: get config by slug+module path.
export function getDoctype(modulePath: string, slug: string): DoctypeConfig | undefined {
  return Object.values(doctypes).find((d) => d.modulePath === modulePath && d.slug === slug);
}

// Re-export for sidebar / starred shortcuts
export { Sparkles, Settings };
