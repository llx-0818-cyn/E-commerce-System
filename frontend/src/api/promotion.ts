import { getJson, postJson } from './client'
import type { FlashSaleReservation } from './types'

export function accept(campaignId: number, idempotencyKey: string, quantity = 1) {
  return postJson<FlashSaleReservation>(`/api/flash-sales/${campaignId}/requests`, { idempotency_key: idempotencyKey, quantity }, { authRequired: true })
}
export function getResult(requestId: string) { return getJson<FlashSaleReservation>(`/api/flash-sale-requests/${requestId}`, { authRequired: true }) }
