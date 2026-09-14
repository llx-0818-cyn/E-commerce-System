import { deleteJson, getJson, patchJson, postJson, putJson } from './client'
import type { Address, CartItem, CheckoutPreview } from './types'

export function listCart() { return getJson<{ items: CartItem[] }>('/api/cart', { authRequired: true }) }
export function addCartItem(input: { sku_id: number; quantity: number; mode?: 'merge' | 'replace'; source_post_id?: number }) {
  return postJson<CartItem>('/api/cart/items', input, { authRequired: true })
}
export function updateCartItem(id: number, quantity: number) { return patchJson<CartItem>(`/api/cart/items/${id}`, { quantity }, { authRequired: true }) }
export function deleteCartItem(id: number) { return deleteJson<{ message: string }>(`/api/cart/items/${id}`, { authRequired: true }) }

export function listAddresses() { return getJson<{ addresses: Address[] }>('/api/addresses', { authRequired: true }) }
export function createAddress(input: Omit<Address, 'id' | 'account_id' | 'created_at' | 'updated_at'>) { return postJson<Address>('/api/addresses', input, { authRequired: true }) }
export function updateAddress(id: number, input: Partial<Address>) { return patchJson<Address>(`/api/addresses/${id}`, input, { authRequired: true }) }
export function deleteAddress(id: number) { return deleteJson<{ message: string }>(`/api/addresses/${id}`, { authRequired: true }) }
export function setDefaultAddress(id: number) { return putJson<Address>(`/api/addresses/${id}/default`, {}, { authRequired: true }) }
export function preview(input: { cart_item_ids?: number[]; address_id?: number; accept_price_changes?: boolean }) {
  return postJson<CheckoutPreview>('/api/checkout/preview', input, { authRequired: true })
}
