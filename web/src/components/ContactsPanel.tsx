import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Plus, Phone, Mail, Star, AlertCircle } from 'lucide-react';
import { Card, CardTitle, CardDescription } from './Card';
import { Button } from './Button';
import { Field, Input } from './Input';
import { Skeleton } from './EmptyState';
import { StatusPill } from './StatusPill';
import { api } from '@/lib/api';

/**
 * ContactsPanel — drops into any record detail page to show the people
 * attached to that record. Reads /crm/contacts?parent_doctype=X&parent_id=Y;
 * the backend allowlist (internal/crm/contact) gates which parents accept
 * contacts.
 *
 * Use from a customer / supplier / lead detail page; the parent passes its
 * doctype + id and gets back a compact list with inline-create.
 */

interface Contact {
  id: string;
  full_name: string;
  email?: string;
  phone?: string;
  job_title?: string;
  is_primary: boolean;
}

interface ContactsPanelProps {
  parentDoctype: string;
  parentID: string;
}

export function ContactsPanel({ parentDoctype, parentID }: ContactsPanelProps) {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: ['contacts', parentDoctype, parentID],
    queryFn:  () => api<{ items: Contact[] }>(
      `/crm/contacts?parent_doctype=${parentDoctype}&parent_id=${parentID}`,
    ),
    enabled: !!parentID,
  });

  const [adding, setAdding] = useState(false);
  const items = data?.items ?? [];

  return (
    <Card padded={false}>
      <div className="px-5 py-3 border-b border-hairline flex items-baseline justify-between">
        <div>
          <CardTitle>Contacts</CardTitle>
          <CardDescription>People at this {parentDoctype}.</CardDescription>
        </div>
        {!adding && (
          <Button size="sm" variant="secondary" onClick={() => setAdding(true)}>
            <Plus className="size-3.5" /> Add
          </Button>
        )}
      </div>

      {isLoading ? (
        <div className="p-5"><Skeleton className="h-20 w-full" /></div>
      ) : items.length === 0 && !adding ? (
        <div className="px-5 py-6 text-center text-caption text-stone">
          No contacts yet. Add the people you talk to here.
        </div>
      ) : (
        <ul className="divide-y divide-hairline">
          {items.map((c) => (
            <li key={c.id} className="px-5 py-3">
              <div className="flex items-baseline gap-2 flex-wrap">
                <span className="text-body-sm font-medium text-ink">{c.full_name}</span>
                {c.is_primary && (
                  <StatusPill tone="accent" withDot={false}>
                    <Star className="size-3" /> Primary
                  </StatusPill>
                )}
                {c.job_title && (
                  <span className="text-caption text-stone">— {c.job_title}</span>
                )}
              </div>
              {(c.email || c.phone) && (
                <div className="mt-1 flex items-center gap-4 text-caption text-stone">
                  {c.email && (
                    <a href={`mailto:${c.email}`} className="inline-flex items-center gap-1 hover:text-ink">
                      <Mail className="size-3" /> {c.email}
                    </a>
                  )}
                  {c.phone && (
                    <a href={`tel:${c.phone}`} className="inline-flex items-center gap-1 hover:text-ink">
                      <Phone className="size-3" /> {c.phone}
                    </a>
                  )}
                </div>
              )}
            </li>
          ))}
        </ul>
      )}

      {adding && (
        <AddContact
          parentDoctype={parentDoctype}
          parentID={parentID}
          onClose={() => setAdding(false)}
          onAdded={() => {
            setAdding(false);
            void qc.invalidateQueries({ queryKey: ['contacts', parentDoctype, parentID] });
          }}
        />
      )}
    </Card>
  );
}

function AddContact({
  parentDoctype, parentID, onClose, onAdded,
}: { parentDoctype: string; parentID: string; onClose: () => void; onAdded: () => void }) {
  const [firstName, setFirstName] = useState('');
  const [lastName, setLastName]   = useState('');
  const [email, setEmail]         = useState('');
  const [phone, setPhone]         = useState('');
  const [jobTitle, setJobTitle]   = useState('');
  const [isPrimary, setIsPrimary] = useState(false);
  const [err, setErr] = useState<string | null>(null);

  const mut = useMutation({
    mutationFn: () => api('/crm/contacts', {
      method: 'POST',
      body: {
        parent_doctype: parentDoctype,
        parent_id:      parentID,
        first_name:     firstName,
        last_name:      lastName || undefined,
        email:          email || undefined,
        phone:          phone || undefined,
        job_title:      jobTitle || undefined,
        is_primary:     isPrimary,
      },
    }),
    onSuccess: () => onAdded(),
    onError:   (e: Error) => setErr(e.message),
  });

  return (
    <div className="px-5 py-4 border-t border-hairline bg-surface-soft space-y-3">
      <div className="grid grid-cols-2 gap-2">
        <Field label="First name">
          <Input value={firstName} onChange={(e) => setFirstName(e.target.value)} autoFocus />
        </Field>
        <Field label="Last name">
          <Input value={lastName} onChange={(e) => setLastName(e.target.value)} />
        </Field>
        <Field label="Job title">
          <Input value={jobTitle} onChange={(e) => setJobTitle(e.target.value)} />
        </Field>
        <Field label="Email">
          <Input value={email} onChange={(e) => setEmail(e.target.value)} />
        </Field>
        <Field label="Phone">
          <Input value={phone} onChange={(e) => setPhone(e.target.value)} />
        </Field>
      </div>
      <label className="inline-flex items-center gap-2 cursor-pointer text-body-sm text-charcoal">
        <input type="checkbox" className="accent-brand-green-deep" checked={isPrimary}
          onChange={(e) => setIsPrimary(e.target.checked)} />
        Primary contact (replaces any existing primary)
      </label>
      {err && (
        <div className="text-caption text-brand-error inline-flex items-start gap-1.5">
          <AlertCircle className="size-3.5 mt-0.5" /> {err}
        </div>
      )}
      <div className="flex justify-end gap-2">
        <Button size="sm" variant="ghost" onClick={onClose}>Cancel</Button>
        <Button size="sm" onClick={() => mut.mutate()}
          loading={mut.isPending} disabled={!firstName.trim()}>
          Save
        </Button>
      </div>
    </div>
  );
}
