/**
 * Per-doctype create form schemas.
 * Kept separate from the column defs so the lists file stays focused on read-side.
 */

export type FieldKind =
  | 'text' | 'textarea' | 'number' | 'money' | 'date' | 'bool'
  | 'select' | 'link';

export interface FieldDef {
  name: string;            // JSON key sent to backend
  label: string;
  kind: FieldKind;
  required?: boolean;
  placeholder?: string;
  hint?: string;
  // for 'select'
  options?: { value: string; label: string }[];
  // for 'link' — static target
  linkEndpoint?: string;   // GET endpoint that returns { items: [...] }
  linkLabel?: string;      // field on each item to display (default: 'display_name' or 'name')
  linkDescription?: string;
  /**
   * for 'link' — target depends on the value of another field on this form.
   * Used by Opportunity (party_id depends on opportunity_from) and
   * Contact (parent_id depends on parent_doctype). When the trigger field
   * has no value yet, the picker renders disabled with a hint; when it
   * changes, the dependent field is cleared automatically.
   */
  linkSwitch?: {
    triggerField: string;
    byValue: Record<string, {
      endpoint: string;
      label?: string;
      description?: string;
    }>;
  };
  // optional default value
  default?: string | number | boolean;
  // column span 1 (half) or 2 (full)
  span?: 1 | 2;
}

export interface CreateSchema {
  /** Top-of-form callout text (e.g. "Quick create: line-items added later") */
  notice?: string;
  /** Body fields (excluding child tables — those are handled by bespoke forms) */
  fields: FieldDef[];
  /** Optional path to navigate to after success. Defaults to the list. */
  redirectTo?: (id: string) => string;
  /** Names of any required nested-table fields the backend will reject if missing.
   *  When set, the page renders a "this needs line items" notice and redirects users
   *  to a bespoke form (or the API docs). Used for SI/JE which already have full forms. */
  needsChildTable?: { label: string; bespokeFormPath?: string };
  /** When `triggerField` (a link) changes, fetch from `fetchEndpoint` (with `{id}`
   *  replaced) and copy the named properties onto matching local form fields.
   *  Used by Asset → Asset Category to pre-fill the four config fields. */
  prefillFromLink?: {
    triggerField: string;
    fetchEndpoint: string;
    /** Maps local form field name → property name on the fetched resource. */
    mapping: Record<string, string>;
  };
}

// ---- Schemas per doctype ----

export const customerCreate: CreateSchema = {
  fields: [
    { name: 'name',          label: 'Internal code', kind: 'text', required: true,  placeholder: 'CUST-0001', hint: 'Unique short code used in references.' },
    { name: 'display_name',  label: 'Display name',  kind: 'text', required: true,  placeholder: 'PT Pelanggan' },
    { name: 'npwp',          label: 'NPWP',          kind: 'text', placeholder: '16 digits', hint: 'Indonesian taxpayer number (16 digits, optional).' },
    { name: 'is_individual', label: 'Individual?',   kind: 'bool' },
    { name: 'email',         label: 'Email',         kind: 'text' },
    { name: 'phone',         label: 'Phone',         kind: 'text' },
    { name: 'default_currency', label: 'Default currency', kind: 'select',
      options: [
        { value: 'IDR', label: 'IDR' },
        { value: 'USD', label: 'USD' },
        { value: 'SGD', label: 'SGD' },
        { value: 'EUR', label: 'EUR' },
      ],
      default: 'IDR',
    },
  ],
};

export const supplierCreate: CreateSchema = {
  fields: [
    { name: 'name',          label: 'Internal code', kind: 'text', required: true,  placeholder: 'SUPP-0001' },
    { name: 'display_name',  label: 'Display name',  kind: 'text', required: true,  placeholder: 'PT Pemasok' },
    { name: 'npwp',          label: 'NPWP',          kind: 'text', placeholder: '16 digits' },
    { name: 'is_individual', label: 'Individual?',   kind: 'bool' },
    { name: 'email',         label: 'Email',         kind: 'text' },
    { name: 'phone',         label: 'Phone',         kind: 'text' },
    { name: 'default_currency', label: 'Default currency', kind: 'select',
      options: [
        { value: 'IDR', label: 'IDR' },
        { value: 'USD', label: 'USD' },
        { value: 'SGD', label: 'SGD' },
      ],
      default: 'IDR',
    },
  ],
};

export const itemCreate: CreateSchema = {
  fields: [
    { name: 'code',          label: 'Code',     kind: 'text', required: true, placeholder: 'ITM-0001' },
    { name: 'name',          label: 'Name',     kind: 'text', required: true, span: 2 },
    { name: 'description',   label: 'Description', kind: 'textarea', span: 2 },
    { name: 'stock_uom',     label: 'UOM',      kind: 'text', default: 'Unit' },
    { name: 'standard_rate', label: 'Standard rate', kind: 'money' },
    { name: 'is_stock_item', label: 'Stock item',    kind: 'bool' },
    { name: 'is_sales_item', label: 'Sales item',    kind: 'bool', default: true },
    { name: 'is_purchase_item', label: 'Purchase item', kind: 'bool', default: true },
    { name: 'is_fixed_asset', label: 'Fixed asset',  kind: 'bool',
      hint: 'On = PI submit auto-creates an Asset draft per unit, using the asset category below.' },
    { name: 'asset_category_id', label: 'Default asset category', kind: 'link',
      linkEndpoint: '/assets/asset-categories', linkLabel: 'name', linkDescription: 'default_depreciation_method',
      hint: 'Required when "Fixed asset" is on.' },
  ],
};

export const warehouseCreate: CreateSchema = {
  fields: [
    { name: 'name',           label: 'Name', kind: 'text', required: true },
    { name: 'code',           label: 'Code', kind: 'text' },
    { name: 'warehouse_type', label: 'Type', kind: 'select',
      options: [
        { value: '',                label: '—' },
        { value: 'raw_material',    label: 'Raw Material' },
        { value: 'wip',             label: 'WIP' },
        { value: 'finished_goods',  label: 'Finished Goods' },
        { value: 'transit',         label: 'Transit' },
      ],
    },
    { name: 'account_id', label: 'Stock account', kind: 'link', linkEndpoint: '/accounting/accounts', linkLabel: 'name', linkDescription: 'account_number', hint: 'Asset account that this warehouse posts to.' },
    { name: 'is_group',   label: 'Group node?', kind: 'bool' },
  ],
};

export const accountCreate: CreateSchema = {
  fields: [
    { name: 'name',           label: 'Name', kind: 'text', required: true },
    { name: 'account_number', label: 'Number', kind: 'text' },
    { name: 'root_type',      label: 'Root type', kind: 'select', required: true, default: 'asset',
      options: [
        { value: 'asset',     label: 'Asset' },
        { value: 'liability', label: 'Liability' },
        { value: 'equity',    label: 'Equity' },
        { value: 'income',    label: 'Income' },
        { value: 'expense',   label: 'Expense' },
      ],
    },
    { name: 'account_type', label: 'Type', kind: 'select',
      options: [
        { value: '',           label: '—' },
        { value: 'cash',       label: 'Cash' },
        { value: 'bank',       label: 'Bank' },
        { value: 'receivable', label: 'Receivable' },
        { value: 'payable',    label: 'Payable' },
        { value: 'stock',      label: 'Stock' },
        { value: 'tax',        label: 'Tax' },
        { value: 'cogs',       label: 'COGS' },
        { value: 'fixed_asset',label: 'Fixed Asset' },
        { value: 'accumulated_depreciation', label: 'Accumulated Depreciation' },
        { value: 'depreciation', label: 'Depreciation' },
      ],
    },
    { name: 'account_currency', label: 'Currency', kind: 'select', default: 'IDR',
      options: [{ value: 'IDR', label: 'IDR' }, { value: 'USD', label: 'USD' }, { value: 'EUR', label: 'EUR' }],
    },
    { name: 'is_group', label: 'Group node?', kind: 'bool' },
  ],
};

export const taxTemplateCreate: CreateSchema = {
  notice: 'Quick create makes an empty template. Add tax lines via the API for now — full editor lands next iteration.',
  fields: [
    { name: 'name',    label: 'Name', kind: 'text', required: true, placeholder: 'PPN Keluaran 11%' },
    { name: 'is_sales', label: 'Sales side?', kind: 'bool', default: true, hint: 'Off = purchase template' },
    { name: 'is_default', label: 'Default for company', kind: 'bool' },
  ],
};

export const employeeCreate: CreateSchema = {
  fields: [
    { name: 'employee_name',  label: 'Full name', kind: 'text', required: true, span: 2 },
    { name: 'date_of_joining',label: 'Date of joining', kind: 'date', required: true },
    { name: 'gender', label: 'Gender', kind: 'select',
      options: [{ value: '', label: '—' }, { value: 'male', label: 'Male' }, { value: 'female', label: 'Female' }],
    },
    { name: 'nik',  label: 'NIK (16 digits)',  kind: 'text', placeholder: '3171012345670001' },
    { name: 'npwp', label: 'NPWP (16 digits)', kind: 'text', placeholder: '3171012345670099' },
    { name: 'ptkp_status', label: 'PTKP status', kind: 'select', default: 'TK/0',
      options: ['TK/0','TK/1','TK/2','TK/3','K/0','K/1','K/2','K/3'].map((v) => ({ value: v, label: v })),
      hint: 'For PPh 21 calculation.',
    },
    { name: 'email', label: 'Email', kind: 'text' },
    { name: 'phone', label: 'Phone', kind: 'text' },
    { name: 'bank_name',         label: 'Bank name',         kind: 'text' },
    { name: 'bank_account_no',   label: 'Bank account no.',  kind: 'text' },
    { name: 'bank_account_name', label: 'Bank account name', kind: 'text', span: 2 },
  ],
};

export const leadCreate: CreateSchema = {
  fields: [
    { name: 'lead_name',     label: 'Company / person', kind: 'text', required: true, span: 2 },
    { name: 'contact_email', label: 'Email', kind: 'text' },
    { name: 'contact_phone', label: 'Phone', kind: 'text' },
    { name: 'source', label: 'Source', kind: 'select',
      options: ['', 'website', 'referral', 'event', 'cold_call'].map((v) => ({ value: v, label: v || '—' })),
    },
    { name: 'remarks', label: 'Notes', kind: 'textarea', span: 2 },
  ],
};

export const projectCreate: CreateSchema = {
  fields: [
    { name: 'project_name', label: 'Name', kind: 'text', required: true, span: 2 },
    { name: 'customer_id',  label: 'Customer', kind: 'link', linkEndpoint: '/accounting/customers', linkLabel: 'display_name', linkDescription: 'name' },
    { name: 'start_date',   label: 'Start date',   kind: 'date' },
    { name: 'expected_end_date', label: 'Expected end', kind: 'date' },
    { name: 'remarks', label: 'Notes', kind: 'textarea', span: 2 },
  ],
};

export const opportunityCreate: CreateSchema = {
  notice: 'An Opportunity is one deal in your pipeline. Pick the prospect (Lead or existing Customer), set an expected amount + close date, and drag through the stages as the deal progresses.',
  fields: [
    { name: 'subject', label: 'Deal title', kind: 'text', required: true, span: 2,
      placeholder: 'e.g. PT Pelanggan — perpanjangan kontrak 2026' },
    { name: 'opportunity_from', label: 'For type', kind: 'select', required: true, default: 'lead',
      options: [
        { value: 'lead',     label: 'Lead' },
        { value: 'customer', label: 'Customer' },
      ] },
    { name: 'party_id', label: 'For', kind: 'link', required: true,
      linkSwitch: {
        triggerField: 'opportunity_from',
        byValue: {
          lead:     { endpoint: '/crm/leads',              label: 'lead_name',    description: 'name' },
          customer: { endpoint: '/accounting/customers',   label: 'display_name', description: 'name' },
        },
      } },
    { name: 'amount',   label: 'Expected amount', kind: 'money' },
    { name: 'currency', label: 'Currency', kind: 'select', default: 'IDR',
      options: [{ value: 'IDR', label: 'IDR' }, { value: 'USD', label: 'USD' }, { value: 'SGD', label: 'SGD' }] },
    { name: 'expected_close_date', label: 'Expected close', kind: 'date' },
    { name: 'stage', label: 'Stage', kind: 'select', default: 'prospecting',
      options: [
        { value: 'prospecting',   label: 'Prospecting' },
        { value: 'qualification', label: 'Qualification' },
        { value: 'proposal',      label: 'Proposal' },
        { value: 'negotiation',   label: 'Negotiation' },
        { value: 'closed_won',    label: 'Closed Won' },
      ] },
    { name: 'source',  label: 'Source', kind: 'text', placeholder: 'e.g. referral, web, event' },
    { name: 'remarks', label: 'Notes', kind: 'textarea', span: 2 },
  ],
};

export const contactCreate: CreateSchema = {
  notice: 'Contacts are people attached to a Customer, Supplier, or Lead. Mark one per organisation as Primary — that\'s the default for "copy to invoice" / email-CC.',
  fields: [
    { name: 'first_name',    label: 'First name', kind: 'text', required: true },
    { name: 'last_name',     label: 'Last name',  kind: 'text' },
    { name: 'parent_doctype', label: 'Belongs to type', kind: 'select', required: true, default: 'customer',
      options: [
        { value: 'customer', label: 'Customer' },
        { value: 'supplier', label: 'Supplier' },
        { value: 'lead',     label: 'Lead' },
      ] },
    { name: 'parent_id',     label: 'Belongs to', kind: 'link', required: true,
      linkSwitch: {
        triggerField: 'parent_doctype',
        byValue: {
          customer: { endpoint: '/accounting/customers', label: 'display_name', description: 'name' },
          supplier: { endpoint: '/accounting/suppliers', label: 'display_name', description: 'name' },
          lead:     { endpoint: '/crm/leads',            label: 'lead_name',    description: 'name' },
        },
      } },
    { name: 'job_title',     label: 'Job title', kind: 'text' },
    { name: 'email',         label: 'Email',     kind: 'text' },
    { name: 'phone',         label: 'Phone',     kind: 'text' },
    { name: 'is_primary',    label: 'Primary contact', kind: 'bool',
      hint: 'Replaces any existing primary on this parent.' },
  ],
};

export const issueCreate: CreateSchema = {
  fields: [
    { name: 'subject', label: 'Subject', kind: 'text', required: true, span: 2 },
    { name: 'description', label: 'Description', kind: 'textarea', span: 2 },
    { name: 'priority', label: 'Priority', kind: 'select', default: 'Medium',
      options: ['Low', 'Medium', 'High', 'Urgent'].map((v) => ({ value: v, label: v })),
    },
    { name: 'customer_id',  label: 'Customer', kind: 'link', linkEndpoint: '/accounting/customers', linkLabel: 'display_name', linkDescription: 'name' },
    { name: 'contact_email', label: 'Contact email', kind: 'text' },
  ],
};

export const assetCreate: CreateSchema = {
  notice: 'Pick an Asset Category to auto-fill the depreciation method, useful life, and the three GL accounts. You can still override per asset.',
  fields: [
    { name: 'asset_name', label: 'Asset name', kind: 'text', required: true, span: 2 },
    { name: 'asset_category_id', label: 'Category', kind: 'link',
      linkEndpoint: '/assets/asset-categories', linkLabel: 'name', linkDescription: 'default_depreciation_method',
      hint: 'Drives the four defaults below.' },
    { name: 'purchase_date', label: 'Purchase date', kind: 'date', required: true },
    { name: 'gross_purchase_amount', label: 'Gross amount', kind: 'money', required: true },
    { name: 'expected_value_after_useful_life', label: 'Salvage value', kind: 'money' },
    { name: 'useful_life_months', label: 'Useful life (months)', kind: 'number', required: true, default: 60 },
    { name: 'depreciation_method', label: 'Method', kind: 'select', default: 'straight_line',
      options: [
        { value: 'straight_line',      label: 'Straight line' },
        { value: 'written_down_value', label: 'Written down value' },
        { value: 'manual',             label: 'Manual' },
      ] },
    { name: 'asset_account_id', label: 'Asset account', kind: 'link', required: true,
      linkEndpoint: '/accounting/accounts', linkLabel: 'name', linkDescription: 'root_type' },
    { name: 'accumulated_depreciation_account_id', label: 'Accumulated dep account', kind: 'link', required: true,
      linkEndpoint: '/accounting/accounts', linkLabel: 'name', linkDescription: 'root_type' },
    { name: 'depreciation_expense_account_id', label: 'Dep expense account', kind: 'link', required: true,
      linkEndpoint: '/accounting/accounts', linkLabel: 'name', linkDescription: 'root_type' },
  ],
  // Picking a category triggers the CreateFormPage helper below to overwrite
  // the dependent fields with the category's defaults.
  prefillFromLink: {
    triggerField: 'asset_category_id',
    fetchEndpoint: '/assets/asset-categories/{id}',
    mapping: {
      depreciation_method:                       'default_depreciation_method',
      useful_life_months:                        'total_useful_life_months',
      asset_account_id:                          'asset_account_id',
      accumulated_depreciation_account_id:       'accumulated_depreciation_account_id',
      depreciation_expense_account_id:           'depreciation_expense_account_id',
    },
  },
};

export const assetMovementCreate: CreateSchema = {
  notice: 'Movements are auditable handovers between custodians or locations. No GL impact.',
  fields: [
    { name: 'asset_id', label: 'Asset', kind: 'link', required: true,
      linkEndpoint: '/assets/assets', linkLabel: 'asset_name', linkDescription: 'name' },
    { name: 'movement_date', label: 'Movement date', kind: 'date', required: true },
    { name: 'movement_type', label: 'Type', kind: 'select', required: true, default: 'transfer',
      options: [
        { value: 'issue',    label: 'Issue (first hand-out)' },
        { value: 'receipt',  label: 'Receipt (back to store)' },
        { value: 'transfer', label: 'Transfer (custodian/location change)' },
      ] },
    { name: 'from_custodian',  label: 'From custodian', kind: 'text', hint: 'Optional — leave blank for first issue.' },
    { name: 'to_custodian',    label: 'To custodian',   kind: 'text', required: true },
    { name: 'from_location_id', label: 'From location',  kind: 'link',
      linkEndpoint: '/assets/asset-locations', linkLabel: 'name', linkDescription: 'address' },
    { name: 'to_location_id',   label: 'To location',    kind: 'link',
      linkEndpoint: '/assets/asset-locations', linkLabel: 'name', linkDescription: 'address',
      hint: 'Pick from your Asset Locations master, or leave blank to type free-text below.' },
    { name: 'to_location',     label: 'To location (free text)', kind: 'text',
      hint: 'Only used when the location picker above is empty.' },
    { name: 'purpose',         label: 'Purpose',        kind: 'text', span: 2 },
    { name: 'remarks',         label: 'Remarks',        kind: 'textarea', span: 2 },
  ],
};

export const assetLocationCreate: CreateSchema = {
  notice: 'Hierarchical master of physical sites. Group locations (e.g. "Jakarta HQ") can have children; leaf locations (e.g. "Conference Room A") are where assets actually sit. Lat/lng are optional — fill them when you want this location to render on a map.',
  fields: [
    { name: 'name',      label: 'Location name', kind: 'text', required: true, span: 2 },
    { name: 'parent_id', label: 'Parent', kind: 'link',
      linkEndpoint: '/assets/asset-locations', linkLabel: 'name', linkDescription: 'address',
      hint: 'Leave blank for a top-level location.' },
    { name: 'is_group',  label: 'Group node', kind: 'bool',
      hint: 'On = can have child locations under it; off = leaf where assets are placed.' },
    { name: 'address',   label: 'Address', kind: 'textarea', span: 2 },
    { name: 'latitude',  label: 'Latitude',  kind: 'text',
      placeholder: '-6.2088', hint: 'Decimal degrees, −90 … 90.' },
    { name: 'longitude', label: 'Longitude', kind: 'text',
      placeholder: '106.8456', hint: 'Decimal degrees, −180 … 180.' },
  ],
};

export const assetCategoryCreate: CreateSchema = {
  notice: 'Categories let you stamp consistent depreciation defaults onto every new asset. Useful life is in months (e.g. 48 = 4 years).',
  fields: [
    { name: 'name', label: 'Category name', kind: 'text', required: true, span: 2,
      hint: 'e.g. "Vehicles", "IT equipment", "Furniture".' },
    { name: 'default_depreciation_method', label: 'Default method', kind: 'select', default: 'straight_line',
      options: [
        { value: 'straight_line',      label: 'Straight line' },
        { value: 'written_down_value', label: 'Written down value' },
        { value: 'manual',             label: 'Manual' },
      ] },
    { name: 'total_useful_life_months', label: 'Useful life (months)', kind: 'number', required: true, default: 60 },
    { name: 'asset_account_id', label: 'Default asset account', kind: 'link',
      linkEndpoint: '/accounting/accounts', linkLabel: 'name', linkDescription: 'root_type' },
    { name: 'accumulated_depreciation_account_id', label: 'Default accumulated dep account', kind: 'link',
      linkEndpoint: '/accounting/accounts', linkLabel: 'name', linkDescription: 'root_type' },
    { name: 'depreciation_expense_account_id', label: 'Default dep expense account', kind: 'link',
      linkEndpoint: '/accounting/accounts', linkLabel: 'name', linkDescription: 'root_type' },
  ],
};

export const bomCreate: CreateSchema = {
  notice: 'Quick create makes an empty BOM. Add component lines via API for now — full editor lands next iteration.',
  fields: [
    { name: 'item_id', label: 'Finished item', kind: 'link', required: true,
      linkEndpoint: '/accounting/items', linkLabel: 'code', linkDescription: 'name' },
    { name: 'quantity', label: 'Output qty', kind: 'number', default: 1 },
    { name: 'uom', label: 'UOM', kind: 'text', default: 'Unit' },
    { name: 'is_default', label: 'Default BOM', kind: 'bool' },
  ],
};

export const workOrderCreate: CreateSchema = {
  fields: [
    { name: 'bom_id', label: 'BOM', kind: 'link', required: true,
      linkEndpoint: '/manufacturing/boms', linkLabel: 'name' },
    { name: 'qty', label: 'Qty to manufacture', kind: 'number', required: true, default: 1 },
    { name: 'source_warehouse_id', label: 'Source warehouse (raw)', kind: 'link', required: true,
      linkEndpoint: '/stock/warehouses', linkLabel: 'name' },
    { name: 'target_warehouse_id', label: 'Target warehouse (finished)', kind: 'link', required: true,
      linkEndpoint: '/stock/warehouses', linkLabel: 'name' },
  ],
};

// ---- Stubs for line-item-heavy docs that need bespoke forms ----

export const purchaseInvoiceCreate: CreateSchema = {
  needsChildTable: { label: 'Items + taxes', bespokeFormPath: undefined },
  fields: [],
};
export const paymentEntryCreate: CreateSchema = {
  needsChildTable: { label: 'References + deductions', bespokeFormPath: undefined },
  fields: [],
};
export const stockEntryCreate: CreateSchema = {
  needsChildTable: { label: 'Stock items (item + warehouses + qty)', bespokeFormPath: undefined },
  fields: [],
};
export const posInvoiceCreate: CreateSchema = {
  needsChildTable: { label: 'POS items', bespokeFormPath: undefined },
  fields: [],
};
export const salaryStructureCreate: CreateSchema = {
  needsChildTable: { label: 'Salary components', bespokeFormPath: undefined },
  fields: [],
};

// Lookup by doctype config slug+modulePath (matches lib/doctypes.tsx keys)
export const createSchemas: Record<string, CreateSchema> = {
  '/accounting/customers':         customerCreate,
  '/accounting/suppliers':         supplierCreate,
  '/accounting/items':             itemCreate,
  '/accounting/tax-templates':     taxTemplateCreate,
  '/accounting/accounts':          accountCreate,
  '/accounting/purchase-invoices': purchaseInvoiceCreate,
  '/accounting/payment-entries':   paymentEntryCreate,
  '/stock/warehouses':             warehouseCreate,
  '/stock/stock-entries':          stockEntryCreate,
  '/hr/employees':                 employeeCreate,
  '/crm/leads':                    leadCreate,
  '/projects/projects':            projectCreate,
  '/support/issues':               issueCreate,
  '/crm/contacts':                 contactCreate,
  '/crm/opportunities':            opportunityCreate,
  '/assets/assets':                assetCreate,
  '/assets/asset-categories':      assetCategoryCreate,
  '/assets/asset-movements':       assetMovementCreate,
  '/assets/asset-locations':       assetLocationCreate,
  '/manufacturing/boms':           bomCreate,
  '/manufacturing/work-orders':    workOrderCreate,
  '/pos/invoices':                 posInvoiceCreate,
};

export function getCreateSchema(modulePath: string, slug: string): CreateSchema | undefined {
  return createSchemas[`${modulePath}/${slug}`];
}
