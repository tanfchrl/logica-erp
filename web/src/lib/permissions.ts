import { useQuery } from '@tanstack/react-query';
import { api } from './api';

/**
 * useMyPermissions — single source of truth for "what doctypes can I read".
 * Wraps GET /auth/permissions, which returns either is_system=true (wildcard)
 * or a per-doctype map of read/write/create/etc. booleans.
 *
 * UI surfaces that branch on permission should use canRead()/canWrite()
 * instead of inspecting the raw response.
 */

export interface DoctypePerm {
  read: boolean;
  write: boolean;
  create: boolean;
  delete: boolean;
  submit: boolean;
  cancel: boolean;
  export: boolean;
}

export interface PermsResponse {
  is_system: boolean;
  doctypes: Record<string, DoctypePerm>;
}

export interface PermissionsHook {
  isLoading: boolean;
  isSystem: boolean;
  canRead: (doctype: string) => boolean;
  canRead_any: (doctypes: string[]) => boolean;
  canWrite: (doctype: string) => boolean;
}

const EMPTY: PermsResponse = { is_system: false, doctypes: {} };

export function useMyPermissions(): PermissionsHook {
  const { data, isLoading } = useQuery({
    queryKey: ['auth-permissions'],
    queryFn:  () => api<PermsResponse>('/auth/permissions'),
    // Permissions change rarely (admin edit), so cache for the session.
    // Invalidated by the role-edit UI through the same queryKey.
    staleTime: 5 * 60_000,
  });
  const perms = data ?? EMPTY;
  return {
    isLoading,
    isSystem: perms.is_system,
    canRead: (d) => perms.is_system || !!perms.doctypes[d]?.read,
    canRead_any: (ds) => perms.is_system || ds.some((d) => perms.doctypes[d]?.read),
    canWrite: (d) => perms.is_system || !!perms.doctypes[d]?.write,
  };
}
