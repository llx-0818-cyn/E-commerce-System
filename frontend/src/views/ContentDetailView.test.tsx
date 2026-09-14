import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter, Route, Routes } from 'react-router'

import ContentDetailView from './ContentDetailView'

const mocks = vi.hoisted(() => ({
  getPost: vi.fn(), findAccount: vi.fn(), listComments: vi.fn(), getState: vi.fn(), setLike: vi.fn(), setFavorite: vi.fn(), createComment: vi.fn(), deleteComment: vi.fn(), getProduct: vi.fn(), follow: vi.fn(), unfollow: vi.fn(),
  auth: { isLoggedIn: true, claims: { account_id: 7, username: 'buyer' } },
}))
vi.mock('../api/content', () => ({ getPost: mocks.getPost }))
vi.mock('../api/account', () => ({ findById: mocks.findAccount }))
vi.mock('../api/interaction', () => ({ listComments: mocks.listComments, getState: mocks.getState, setLike: mocks.setLike, setFavorite: mocks.setFavorite, createComment: mocks.createComment, deleteComment: mocks.deleteComment }))
vi.mock('../api/storefront', () => ({ getProduct: mocks.getProduct }))
vi.mock('../stores/auth', () => { const useAuthStore = Object.assign((selector?: (state: typeof mocks.auth) => unknown) => selector ? selector(mocks.auth) : mocks.auth, { getState: () => mocks.auth }); return { useAuthStore } })
vi.mock('../stores/social', () => ({ useSocialStore: () => ({ isFollowing: () => false, follow: mocks.follow, unfollow: mocks.unfollow }) }))
vi.mock('../components/AppShell', () => ({ default: ({ children }: { children: ReactNode }) => <div>{children}</div> }))

afterEach(() => { cleanup(); vi.clearAllMocks() })

describe('ContentDetailView', () => {
  it('loads detail and supports like, follow and comment flow', async () => {
    mocks.getPost.mockResolvedValue({ id: 1, author_id: 2, title: '咖啡内容', body: '正文', status: 'published', likes_count: 0, favorites_count: 0, comments_count: 0, popularity: 0, published_at: '2026-08-24T12:00:00Z', created_at: '', updated_at: '', images: [], tags: [], product_ids: [] })
    mocks.findAccount.mockResolvedValue({ id: 2, username: 'alice' })
    mocks.listComments.mockResolvedValue({ comments: [], has_more: false })
    mocks.getState.mockResolvedValue({ is_liked: false, is_favorited: false })
    mocks.setLike.mockResolvedValue({ active: true, count: 1 })
    mocks.createComment.mockResolvedValue({ id: 3, post_id: 1, author_id: 7, content: '写得很好', status: 'active', created_at: '2026-08-24T12:00:00Z', updated_at: '', mentioned_usernames: [] })
    render(<MemoryRouter initialEntries={['/posts/1']}><Routes><Route path="/posts/:id" element={<ContentDetailView />} /></Routes></MemoryRouter>)
    expect(await screen.findByText('咖啡内容')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '点赞' }))
    await waitFor(() => expect(mocks.setLike).toHaveBeenCalledWith(1, true))
    fireEvent.click(screen.getByRole('button', { name: '关注' }))
    await waitFor(() => expect(mocks.follow).toHaveBeenCalledWith(2))
    fireEvent.change(screen.getByLabelText('评论内容'), { target: { value: '写得很好' } })
    fireEvent.click(screen.getByRole('button', { name: '发送' }))
    expect(await screen.findByText('写得很好')).toBeTruthy()
  })
})
