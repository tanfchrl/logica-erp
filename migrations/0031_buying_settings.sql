-- +goose Up
-- +goose StatementBegin

-- ============================================================================
-- BUYING SETTINGS (per-company singleton)
-- ============================================================================
-- ERPNext-equivalent buying configuration. One row per company; service
-- guarantees this with UNIQUE(company_id) + UPSERT semantics. Values shape
-- behaviour in the PI / GRN services (over-billing/over-receipt tolerance,
-- "is PO required", etc).

CREATE TABLE buying_settings (
  id                            text PRIMARY KEY,
  company_id                    text NOT NULL UNIQUE REFERENCES company(id) ON DELETE CASCADE,
  -- Workflow gates
  po_required_for_pi            boolean NOT NULL DEFAULT false,
  pr_required_for_pi            boolean NOT NULL DEFAULT false,
  -- Tolerances expressed as percentage (e.g. 5.00 = 5%). 0 = no over-allowance.
  over_billing_tolerance_pct    numeric(8,4) NOT NULL DEFAULT 0 CHECK (over_billing_tolerance_pct >= 0),
  over_receipt_tolerance_pct    numeric(8,4) NOT NULL DEFAULT 0 CHECK (over_receipt_tolerance_pct >= 0),
  -- Pricing rules
  maintain_same_rate            boolean NOT NULL DEFAULT false,
  allow_item_multiple_times     boolean NOT NULL DEFAULT true,
  disable_last_purchase_rate    boolean NOT NULL DEFAULT false,
  bill_for_rejected_qty         boolean NOT NULL DEFAULT false,
  -- Defaults
  default_supplier_group_id     text REFERENCES supplier_group(id),
  -- Audit
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  updated_by  text REFERENCES users(id)
);
CREATE TRIGGER buying_settings_touch BEFORE UPDATE ON buying_settings
  FOR EACH ROW EXECUTE FUNCTION logica_touch_updated_at();

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS buying_settings;
-- +goose StatementEnd
