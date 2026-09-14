import { deleteJson, getJson, postJson, putJson } from './client'
import type { ContentPost, InteractionStateResult, PostComment, PostCommentPage, PostInteractionState } from './types'

export function getState(postId: number) { return getJson<PostInteractionState>(`/api/posts/${postId}/interaction`, { authRequired: true }) }
export function setLike(postId: number, active: boolean) { return putJson<InteractionStateResult>(`/api/posts/${postId}/like`, { active }, { authRequired: true }) }
export function setFavorite(postId: number, active: boolean) { return putJson<InteractionStateResult>(`/api/posts/${postId}/favorite`, { active }, { authRequired: true }) }
export function listFavorites() { return getJson<{ posts: ContentPost[]; has_more: boolean }>('/api/favorites', { authRequired: true }) }
export function listComments(postId: number, limit = 30) { return getJson<PostCommentPage>(`/api/posts/${postId}/comments?limit=${limit}`) }
export function createComment(postId: number, content: string) { return postJson<PostComment>(`/api/posts/${postId}/comments`, { content }, { authRequired: true }) }
export function deleteComment(id: number) { return deleteJson<{ message: string }>(`/api/comments/${id}`, { authRequired: true }) }
