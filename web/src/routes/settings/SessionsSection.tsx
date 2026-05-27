import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { Monitor, Trash2, LogOut } from 'lucide-react';
import { Card, CardDescription, CardTitle } from '@/components/Card';
import { Button } from '@/components/Button';
import { StatusPill } from '@/components/StatusPill';
import { Skeleton } from '@/components/EmptyState';
import { api } from '@/lib/api';
import { me } from '@/lib/auth';
import { cn } from '@/lib/cn';

interface SessionRow {
  id: string; issued_at: string; expires_at: string;
  user_agent?: string; ip?: string; revoked: boolean;
}
interface SessionList { items: SessionRow[] }

/**
 * Shows the caller's own sessions. Admins managing other users' sessions
 * should use Users → user → Sessions tab.
 */
export function SessionsSection() {
  const qc = useQueryClient();
  const { data: caller } = useQuery({ queryKey: ['me'], queryFn: () => me() });
  const userId = caller?.id;
  const { data, isLoading } = useQuery({
    queryKey: ['my-sessions', userId],
    queryFn:  () => api<SessionList>(`/admin/users/${userId}/sessions`),
    enabled:  !!userId,
  });
  const revoke = useMutation({
    mutationFn: (sid: string) => api(`/admin/users/${userId}/sessions/${sid}`, { method: 'DELETE' }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['my-sessions', userId] }),
  });

  const rows = data?.items ?? [];
  const active = rows.filter((s) => !s.revoked && new Date(s.expires_at) > new Date());

  return (
    <div className="space-y-4">
      <div>
        <CardTitle>Sessions &amp; devices</CardTitle>
        <CardDescription>
          Your signed-in browsers and apps. Revoking forces re-login on that device.
        </CardDescription>
      </div>

      {isLoading ? <Card><Skeleton className="h-32 w-full" /></Card> :
       rows.length === 0 ? (
        <Card>
          <div className="text-center py-8">
            <Monitor className="mx-auto size-6 text-stone mb-2" />
            <div className="text-body-sm text-charcoal">No sessions on record.</div>
          </div>
        </Card>
       ) : (
        <>
          <div className="text-caption text-stone">{active.length} active · {rows.length - active.length} expired or revoked</div>
          <Card padded={false}>
            <table className="w-full text-body-sm">
              <thead className="bg-surface-soft border-b border-hairline">
                <tr className="text-micro-uppercase text-stone">
                  <th className="text-left  font-medium px-4 py-2.5">Issued</th>
                  <th className="text-left  font-medium px-4 py-2.5">Expires</th>
                  <th className="text-left  font-medium px-4 py-2.5">IP</th>
                  <th className="text-left  font-medium px-4 py-2.5">User agent</th>
                  <th className="text-right font-medium px-4 py-2.5"></th>
                </tr>
              </thead>
              <tbody>
                {rows.map((s) => {
                  const isActive = !s.revoked && new Date(s.expires_at) > new Date();
                  return (
                    <tr key={s.id} className="border-b border-hairline last:border-0">
                      <td className="px-4 py-2 text-stone num">{new Date(s.issued_at).toLocaleString('id-ID')}</td>
                      <td className="px-4 py-2 text-stone num">{new Date(s.expires_at).toLocaleString('id-ID')}</td>
                      <td className="px-4 py-2 text-charcoal font-mono text-caption">{s.ip ?? '—'}</td>
                      <td className="px-4 py-2 text-stone truncate max-w-[300px]">{s.user_agent ?? '—'}</td>
                      <td className="px-4 py-2 text-right">
                        {isActive ? (
                          <Button size="sm" variant="ghost" onClick={() => revoke.mutate(s.id)}>
                            <Trash2 className="size-3.5" /> Revoke
                          </Button>
                        ) : (
                          <StatusPill tone="neutral" withDot={false}>{s.revoked ? 'Revoked' : 'Expired'}</StatusPill>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </Card>
        </>
       )}
    </div>
  );
}
