import { api, setAccessToken, setActiveCompany, getAccessToken } from './api';

export interface MeResponse {
  id: string;
  email: string;
  full_name: string;
  locale: string;
  companies: string[];
  roles: string[];
  is_system: boolean;
}

export interface LoginResponse {
  access_token: string;
  token_type: 'Bearer';
  expires_in: number;
  companies: string[];
  roles: string[];
}

export async function login(email: string, password: string): Promise<LoginResponse> {
  const res = await api<LoginResponse>('/auth/login', {
    method: 'POST',
    body: { email, password },
  });
  setAccessToken(res.access_token);
  if (res.companies.length > 0 && res.companies[0]) setActiveCompany(res.companies[0]);
  scheduleRefresh(res.expires_in);
  return res;
}

export async function refresh(): Promise<LoginResponse | null> {
  try {
    const res = await api<LoginResponse>('/auth/refresh', { method: 'POST' });
    setAccessToken(res.access_token);
    if (res.companies.length > 0 && res.companies[0]) setActiveCompany(res.companies[0]);
    scheduleRefresh(res.expires_in);
    return res;
  } catch {
    setAccessToken(null);
    setActiveCompany(null);
    return null;
  }
}

export async function logout(): Promise<void> {
  try { await api('/auth/logout', { method: 'POST' }); } finally {
    setAccessToken(null);
    setActiveCompany(null);
  }
}

export const me = () => api<MeResponse>('/auth/me');

export const isAuthenticated = () => getAccessToken() !== null;

let refreshTimer: ReturnType<typeof setTimeout> | null = null;
function scheduleRefresh(seconds: number) {
  if (refreshTimer) clearTimeout(refreshTimer);
  const ms = Math.max(seconds - 60, 30) * 1000;
  refreshTimer = setTimeout(() => { void refresh(); }, ms);
}
