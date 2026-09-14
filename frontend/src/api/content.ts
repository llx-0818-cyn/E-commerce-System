import { deleteJson, getJson, patchJson, postForm, postJson } from './client'
import type { ContentFeedPage, ContentPost, PopularContentFeedPage, PostCursor, PostImage } from './types'

function cursorQuery(cursor?: PostCursor) {
  return cursor ? `&before_time=${encodeURIComponent(cursor.published_at)}&before_id=${cursor.id}` : ''
}

export function listLatest(limit = 20, cursor?: PostCursor) {
  return getJson<ContentFeedPage>(`/api/feeds/latest?limit=${limit}${cursorQuery(cursor)}`)
}

export function listFollowing(limit = 20, cursor?: PostCursor) {
  return getJson<ContentFeedPage>(`/api/feeds/following?limit=${limit}${cursorQuery(cursor)}`, { authRequired: true })
}

export function listByTag(tag: string, limit = 20, cursor?: PostCursor) {
  return getJson<ContentFeedPage>(`/api/feeds/tags/${encodeURIComponent(tag)}?limit=${limit}${cursorQuery(cursor)}`)
}

export function listPopular(params: { limit?: number; snapshotAt?: string; offset?: number } = {}) {
  const query = new URLSearchParams({ limit: String(params.limit ?? 20), offset: String(params.offset ?? 0) })
  if (params.snapshotAt) query.set('snapshot_at', params.snapshotAt)
  return getJson<PopularContentFeedPage>(`/api/feeds/popular?${query}`)
}

export function getPost(id: number) {
  return getJson<ContentPost>(`/api/posts/${id}`)
}

export function createPost(input: { title: string; body: string; image_ids?: number[]; tags?: string[]; product_ids?: number[] }) {
  return postJson<ContentPost>('/api/posts', input, { authRequired: true })
}

export function updatePost(id: number, input: { title: string; body: string; image_ids?: number[]; tags?: string[]; product_ids?: number[] }) {
  return patchJson<ContentPost>(`/api/posts/${id}`, input, { authRequired: true })
}

export function deletePost(id: number) {
  return deleteJson<{ message: string }>(`/api/posts/${id}`, { authRequired: true })
}

export function uploadImage(file: File) {
  const form = new FormData()
  form.append('file', file)
  return postForm<PostImage>('/api/images', form, { authRequired: true })
}
