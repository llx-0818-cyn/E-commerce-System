import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { MemoryRouter, Route, Routes } from 'react-router'

import OrderDetailView from './OrderDetailView'

const mocks = vi.hoisted(() => ({ get: vi.fn(), cancel: vi.fn(), confirm: vi.fn(), create: vi.fn(), success: vi.fn(), failure: vi.fn(), repeat: vi.fn() }))
vi.mock('../api/orders', () => ({ getOrder: mocks.get, cancelOrder: mocks.cancel, confirmReceipt: mocks.confirm }))
vi.mock('../api/payment', () => ({ createPayment: mocks.create, simulateSuccess: mocks.success, simulateFailure: mocks.failure, repeatCallback: mocks.repeat }))
vi.mock('../components/AppShell', () => ({ default: ({ children }: { children: ReactNode }) => <div>{children}</div> }))
afterEach(() => { cleanup(); vi.clearAllMocks() })

const order = (status: string) => ({ id: 8, order_no: 'ORDER-8', status, total_cent: 9900, address_snapshot: { recipient_name: 'Alice', phone: '138', province: '浙', city: '杭', district: '', detail: '路 1 号' }, items: [], created_at: '', updated_at: '', expire_at: '', shop_id: 3, buyer_id: 7, submission_id: 1 })
const renderView = () => render(<MemoryRouter initialEntries={['/orders/8']}><Routes><Route path="/orders/:id" element={<OrderDetailView />} /></Routes></MemoryRouter>)

describe('OrderDetailView', () => {
  it('shows the Mock warning and supports success plus duplicate callback', async () => {
    mocks.get.mockResolvedValue(order('pending_payment'))
    mocks.create.mockResolvedValue({ payment: { payment_no: 'PAY-8' } })
    mocks.success.mockResolvedValue({})
    mocks.repeat.mockResolvedValue({})
    renderView()
    expect((await screen.findAllByText('演示环境，不产生真实支付')).length).toBeGreaterThan(0)
    fireEvent.click(screen.getByRole('button', { name: '创建支付单' }))
    expect(await screen.findByText('PAY-8')).toBeTruthy()
    fireEvent.click(screen.getByRole('button', { name: '模拟成功' }))
    await waitFor(() => expect(mocks.success).toHaveBeenCalledWith('PAY-8'))
    fireEvent.click(screen.getByRole('button', { name: '重复回调' }))
    await waitFor(() => expect(mocks.repeat).toHaveBeenCalledWith('PAY-8'))
  })

  it('confirms receipt only for a shipped order', async () => {
    mocks.get.mockResolvedValue(order('shipped'))
    mocks.confirm.mockResolvedValue(order('completed'))
    renderView()
    fireEvent.click(await screen.findByRole('button', { name: '确认收货' }))
    await waitFor(() => expect(mocks.confirm).toHaveBeenCalledWith(8))
  })
})
