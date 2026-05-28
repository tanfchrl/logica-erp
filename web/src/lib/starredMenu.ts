import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { api } from './api';

/**
 * Per-user starred sidebar items. Wraps GET /me/starred-menu + the
 * add/remove mutations. Sidebar's "Starred" section renders from this;
 * each list page's StarMenuButton toggles it.
 */

export interface StarredMenuItem {
  path: string;
  label: string;
  position: number;
  starred_at: string;
}

interface StarredList { items: StarredMenuItem[] }

const KEY = ['me-starred-menu'] as const;

export function useStarredMenu() {
  const qc = useQueryClient();
  const { data, isLoading } = useQuery({
    queryKey: KEY,
    queryFn:  () => api<StarredList>('/me/starred-menu'),
    // Per-user; rarely changes; cache for the session.
    staleTime: 5 * 60_000,
  });
  const items = data?.items ?? [];

  const add = useMutation({
    mutationFn: (input: { path: string; label: string }) =>
      api<StarredMenuItem>('/me/starred-menu', { method: 'POST', body: input }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: KEY }),
  });

  const remove = useMutation({
    mutationFn: (path: string) =>
      api(`/me/starred-menu?path=${encodeURIComponent(path)}`, { method: 'DELETE' }),
    onSuccess: () => void qc.invalidateQueries({ queryKey: KEY }),
  });

  return {
    items,
    isLoading,
    isStarred: (path: string) => items.some((s) => s.path === path),
    toggle: (path: string, label: string) => {
      if (items.some((s) => s.path === path)) remove.mutate(path);
      else add.mutate({ path, label });
    },
    pending: add.isPending || remove.isPending,
  };
}
