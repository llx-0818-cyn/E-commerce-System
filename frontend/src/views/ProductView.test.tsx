import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, expect, it, vi } from 'vitest'
import { MemoryRouter, Route, Routes } from 'react-router'

import ProductView from './ProductView'

const mocks = vi.hoisted(() => ({ product: vi.fn(), related: vi.fn(), add: vi.fn(), success: vi.fn(), auth: { isLoggedIn: true } }))
vi.mock('../api/storefront', () => ({ getProduct: mocks.product, listRelatedPosts: mocks.related }))
vi.mock('../api/checkout', () => ({ addCartItem: mocks.add }))
vi.mock('../stores/auth', () => ({ useAuthStore: (selector: (state: typeof mocks.auth) => unknown) => selector(mocks.auth) }))
vi.mock('../stores/toast', () => ({ useToastStore: () => ({ success: mocks.success }) }))
vi.mock('../components/AppShell', () => ({ default: ({ children }: { children: ReactNode }) => <div>{children}</div> }))
afterEach(() => { cleanup(); vi.clearAllMocks() })

it('selects the first purchasable SKU, adds it to cart and links related content', async () => {
  mocks.product.mockResolvedValue({
    product: { id: 6, name: '多规格杯', subtitle: '', description: '详情', cover_url: '', status: 'on_sale' },
    shop: { shop: { id: 3, name: '咖啡店' }, accepting_orders: true }, images: [], min_price_cent: 6900, can_purchase: true,
    skus: [
      { id: 1, sku_code: 'OFF', specs: { 颜色: '灰' }, price_cent: 6900, available_stock: 5, status: 'inactive', can_purchase: false },
      { id: 2, sku_code: 'ON', specs: { 颜色: '白' }, price_cent: 7900, available_stock: 4, status: 'active', can_purchase: true },
    ],
  })
  mocks.related.mockResolvedValue({ posts: [{ id: 9, title: '杯子内容', body: '正文' }], has_more: false })
  mocks.add.mockResolvedValue({ id: 1 })
  render(<MemoryRouter initialEntries={['/products/6']}><Routes><Route path="/products/:id" element={<ProductView />} /></Routes></MemoryRouter>)
  expect(await screen.findByText('多规格杯')).toBeTruthy()
  expect(screen.getByText('79.00')).toBeTruthy()
  expect(screen.getByRole('button', { name: /颜色: 灰/ }).hasAttribute('disabled')).toBe(true)
  fireEvent.click(screen.getByRole('button', { name: '加入购物车' }))
  await waitFor(() => expect(mocks.add).toHaveBeenCalledWith({ sku_id: 2, quantity: 1, mode: 'merge' }))
  expect(screen.getByRole('link', { name: /杯子内容/ }).getAttribute('href')).toBe('/posts/9')
})
