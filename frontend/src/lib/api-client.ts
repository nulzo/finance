import { config } from "@/config/env"
import { useAuthStore } from "@/stores/auth"

/** Shape thrown on any non-2xx or network-level failure. Both react-query
 *  and feature components catch against this. Keeping it a plain object
 *  (not an Error subclass) matches the previous axios-based error shape. */
export interface ApiErrorShape {
  status: number
  message: string
  body?: unknown
}

export type Params = Record<
  string,
  string | number | boolean | null | undefined
>

export interface RequestOptions {
  params?: Params
  body?: unknown
  signal?: AbortSignal
  headers?: Record<string, string>
  /** Override the JSON content-type for multipart / form payloads. */
  rawBody?: BodyInit
}

function buildQuery(params?: Params): string {
  if (!params) return ""
  const sp = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v === undefined || v === null || v === "") continue
    sp.set(k, String(v))
  }
  const s = sp.toString()
  return s ? `?${s}` : ""
}

async function request<T>(
  method: string,
  path: string,
  opts: RequestOptions = {},
): Promise<T> {
  const token = useAuthStore.getState().token
  const headers: Record<string, string> = {
    Accept: "application/json",
    ...(opts.headers ?? {}),
  }
  if (token) headers["Authorization"] = `Bearer ${token}`

  let body: BodyInit | undefined
  if (opts.rawBody != null) {
    body = opts.rawBody
  } else if (opts.body !== undefined) {
    headers["Content-Type"] ??= "application/json"
    body = JSON.stringify(opts.body)
  }

  const url = `${config.apiUrl}${path}${buildQuery(opts.params)}`

  let res: Response
  try {
    res = await fetch(url, { method, headers, body, signal: opts.signal })
  } catch (e) {
    throw {
      status: 0,
      message:
        e instanceof Error ? e.message : "Network error talking to trader API",
    } satisfies ApiErrorShape
  }

  // No content
  if (res.status === 204) return undefined as T

  const ct = res.headers.get("content-type") ?? ""
  let parsed: unknown = null
  if (ct.includes("application/json")) {
    parsed = await res.json().catch(() => null)
  } else {
    const txt = await res.text().catch(() => "")
    parsed = txt || null
  }

  if (!res.ok) {
    const message =
      (parsed as { error?: string } | null)?.error ??
      (typeof parsed === "string" ? parsed : "") ??
      `HTTP ${res.status}`
    throw {
      status: res.status,
      message: message || `HTTP ${res.status}`,
      body: parsed,
    } satisfies ApiErrorShape
  }

  return parsed as T
}

export const api = {
  get: <T>(path: string, opts?: RequestOptions) =>
    request<T>("GET", path, opts),
  post: <T>(path: string, body?: unknown, opts?: RequestOptions) =>
    request<T>("POST", path, { ...opts, body }),
  put: <T>(path: string, body?: unknown, opts?: RequestOptions) =>
    request<T>("PUT", path, { ...opts, body }),
  patch: <T>(path: string, body?: unknown, opts?: RequestOptions) =>
    request<T>("PATCH", path, { ...opts, body }),
  del: <T>(path: string, opts?: RequestOptions) =>
    request<T>("DELETE", path, opts),
}
