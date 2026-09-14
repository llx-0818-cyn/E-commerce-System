import { cleanup, render, screen } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, expect, it, vi } from 'vitest'
import { MemoryRouter, Route, Routes } from 'react-router'

import ShopView from './ShopView'

const mocks = vi.hoisted(() => ({ shop: vi.fn(), products: vi.fn() }))
vi.mock('../api/storefront', () => ({ getShop: mocks.shop, listShopProducts: mocks.products }))
vi.mock('../components/AppShell', () => ({ default: ({ children }: { children: ReactNode }) => <div>{children}</div> }))
afterEach(() => { cleanup(); vi.clearAllMocks() })

it('shows a closed shop without hiding its public products', async () => {
  mocks.shop.mockResolvedValue({ shop: { id: 4, name: '闭店', description: '资料仍可查看', avatar_url: '', banner_url: '', status: 'closed' }, product_count: 1, accepting_orders: false })
  mocks.products.mockResolvedValue({ products: [{ product: { id: 6, name: '公开商品', subtitle: '', cover_url: '', status: 'on_sale' }, min_price_cent: 1000, can_purchase: false }], has_more: false })
  render(<MemoryRouter initialEntries={['/shops/4']}><Routes><Route path="/shops/:id" element={<ShopView />} /></Routes></MemoryRouter>)
  expect(await screen.findByText('暂停营业')).toBeTruthy()
  expect(screen.getByText('公开商品')).toBeTruthy()
})
