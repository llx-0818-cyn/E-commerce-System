import { useAuthStore } from '../stores/auth'
import { reportError } from '../utils/error-reporter'

export class ApiError extends Error {
  status: number
  payload?: unknown

  constructor(message: string, status: number, payload?: unknown) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.payload = payload
  }
}

type ApiErrorBody = { error?: string }
type RequestOptions = {
  authRequired?: boolean
  headers?: Record<string, string>
  body?: unknown
  form?: FormData
}

const API_BASE = (import.meta.env.VITE_API_BASE as string | undefined) ?? '/api'

let refreshPromise: Promise<string | null> | null = null

async function tryRefresh(): Promise<string | null> {
  if (refreshPromise) return refreshPromise
  const auth = useAuthStore.getState()
  if (!auth.refreshToken) return null
  refreshPromise = (async () => {
    try {
      const response = await fetch(`${API_BASE}/account/refresh`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ refresh_token: auth.refreshToken }),
      })
      if (!response.ok) {
        auth.clearTokens()
        return null
      }
      const data = await response.json() as { token: string }
      auth.setToken(data.token)
      return data.token
    } catch {
      auth.clearTokens()
      return null
    } finally {
      refreshPromise = null
    }
  })()
  return refreshPromise
}

export async function request<T>(method: string, path: string, options: RequestOptions = {}): Promise<T> {
  const auth = useAuthStore.getState()
  if (options.authRequired && !auth.token) throw new ApiError('需要先登录', 401)

  const headers: Record<string, string> = { ...options.headers }
  if (!options.form && options.body !== undefined) headers['Content-Type'] = 'application/json'
  if (auth.token) headers.Authorization = `Bearer ${auth.token}`
  const body = options.form ?? (options.body === undefined ? undefined : JSON.stringify(options.body))

  let response = await fetch(`${API_BASE}${path}`, { method, headers, body })
  if (response.status === 401 && path !== '/account/refresh' && auth.refreshToken) {
    const token = await tryRefresh()
    if (token) {
      headers.Authorization = `Bearer ${token}`
      response = await fetch(`${API_BASE}${path}`, { method, headers, body })
    }
  }
  return handleResponse<T>(response, path)
}

export function getJson<T>(path: string, options?: Omit<RequestOptions, 'body' | 'form'>) {
  return request<T>('GET', path, options)
}

export function postJson<T>(path: string, body: unknown, options?: Omit<RequestOptions, 'body' | 'form'>) {
  return request<T>('POST', path, { ...options, body })
}

export function patchJson<T>(path: string, body: unknown, options?: Omit<RequestOptions, 'body' | 'form'>) {
  return request<T>('PATCH', path, { ...options, body })
}

export function putJson<T>(path: string, body: unknown, options?: Omit<RequestOptions, 'body' | 'form'>) {
  return request<T>('PUT', path, { ...options, body })
}

export function deleteJson<T>(path: string, options?: Omit<RequestOptions, 'body' | 'form'>) {
  return request<T>('DELETE', path, options)
}

export function postForm<T>(path: string, form: FormData, options?: Omit<RequestOptions, 'body' | 'form'>) {
  return request<T>('POST', path, { ...options, form })
}

async function handleResponse<T>(response: Response, path: string): Promise<T> {
  const text = await response.text()
  let data: unknown = null
  if (text) {
    try { data = JSON.parse(text) } catch { data = text }
  }
  if (!response.ok) {
    if (response.status === 401) useAuthStore.getState().clearTokens()
    const message = data && typeof data === 'object' && (data as ApiErrorBody).error
      ? String((data as ApiErrorBody).error)
      : `请求失败 (${response.status})`
    const error = new ApiError(message, response.status, data)
    reportError(error, { path, status: response.status })
    throw error
  }
  return data as T
}
