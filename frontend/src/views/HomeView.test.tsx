import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router'

import type { ContentFeedItem } from '../api/types'
import HomeView, { mergeFeedItems } from './HomeView'

const mocks = vi.hoisted(() => ({
  latest: vi.fn(), following: vi.fn(), popular: vi.fn(), auth: { isLoggedIn: true },
}))

vi.mock('../api/content', () => ({ listLatest: mocks.latest, listFollowing: mocks.following, listPopular: mocks.popular }))
vi.mock('../stores/auth', () => {
  const useAuthStore = Object.assign((selector: (state: typeof mocks.auth) => unknown) => selector(mocks.auth), { getState: () => mocks.auth })
  return { useAuthStore }
})
vi.mock('../components/AppShell', () => ({ default: ({ children }: { children: ReactNode }) => <div>{children}</div> }))
vi.mock('../components/ContentFeedCard', () => ({ default: ({ item }: { item: ContentFeedItem }) => <article data-testid={`feed-item-${item.id}`}>{item.title}</article> }))

afterEach(() => { cleanup(); vi.clearAllMocks(); mocks.auth.isLoggedIn = true })

const item = (id: number, title = `内容 ${id}`): ContentFeedItem => ({
  id, title, body: title, author: { id: 1, username: 'alice' }, images: [], tags: [], products: [],
  likes_count: 0, favorites_count: 0, comments_count: 0, popularity: 0,
  published_at: '2026-08-24T12:00:00Z', is_liked: false, is_favorited: false,
})

const renderView = () => render(<MemoryRouter><HomeView /></MemoryRouter>)

describe('HomeView', () => {
  it('switches from latest to popular feed', async () => {
    mocks.latest.mockResolvedValue({ items: [item(1, '推荐内容')], has_more: false })
    mocks.popular.mockResolvedValue({ items: [item(2, '热门内容')], has_more: false, next_offset: 1, degraded: false })
    renderView()
    expect(await screen.findByText('推荐内容')).toBeTruthy()
    fireEvent.click(screen.getByRole('tab', { name: '热门' }))
    expect(await screen.findByText('热门内容')).toBeTruthy()
    expect(mocks.popular).toHaveBeenCalledTimes(1)
  })

  it('loads the next page and removes duplicate ids', async () => {
    mocks.latest
      .mockResolvedValueOnce({ items: [item(1)], has_more: true, next_cursor: { published_at: '2026-08-24T12:00:00Z', id: 1 } })
      .mockResolvedValueOnce({ items: [item(1), item(2)], has_more: false })
    renderView()
    await screen.findByTestId('feed-item-1')
    fireEvent.click(screen.getByRole('button', { name: '加载更多' }))
    await screen.findByTestId('feed-item-2')
    expect(screen.getAllByTestId('feed-item-1')).toHaveLength(1)
    expect(mocks.latest.mock.calls[1]?.[1]).toEqual({ published_at: '2026-08-24T12:00:00Z', id: 1 })
  })

  it('shows an error and retries', async () => {
    mocks.latest.mockRejectedValueOnce(new Error('网络不可用')).mockResolvedValueOnce({ items: [item(3, '恢复内容')], has_more: false })
    renderView()
    expect(await screen.findByText('网络不可用')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '重试' }))
    await waitFor(() => expect(screen.getByText('恢复内容')).toBeTruthy())
    expect(mocks.latest).toHaveBeenCalledTimes(2)
  })
})

describe('mergeFeedItems', () => {
  it('preserves order while removing ids already present', () => {
    expect(mergeFeedItems([item(1)], [item(1), item(2)]).map((value) => value.id)).toEqual([1, 2])
  })
})
