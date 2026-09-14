import { getJson, patchJson, postJson, putJson } from './client'
import type { Product, ProductImage, ProductSKU, PublicProductDetail, PublicProductPage, PublicShop, RelatedPostPage, SellerDashboard, SellerProductDetail, SellerProductPage, Shop, ShopStatus, SKUStatus } from './types'

export function getShop(id: number) { return getJson<PublicShop>(`/api/shops/${id}`) }
export function getProduct(id: number) { return getJson<PublicProductDetail>(`/api/products/${id}`) }
export function listShopProducts(id: number, limit = 20) { return getJson<PublicProductPage>(`/api/shops/${id}/products?limit=${limit}`) }
export function listRelatedPosts(id: number, limit = 12) { return getJson<RelatedPostPage>(`/api/products/${id}/posts?limit=${limit}`) }
export function getDashboard() { return getJson<SellerDashboard>('/api/seller/dashboard', { authRequired: true }) }
export function getOwnedShop() { return getJson<Shop>('/api/seller/shop', { authRequired: true }) }
export function listSellerProducts(limit = 100) { return getJson<SellerProductPage>(`/api/seller/products?limit=${limit}`, { authRequired: true }) }
export function getSellerProduct(id: number) { return getJson<SellerProductDetail>(`/api/seller/products/${id}`, { authRequired: true }) }

export function createShop(input: { name: string; description?: string; avatar_url?: string; banner_url?: string }) {
  return postJson<Shop>('/api/shops', input, { authRequired: true })
}
export function updateShop(id: number, input: { name: string; description?: string; avatar_url?: string; banner_url?: string }) {
  return patchJson<Shop>(`/api/shops/${id}`, input, { authRequired: true })
}
export function setShopStatus(id: number, status: Exclude<ShopStatus, 'disabled'>) {
  return patchJson<Shop>(`/api/shops/${id}/status`, { status }, { authRequired: true })
}
export function createProduct(input: { name: string; subtitle?: string; description?: string; cover_url?: string; category_id?: number; images?: unknown[] }) {
  return postJson<Product>('/api/seller/products', input, { authRequired: true })
}
export function updateProduct(id: number, input: { name: string; subtitle?: string; description?: string; cover_url?: string; category_id?: number }) {
  return patchJson<Product>(`/api/seller/products/${id}`, input, { authRequired: true })
}
export function replaceProductImages(id: number, images: unknown[]) {
  return putJson<{ images: ProductImage[] }>(`/api/seller/products/${id}/images`, { images }, { authRequired: true })
}
export function setProductStatus(id: number, status: 'on_sale' | 'off_sale') {
  return patchJson<Product>(`/api/seller/products/${id}/status`, { status }, { authRequired: true })
}
export function createSKU(productId: number, input: { sku_code: string; specs?: Record<string, string>; price_cent: number; original_price_cent?: number; available_stock?: number }) {
  return postJson<ProductSKU>(`/api/seller/products/${productId}/skus`, input, { authRequired: true })
}
export function updateSKU(id: number, input: { sku_code: string; specs?: Record<string, string>; price_cent: number; original_price_cent?: number }) {
  return patchJson<ProductSKU>(`/api/seller/skus/${id}`, input, { authRequired: true })
}
export function setSKUStatus(id: number, status: Exclude<SKUStatus, 'disabled'>) {
  return patchJson<ProductSKU>(`/api/seller/skus/${id}/status`, { status }, { authRequired: true })
}
export function adjustStock(id: number, delta: number) {
  return postJson<ProductSKU>(`/api/seller/skus/${id}/stock-adjustments`, { delta }, { authRequired: true })
}
