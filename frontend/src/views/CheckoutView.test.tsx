import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router'

import CheckoutView from './CheckoutView'

const mocks = vi.hoisted(() => ({ cart: vi.fn(), addresses: vi.fn(), preview: vi.fn(), createAddress: vi.fn(), createOrder: vi.fn() }))
vi.mock('../api/checkout', () => ({ listCart: mocks.cart, listAddresses: mocks.addresses, preview: mocks.preview, createAddress: mocks.createAddress }))
vi.mock('../api/orders', () => ({ createOrder: mocks.createOrder }))
vi.mock('../components/AppShell', () => ({ default: ({ children }: { children: ReactNode }) => <div>{children}</div> }))

const address = { id: 4, account_id: 7, recipient_name: 'Alice', phone: '138', province: '浙', city: '杭', district: '', detail: '路 1 号', postal_code: '', is_default: true, created_at: '', updated_at: '' }
const cartItems = [{ id: 1, account_id: 7, sku_id: 9, quantity: 1, observed_price_cent: 9000, created_at: '', updated_at: '' }]
const item = (shop: number, changed = false, issue?: string) => ({ cart_item_id: shop, shop_id: shop, product_id: shop, product_name: `商品${shop}`, cover_url: '', sku_id: shop + 10, sku_code: `SKU-${shop}`, specs: {}, quantity: 1, observed_price_cent: 9000, current_price_cent: 9900, subtotal_cent: 9900, price_changed: changed, can_checkout: !issue, issue })

beforeEach(() => { mocks.cart.mockResolvedValue({ items: cartItems }); mocks.addresses.mockResolvedValue({ addresses: [address] }); vi.stubGlobal('crypto', { randomUUID: () => 'checkout-key' }) })
afterEach(() => { cleanup(); vi.clearAllMocks(); vi.unstubAllGlobals() })

describe('CheckoutView', () => {
  it('confirms price changes and submits two shop drafts', async () => {
    mocks.preview
      .mockResolvedValueOnce({ address, shops: [{ shop_id: 1, shop_name: '店一', items: [item(1, true)], subtotal_cent: 9900 }, { shop_id: 2, shop_name: '店二', items: [item(2)], subtotal_cent: 9900 }], total_cent: 19800, has_price_changes: true, has_unavailable_items: false, can_submit: false })
      .mockResolvedValueOnce({ address, shops: [{ shop_id: 1, shop_name: '店一', items: [item(1, true)], subtotal_cent: 9900 }, { shop_id: 2, shop_name: '店二', items: [item(2)], subtotal_cent: 9900 }], total_cent: 19800, has_price_changes: true, has_unavailable_items: false, can_submit: true })
    mocks.createOrder.mockResolvedValue({ submission: { id: 1 }, orders: [{ id: 8, order_no: 'A' }, { id: 9, order_no: 'B' }] })
    render(<MemoryRouter><CheckoutView /></MemoryRouter>)
    expect(await screen.findByText('店一')).toBeTruthy()
    expect(screen.getByText('店二')).toBeTruthy()
    fireEvent.click(screen.getByRole('checkbox', { name: '接受最新价格' }))
    await waitFor(() => expect(mocks.preview).toHaveBeenCalledTimes(2))
    fireEvent.click(screen.getByRole('button', { name: '提交订单' }))
    await waitFor(() => expect(mocks.createOrder).toHaveBeenCalledWith(4, [
      { shop_id: 1, items: [{ sku_id: 11, quantity: 1, expected_price_cent: 9900 }] },
      { shop_id: 2, items: [{ sku_id: 12, quantity: 1, expected_price_cent: 9900 }] },
    ], 'checkout-key'))
  })

  it('shows stock shortage and keeps submit disabled', async () => {
    mocks.preview.mockResolvedValue({ address, shops: [{ shop_id: 1, shop_name: '店一', items: [item(1, false, 'stock_insufficient')], subtotal_cent: 9900 }], total_cent: 9900, has_price_changes: false, has_unavailable_items: true, can_submit: false })
    render(<MemoryRouter><CheckoutView /></MemoryRouter>)
    expect(await screen.findByText('库存不足')).toBeTruthy()
    expect(screen.getByRole('button', { name: '提交订单' }).hasAttribute('disabled')).toBe(true)
  })
})
