import { cleanup, fireEvent, render, screen, waitFor } from '@testing-library/react'
import type { ReactNode } from 'react'
import { afterEach, expect, it, vi } from 'vitest'
import { MemoryRouter } from 'react-router'

import CartView from './CartView'

const mocks = vi.hoisted(() => ({ list: vi.fn(), update: vi.fn(), remove: vi.fn() }))
vi.mock('../api/checkout', () => ({ listCart: mocks.list, updateCartItem: mocks.update, deleteCartItem: mocks.remove }))
vi.mock('../components/AppShell', () => ({ default: ({ children }: { children: ReactNode }) => <div>{children}</div> }))
afterEach(() => { cleanup(); vi.clearAllMocks() })

it('updates quantity and removes a cart item', async () => {
  const cart = { id: 1, account_id: 7, sku_id: 9, quantity: 1, observed_price_cent: 9900, created_at: '', updated_at: '' }
  mocks.list.mockResolvedValue({ items: [cart] })
  mocks.update.mockResolvedValue({ ...cart, quantity: 2 })
  mocks.remove.mockResolvedValue({ message: 'ok' })
  render(<MemoryRouter><CartView /></MemoryRouter>)
  expect(await screen.findByText('SKU #9')).toBeTruthy()
  fireEvent.click(screen.getByRole('button', { name: '增加' }))
  await waitFor(() => expect(mocks.update).toHaveBeenCalledWith(1, 2))
  fireEvent.click(screen.getByRole('button', { name: '删除' }))
  await waitFor(() => expect(mocks.remove).toHaveBeenCalledWith(1))
})
