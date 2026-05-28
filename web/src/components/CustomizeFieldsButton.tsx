import { useState } from 'react';
import { Settings2 } from 'lucide-react';
import { Button } from '@/components/Button';
import { Card } from '@/components/Card';
import { Dialog, DialogContent, DialogDescription, DialogTitle } from '@/components/Dialog';
import { CustomFieldsManager } from '@/components/CustomFieldsManager';
import { useMyPermissions } from '@/lib/permissions';

/**
 * Per-doctype "Customize fields" toolbar button. Renders nothing for
 * non-system users so it stays out of the way for normal staff. Clicking it
 * opens a dialog scoped to the given doctype with the same list + add UI
 * Settings → Custom fields uses.
 */
export function CustomizeFieldsButton({ doctype }: { doctype: string }) {
  const { isSystem, isLoading } = useMyPermissions();
  const [open, setOpen] = useState(false);

  if (isLoading || !isSystem) return null;

  return (
    <Dialog open={open} onOpenChange={setOpen}>
      <Button variant="secondary" onClick={() => setOpen(true)}>
        <Settings2 className="size-4" /> Customize
      </Button>
      <DialogContent className="max-w-2xl">
        <DialogTitle>Customize fields</DialogTitle>
        <DialogDescription>
          Add per-doctype extra fields without redeploying. Visible in forms
          and the API immediately after save.
        </DialogDescription>
        <Card padded={false} className="overflow-hidden max-h-[60vh] overflow-y-auto">
          <CustomFieldsManager doctype={doctype} />
        </Card>
      </DialogContent>
    </Dialog>
  );
}
