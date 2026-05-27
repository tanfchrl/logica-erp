import {
  Palette, Languages, Building2, UserCog, ShieldCheck, KeyRound,
  Calculator, FileText, Mail, Plug, Wand2, Database, BookOpen, Server, Bell,
  ScrollText, Hash, Inbox, type LucideIcon,
} from 'lucide-react';
import { AppearanceSection }    from './AppearanceSection';
import { LocalizationSection }  from './LocalizationSection';
import { CompanySection }       from './CompanySection';
import { TaxTemplatesSection }  from './TaxTemplatesSection';
import { NumberingSection }     from './NumberingSection';
import { SMTPSection }          from './SMTPSection';
import { EmailTemplatesSection } from './EmailTemplatesSection';
import { AuditLogSection }       from './AuditLogSection';
import { FiscalYearsSection }    from './FiscalYearsSection';
import { UsersSection }          from './UsersSection';
import { RolesSection }          from './RolesSection';
import { PrintTemplatesSection } from './PrintTemplatesSection';
import { WorkflowsSection }      from './WorkflowsSection';
import { ApprovalsInboxSection } from './ApprovalsInboxSection';
import { ImportExportSection }   from './ImportExportSection';
import { WebhooksSection }       from './WebhooksSection';
import { APITokensSection }      from './APITokensSection';
import { SessionsSection }       from './SessionsSection';
import { SystemHealthSection }   from './SystemHealthSection';
import { NotificationRulesSection } from './NotificationRulesSection';
import { EFakturSection }        from './EFakturSection';
import { PayrollConfigSection }  from './PayrollConfigSection';
import { PaymentGatewaysSection, BankFeedsSection, MarketplacesSection } from './ConnectorsPanel';
import { ComingSoonSection, type ComingSoonProps } from './ComingSoonSection';

/**
 * Single source of truth for the Settings IA. The sidebar nav, the routing,
 * and breadcrumbs are all derived from this registry — add a section here and
 * it appears everywhere.
 */
export interface SectionDef {
  key: string;
  label: string;
  description: string;
  icon: LucideIcon;
  /** Top-level grouping for the sidebar. */
  group: 'general' | 'access' | 'finance' | 'documents' | 'comms' | 'integrations' | 'automation' | 'data' | 'system';
  component: React.ComponentType;
}

/* Helper to build a ComingSoon section without repeating boilerplate. */
function soon(props: ComingSoonProps): React.ComponentType {
  const C = () => <ComingSoonSection {...props} />;
  C.displayName = `ComingSoon(${props.title})`;
  return C;
}

export const SECTIONS: SectionDef[] = [
  // ----- General -----
  { key: 'appearance', group: 'general', label: 'Appearance', icon: Palette,
    description: 'Theme, brand mark, and workspace identity.',
    component: AppearanceSection },
  { key: 'localization', group: 'general', label: 'Localization', icon: Languages,
    description: 'Language, date and number format, timezone.',
    component: LocalizationSection },
  { key: 'company', group: 'general', label: 'Companies', icon: Building2,
    description: 'Legal entities you do business under.',
    component: CompanySection },

  // ----- Users & Access -----
  { key: 'users', group: 'access', label: 'Users', icon: UserCog,
    description: 'Invite, deactivate, set passwords, assign roles + companies, manage sessions.',
    component: UsersSection },
  { key: 'roles', group: 'access', label: 'Roles & permissions', icon: ShieldCheck,
    description: 'Define roles and the per-doctype permission matrix.',
    component: RolesSection },
  { key: 'sessions', group: 'access', label: 'Sessions & devices', icon: KeyRound,
    description: 'Your active sessions and revoke any device.',
    component: SessionsSection },
  { key: 'api-tokens', group: 'access', label: 'API tokens', icon: Hash,
    description: 'Personal access tokens for scripts and integrations.',
    component: APITokensSection },

  // ----- Finance -----
  { key: 'fiscal-years', group: 'finance', label: 'Fiscal years', icon: Calculator,
    description: 'Fiscal periods, year-end close, and period locks.',
    component: FiscalYearsSection },
  { key: 'tax-templates', group: 'finance', label: 'Tax templates', icon: FileText,
    description: 'PPN templates, withholding tax types, and categories.',
    component: TaxTemplatesSection },
  { key: 'efaktur', group: 'finance', label: 'e-Faktur / Coretax', icon: ScrollText,
    description: 'CSV export for the Indonesian tax authority.',
    component: EFakturSection },
  { key: 'payroll-config', group: 'finance', label: 'Payroll configuration', icon: Calculator,
    description: 'BPJS rates and PPh21 TER tables, versioned by effective date.',
    component: PayrollConfigSection },
  { key: 'numbering', group: 'finance', label: 'Numbering series', icon: Hash,
    description: 'Document number patterns per doctype + company.',
    component: NumberingSection },

  // ----- Documents -----
  { key: 'print-templates', group: 'documents', label: 'Print templates', icon: FileText,
    description: 'PDF templates per doctype, letterheads, paper size, and margins.',
    component: PrintTemplatesSection },

  // ----- Communications -----
  { key: 'smtp', group: 'comms', label: 'Email (SMTP)', icon: Mail,
    description: 'Outbound mail server + test send + delivery log.',
    component: SMTPSection },
  { key: 'email-templates', group: 'comms', label: 'Email templates', icon: BookOpen,
    description: 'Per-event subject + body templates.',
    component: EmailTemplatesSection },
  { key: 'notifications', group: 'comms', label: 'Notification rules', icon: Bell,
    description: 'Event → recipient → channel routing (storage only; engine wiring next).',
    component: NotificationRulesSection },

  // ----- Integrations -----
  { key: 'payment-gateways', group: 'integrations', label: 'Payment gateways', icon: Plug,
    description: 'Midtrans, Xendit, DOKU, iPaymu — credential storage.',
    component: PaymentGatewaysSection },
  { key: 'bank-feeds', group: 'integrations', label: 'Bank feeds', icon: Plug,
    description: 'BCA / Mandiri / BRI / BNI / Brick / Finantier — credential storage.',
    component: BankFeedsSection },
  { key: 'marketplaces', group: 'integrations', label: 'Marketplaces', icon: Plug,
    description: 'Tokopedia, Shopee, TikTok Shop, Lazada — credential storage.',
    component: MarketplacesSection },
  { key: 'webhooks', group: 'integrations', label: 'Webhooks', icon: Plug,
    description: 'HMAC-signed outbound HTTP hooks on events; delivery log + replay.',
    component: WebhooksSection },

  // ----- Automation -----
  { key: 'approvals', group: 'automation', label: 'Approvals inbox', icon: Inbox,
    description: 'Pending and resolved approval requests for the roles you hold.',
    component: ApprovalsInboxSection },
  { key: 'workflows', group: 'automation', label: 'Workflows', icon: Wand2,
    description: 'State machines per doctype + amount/role-based approval rules.',
    component: WorkflowsSection },
  { key: 'jobs', group: 'automation', label: 'System health', icon: Server,
    description: 'Failed deliveries, stuck approvals, import errors over the last 24h.',
    component: SystemHealthSection },

  // ----- Data -----
  { key: 'import-export', group: 'data', label: 'Import / Export', icon: Database,
    description: 'CSV bulk import for customers, suppliers, items, chart of accounts.',
    component: ImportExportSection },
  { key: 'backups', group: 'data', label: 'Backups', icon: Database,
    description: 'Snapshot, restore, off-site target.',
    component: soon({
      title: 'Backups',
      description: 'Scheduled DB + attachment snapshots, with optional off-site target.',
      checklist: ['On-demand snapshot', 'Daily schedule',
                  'S3 / B2 off-site target', 'Restore from snapshot',
                  'Retention policy'],
      backend: 'needs-backend',
    }) },

  // ----- System -----
  { key: 'audit-log', group: 'system', label: 'Audit log', icon: ScrollText,
    description: 'Who changed what, when.',
    component: AuditLogSection },
];

/** Sidebar grouping order + labels. */
export const GROUPS: { key: SectionDef['group']; label: string }[] = [
  { key: 'general',      label: 'General' },
  { key: 'access',       label: 'Users & access' },
  { key: 'finance',      label: 'Finance' },
  { key: 'documents',    label: 'Documents' },
  { key: 'comms',        label: 'Communications' },
  { key: 'integrations', label: 'Integrations' },
  { key: 'automation',   label: 'Automation' },
  { key: 'data',         label: 'Data' },
  { key: 'system',       label: 'System' },
];

export function findSection(key: string | undefined): SectionDef {
  return SECTIONS.find((s) => s.key === key) ?? SECTIONS[0]!;
}
