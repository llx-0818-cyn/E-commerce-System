import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router'

import OrdersView from './OrdersView'

const mocks = vi.hoisted(() => ({ list: vi.fn() }))
vi.mock('../api/orders', () => ({ listOrders: mocks.list }))
vi.mock('../components/AppShell', () => ({ default: ({ children }: { children: ReactNode }) => <div>{children}</div> }))
afterEach(() => { cleanup(); vi.clearAllMocks() })

it('filters buyer orders by status', async () => {
  mocks.list.mockResolvedValue({ orders: [{ id: 1, order_no: 'ORDER-1', shop_id: 3, status: 'paid', total_cent: 9900, created_at: '2026-08-24T12:00:00Z', items: [] }], has_more: false })
  render(<MemoryRouter><OrdersView /></MemoryRouter>)
  expect(await screen.findByText('ORDER-1')).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: '待发货' }))
  await waitFor(() => expect(mocks.list).toHaveBeenLastCalledWith('paid'))
})
