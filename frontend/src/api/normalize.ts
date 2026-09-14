import type { Account } from './types'

export function listOrEmpty<T>(value: T[] | null | undefined): T[] {
  return Array.isArray(value) ? value : []
}

export function normalizeAccount(value: Account | null | undefined): Account {
  return {
    id: Number(value?.id ?? 0),
    username: value?.username || '匿名用户',
    avatar_url: value?.avatar_url || '',
    bio: value?.bio || '',
  }
}
