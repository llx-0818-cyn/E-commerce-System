import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router'

import SellerView from './SellerView'

const mocks = vi.hoisted(() => ({ dashboard: vi.fn(), shop: vi.fn(), products: vi.fn(), detail: vi.fn(), orders: vi.fn(), stock: vi.fn(), ship: vi.fn() }))
vi.mock('../api/storefront', () => ({
  getDashboard: mocks.dashboard, getOwnedShop: mocks.shop, listSellerProducts: mocks.products, getSellerProduct: mocks.detail,
  adjustStock: mocks.stock, updateShop: vi.fn(), createShop: vi.fn(), setShopStatus: vi.fn(), createProduct: vi.fn(), createSKU: vi.fn(), setProductStatus: vi.fn(), setSKUStatus: vi.fn(),
}))
vi.mock('../api/orders', () => ({ listSellerOrders: mocks.orders, shipOrder: mocks.ship }))
vi.mock('../components/AppShell', () => ({ default: ({ children }: { children: ReactNode }) => <div>{children}</div> }))
afterEach(() => { cleanup(); vi.clearAllMocks() })

it('loads seller metrics, adjusts SKU stock and ships a paid order', async () => {
  const shop = { id: 3, name: '店铺', description: '', avatar_url: '', banner_url: '', status: 'open' }
  const product = { id: 5, name: '商品', subtitle: '', description: '', cover_url: '', status: 'on_sale' }
  const detail = { product, images: [], skus: [{ id: 8, sku_code: 'SKU', specs: { 规格: '标准' }, available_stock: 4, locked_stock: 0, sold_stock: 1, status: 'active' }] }
  mocks.dashboard.mockResolvedValue({ shop_id: 3, product_count: 1, sku_count: 1, available_stock: 4, locked_stock: 0, sold_stock: 1, pending_shipment_orders: 1, completed_orders: 0 })
  mocks.shop.mockResolvedValue(shop)
  mocks.products.mockResolvedValue({ products: [product] })
  mocks.detail.mockResolvedValue(detail)
  mocks.orders.mockResolvedValue({ orders: [{ id: 10, order_no: 'ORDER-10', total_cent: 9900, status: 'paid', items: [] }] })
  mocks.stock.mockResolvedValue({ ...detail.skus[0], available_stock: 5 })
  mocks.ship.mockResolvedValue({ id: 10, status: 'shipped' })
  render(<MemoryRouter><SellerView /></MemoryRouter>)
  expect((await screen.findAllByText('待发货')).length).toBeGreaterThan(0)
  fireEvent.click(screen.getByRole('button', { name: '商品与库存' }))
  await screen.findByText('SKU')
  fireEvent.click(screen.getByRole('button', { name: '+1' }))
  await waitFor(() => expect(mocks.stock).toHaveBeenCalledWith(8, 1))
  fireEvent.click(screen.getByRole('button', { name: '待发货' }))
  fireEvent.click(await screen.findByRole('button', { name: '发货' }))
  await waitFor(() => expect(mocks.ship).toHaveBeenCalledWith(10))
})
