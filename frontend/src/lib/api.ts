/**
 * HTTP client for the Go API. JSON in and out, access token in memory, rotating
 * refresh cookie, branch header, and the `{ data, meta }` / `{ error }` envelopes.
 */
export class ApiError extends Error {
  constructor(
    message: string,
    public status: number,
    public code = "error",
    public fields: Record<string, string> = {},
  ) {
    super(message);
    this.name = "ApiError";
  }
}

export interface Meta {
  page: number;
  per_page: number;
  total: number;
}
export interface Page<T> {
  data: T[];
  meta: Meta;
}

const API_PREFIX = "/api/v1";

function baseUrl() {
  // Empty by default: requests go to the same origin, and the Vite dev server proxies /api.
  return ((import.meta.env.VITE_API_BASE_URL as string | undefined) ?? "").replace(/\/$/, "");
}

let accessToken: string | null = null;
let branchId: string | null = null;
let onSessionLost: (() => void) | null = null;
let refreshing: Promise<boolean> | null = null;

export const session = {
  setToken: (token: string | null) => {
    accessToken = token;
  },
  getToken: () => accessToken,
  setBranch: (id: string | null) => {
    branchId = id;
  },
  onLost: (fn: (() => void) | null) => {
    onSessionLost = fn;
  },
};

export function newIdempotencyKey() {
  return crypto.randomUUID();
}

type Query = Record<string, string | number | boolean | undefined | null>;
export interface RequestOptions {
  method?: string;
  body?: unknown;
  query?: Query;
  idempotencyKey?: string;
  signal?: AbortSignal;
  /** Do not try to refresh the session on 401 (used by auth calls). */
  noRefresh?: boolean;
}

function buildUrl(path: string, query?: Query) {
  const url = `${baseUrl()}${API_PREFIX}${path}`;
  if (!query) return url;
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value !== undefined && value !== null && value !== "") params.set(key, String(value));
  }
  const text = params.toString();
  return text ? `${url}?${text}` : url;
}

export function refreshSession(): Promise<boolean> {
  if (refreshing) return refreshing;
  const attempt = (async () => {
    try {
      const response = await fetch(buildUrl("/auth/refresh"), {
        method: "POST",
        credentials: "include",
      });
      if (!response.ok) return false;
      const json = (await response.json()) as { data: { access_token: string } };
      accessToken = json.data.access_token;
      return true;
    } catch {
      return false;
    }
  })();
  // Concurrent callers share one attempt: the server rotates the cookie, so a second
  // parallel refresh would look like token reuse and end the session.
  refreshing = attempt;
  void attempt.finally(() => {
    if (refreshing === attempt) refreshing = null;
  });
  return attempt;
}

async function send(path: string, options: RequestOptions): Promise<Response> {
  const headers: Record<string, string> = {};
  if (options.body !== undefined) headers["Content-Type"] = "application/json";
  if (accessToken) headers.Authorization = `Bearer ${accessToken}`;
  if (branchId) headers["X-Branch-Id"] = branchId;
  if (options.idempotencyKey) headers["Idempotency-Key"] = options.idempotencyKey;
  try {
    return await fetch(buildUrl(path, options.query), {
      method: options.method ?? "GET",
      headers,
      credentials: "include",
      signal: options.signal,
      body: options.body === undefined ? undefined : JSON.stringify(options.body),
    });
  } catch {
    throw new ApiError("Cannot reach the server. Check your connection.", 0, "network");
  }
}

export async function request<T>(path: string, options: RequestOptions = {}): Promise<T> {
  let response = await send(path, options);
  if (response.status === 401 && !options.noRefresh) {
    if (await refreshSession()) {
      response = await send(path, options);
    }
    if (response.status === 401) onSessionLost?.();
  }
  if (response.status === 204) return undefined as T;
  let json: unknown = null;
  try {
    json = await response.json();
  } catch {
    if (response.ok) throw new ApiError("The server returned invalid JSON", response.status);
  }
  if (!response.ok) {
    const err = (
      json as {
        error?: { code?: string; message?: string; fields?: Record<string, string> };
      } | null
    )?.error;
    throw new ApiError(
      err?.message ?? response.statusText ?? "Request failed",
      response.status,
      err?.code,
      err?.fields,
    );
  }
  return json as T;
}

/** Unwraps `{ data }`. */
export async function get<T>(path: string, query?: Query, signal?: AbortSignal): Promise<T> {
  const json = await request<{ data: T }>(path, { query, signal });
  return json.data;
}

export function getPage<T>(path: string, query?: Query, signal?: AbortSignal): Promise<Page<T>> {
  return request<Page<T>>(path, { query, signal });
}

export interface WriteResult<T> {
  data: T;
  status: number;
}

/** POST, PUT or DELETE. Returns the unwrapped payload. */
export async function send_<T>(
  method: "POST" | "PUT" | "DELETE",
  path: string,
  body?: unknown,
  options: Pick<RequestOptions, "idempotencyKey" | "noRefresh"> = {},
): Promise<T> {
  const json = await request<{ data: T } | undefined>(path, { method, body, ...options });
  return json?.data as T;
}

export const api = {
  get,
  getPage,
  post: <T>(
    path: string,
    body?: unknown,
    options?: Pick<RequestOptions, "idempotencyKey" | "noRefresh">,
  ) => send_<T>("POST", path, body, options),
  put: <T>(path: string, body?: unknown) => send_<T>("PUT", path, body),
  del: <T>(path: string) => send_<T>("DELETE", path),
};

/** The backend answers 202 with an approval request when an action needs sign-off. */
export function isPendingApproval(
  value: unknown,
): value is { approval_request_id: string; status: "pending_approval" } {
  return (
    typeof value === "object" &&
    value !== null &&
    (value as { status?: string }).status === "pending_approval"
  );
}

/** Download a file (PDF, CSV, XLSX) from an authenticated endpoint and hand it to the browser. */
export async function download(path: string, query: Query, filename: string) {
  let response = await send(path, { query });
  if (response.status === 401 && (await refreshSession())) response = await send(path, { query });
  if (!response.ok) {
    let message = "Could not download the file";
    try {
      const json = (await response.json()) as { error?: { message?: string } };
      message = json.error?.message ?? message;
    } catch {
      // keep the default message
    }
    throw new ApiError(message, response.status);
  }
  const url = URL.createObjectURL(await response.blob());
  const link = document.createElement("a");
  link.href = url;
  link.download = filename;
  link.click();
  URL.revokeObjectURL(url);
}

/** Upload one file as multipart form data. Returns the stored file id. */
export async function uploadFile(file: File): Promise<string> {
  const send = () => {
    const form = new FormData();
    form.append("file", file);
    const headers: Record<string, string> = {};
    if (accessToken) headers.Authorization = `Bearer ${accessToken}`;
    if (branchId) headers["X-Branch-Id"] = branchId;
    return fetch(buildUrl("/files"), {
      method: "POST",
      headers,
      credentials: "include",
      body: form,
    });
  };
  let response: Response;
  try {
    response = await send();
    if (response.status === 401 && (await refreshSession())) response = await send();
  } catch {
    throw new ApiError("Cannot reach the server. Check your connection.", 0, "network");
  }
  const json = (await response.json().catch(() => null)) as {
    data?: { id: string };
    error?: { code?: string; message?: string };
  } | null;
  if (!response.ok || !json?.data) {
    throw new ApiError(
      json?.error?.message ?? "Could not upload the file",
      response.status,
      json?.error?.code,
    );
  }
  return json.data.id;
}

/** Fetch an authenticated binary file (image, PDF) as a temporary object URL for inline preview. */
export async function fetchFileBlob(
  path: string,
  signal?: AbortSignal,
): Promise<{ url: string; mime: string }> {
  let response = await send(path, { signal });
  if (response.status === 401 && (await refreshSession())) response = await send(path, { signal });
  if (!response.ok) {
    throw new ApiError("Could not load the file preview", response.status);
  }
  const blob = await response.blob();
  return { url: URL.createObjectURL(blob), mime: blob.type || response.headers.get("Content-Type") || "" };
}

/** Absolute URL for a public (no sign-in) endpoint, for use in img/iframe/link tags. */
export function publicUrl(path: string) {
  return buildUrl(path);
}
