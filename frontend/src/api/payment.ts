import { getJson, postJson } from './client'
import type { PaymentSession } from './types'

export function createPayment(orderId: number, amountCent: number) {
  return postJson<PaymentSession>('/api/payments', { order_id: orderId, amount_cent: amountCent }, { authRequired: true })
}
export function getPayment(paymentNo: string) { return getJson<PaymentSession>(`/api/payments/${paymentNo}`, { authRequired: true }) }
export function simulateSuccess(paymentNo: string) { return postJson<unknown>(`/api/mock-payments/${paymentNo}/succeed`, {}, { authRequired: true }) }
export function simulateFailure(paymentNo: string) { return postJson<unknown>(`/api/mock-payments/${paymentNo}/fail`, {}, { authRequired: true }) }
export function repeatCallback(paymentNo: string) { return postJson<unknown>(`/api/mock-payments/${paymentNo}/repeat`, {}, { authRequired: true }) }
