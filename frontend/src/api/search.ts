import { getJson } from './client'
import type { SearchPage, SearchScope } from './types'

export function search(query: { q: string; scope?: SearchScope; limit?: number; offset?: number }) {
  const params = new URLSearchParams({ q: query.q, scope: query.scope ?? 'content', limit: String(query.limit ?? 20), offset: String(query.offset ?? 0) })
  return getJson<SearchPage>(`/api/search?${params}`)
}
