import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router'

import PublishPostView, { moveItem } from './PublishPostView'

const mocks = vi.hoisted(() => ({ createPost: vi.fn(), dashboard: vi.fn(), products: vi.fn() }))
vi.mock('../api/content', () => ({ createPost: mocks.createPost, uploadImage: vi.fn() }))
vi.mock('../api/storefront', () => ({ getDashboard: mocks.dashboard, listShopProducts: mocks.products }))
vi.mock('../components/AppShell', () => ({ default: ({ children }: { children: ReactNode }) => <div>{children}</div> }))

afterEach(() => { cleanup(); vi.clearAllMocks() })

describe('PublishPostView', () => {
  it('publishes a text post with normalized tags', async () => {
    mocks.dashboard.mockRejectedValue(new Error('no shop'))
    mocks.createPost.mockResolvedValue({ id: 9 })
    render(<MemoryRouter><PublishPostView /></MemoryRouter>)
    fireEvent.change(screen.getByLabelText('标题'), { target: { value: '新内容' } })
    fireEvent.change(screen.getByLabelText('正文'), { target: { value: '正文内容' } })
    fireEvent.change(screen.getByLabelText('标签'), { target: { value: '咖啡, 日常' } })
    fireEvent.click(screen.getByRole('button', { name: '发布' }))
    await waitFor(() => expect(mocks.createPost).toHaveBeenCalledWith({ title: '新内容', body: '正文内容', image_ids: [], tags: ['咖啡', '日常'], product_ids: [] }))
  })
})

describe('moveItem', () => {
  it('reorders uploaded images without losing items', () => {
    expect(moveItem([1, 2, 3], 0, 2)).toEqual([2, 3, 1])
  })
})
