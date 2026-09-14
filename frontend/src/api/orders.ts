import { getJson, postJson } from './client'
import type { Order, OrderGroup, OrderPage, OrderStatus } from './types'

export type OrderDraft = { shop_id: number; items: Array<{ sku_id: number; quantity: number; expected_price_cent: number }> }

export function createOrder(addressId: number, drafts: OrderDraft[], idempotencyKey: string) {
  return postJson<OrderGroup>('/api/orders', { address_id: addressId, drafts }, {
    authRequired: true, headers: { 'Idempotency-Key': idempotencyKey },
  })
}

export function listOrders(status?: OrderStatus) {
  return getJson<OrderPage>(`/api/orders${status ? `?status=${status}` : ''}`, { authRequired: true })
}
export function getOrder(id: number) { return getJson<Order>(`/api/orders/${id}`, { authRequired: true }) }
export function cancelOrder(id: number) { return postJson<Order>(`/api/orders/${id}/cancel`, {}, { authRequired: true }) }
export function confirmReceipt(id: number) { return postJson<Order>(`/api/orders/${id}/confirm`, {}, { authRequired: true }) }
export function listSellerOrders(shopId: number, status?: OrderStatus) {
  const query = new URLSearchParams({ shop_id: String(shopId) })
  if (status) query.set('status', status)
  return getJson<OrderPage>(`/api/seller/orders?${query}`, { authRequired: true })
}
export function shipOrder(id: number) { return postJson<Order>(`/api/seller/orders/${id}/ship`, {}, { authRequired: true }) }
