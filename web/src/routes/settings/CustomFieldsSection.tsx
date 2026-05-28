import { useState } from 'react';
import { Settings, AlertCircle } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { CustomFieldsManager, CUSTOM_FIELD_DOCTYPES } from '@/components/CustomFieldsManager';

/**
 * CustomFieldsSection — Settings → "Custom fields". Admins define per-
 * doctype extra fields; the backend's existing customfield Validator
 * picks them up automatically on create/update of any doctype that runs
 * through customfield.EnsureTxValidator.
 *
 * Doctype picker on top, scoped manager below. Same manager component is
 * embedded in each doctype's "Customize fields" toolbar dialog.
 */
export function CustomFieldsSection() {
  const [selectedDoctype, setSelectedDoctype] = useState<string>(CUSTOM_FIELD_DOCTYPES[0]!);

  return (
    <div className="space-y-5">
      <div>
        <CardTitle>
          <Settings className="size-4 inline mr-1.5 text-accent" /> Custom fields
        </CardTitle>
        <CardDescription>
          Define extra fields per doctype. The API + auto-generated forms read
          these on the fly — no redeploy needed. System administrators only.
        </CardDescription>
      </div>

      <Card padded={false}>
        <div className="px-5 py-3 border-b border-hairline flex items-center gap-3 flex-wrap">
          <span className="text-body-sm text-stone">Doctype:</span>
          <select className="input-base !h-8 !w-auto"
            value={selectedDoctype}
            onChange={(e) => setSelectedDoctype(e.target.value)}>
            {CUSTOM_FIELD_DOCTYPES.map((dt) => <option key={dt} value={dt}>{dt}</option>)}
          </select>
        </div>
        <CustomFieldsManager doctype={selectedDoctype} />
      </Card>

      <Card className="!p-3 flex items-start gap-2 bg-info/5 border-info/30">
        <AlertCircle className="size-4 text-info shrink-0 mt-0.5" />
        <div className="text-body-sm text-text-secondary">
          Field names use <code className="font-mono text-caption">[a-z0-9_]</code> and become JSON keys on the
          <code className="font-mono text-caption"> custom_fields</code> column. For <code>select</code>, set options to
          <code className="font-mono text-caption">{` {"values":["a","b"]}`}</code>; for <code>link</code>,
          <code className="font-mono text-caption">{` {"doctype":"customer"}`}</code>.
        </div>
      </Card>
    </div>
  );
}
