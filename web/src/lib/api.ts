// Minimal fetch wrapper. Adds Authorization + X-Company-Id headers, decodes JSON,
// throws ApiError with the stable error code from the server.

export interface ApiError {
  status: number;
  code: string;
  message: string;
  fields?: Record<string, string>;
}

export interface ApiOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'DELETE' | 'PATCH';
  body?: unknown;
  companyId?: string;
  signal?: AbortSignal;
}

let accessToken: string | null = null;
let activeCompany: string | null = null;

export const setAccessToken = (t: string | null) => { accessToken = t; };
export const getAccessToken = () => accessToken;
export const setActiveCompany = (c: string | null) => { activeCompany = c; };
export const getActiveCompany = () => activeCompany;

export async function api<T>(path: string, opts: ApiOptions = {}): Promise<T> {
  const headers: Record<string, string> = {
    'Accept': 'application/json',
  };
  if (opts.body !== undefined) headers['Content-Type'] = 'application/json';
  if (accessToken) headers['Authorization'] = `Bearer ${accessToken}`;
  const co = opts.companyId ?? activeCompany;
  if (co) headers['X-Company-Id'] = co;

  const res = await fetch(`/api/v1${path}`, {
    method: opts.method ?? 'GET',
    headers,
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
    credentials: 'include',
    signal: opts.signal,
  });

  if (res.status === 204) return undefined as unknown as T;

  let payload: unknown;
  try { payload = await res.json(); } catch { payload = undefined; }

  if (!res.ok) {
    const err = (payload as { errors?: Array<{ value?: unknown; message?: string }>; detail?: string; status?: number; message?: string });
    const detail = err.errors?.[0]?.value as { code?: string; fields?: Record<string,string> } | undefined;
    const apiErr: ApiError = {
      status: res.status,
      code: detail?.code ?? 'internal',
      message: err.detail ?? err.message ?? res.statusText,
      fields: detail?.fields,
    };
    throw apiErr;
  }
  return payload as T;
}

/** Like api() but returns the raw response Blob. For PDFs and other binary payloads. */
export async function apiBlob(path: string, opts: ApiOptions = {}): Promise<Blob> {
  const headers: Record<string, string> = {};
  if (opts.body !== undefined) headers['Content-Type'] = 'application/json';
  if (accessToken) headers['Authorization'] = `Bearer ${accessToken}`;
  const co = opts.companyId ?? activeCompany;
  if (co) headers['X-Company-Id'] = co;

  const res = await fetch(`/api/v1${path}`, {
    method: opts.method ?? 'GET',
    headers,
    body: opts.body !== undefined ? JSON.stringify(opts.body) : undefined,
    credentials: 'include',
    signal: opts.signal,
  });
  if (!res.ok) {
    let msg = res.statusText;
    try { msg = (await res.json())?.detail ?? msg; } catch { /* not JSON */ }
    throw { status: res.status, code: 'http_error', message: msg } satisfies ApiError;
  }
  return res.blob();
}
