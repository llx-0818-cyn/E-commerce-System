import { cleanup, render, screen } from '@testing-library/react'
import { afterEach, expect, it, vi } from 'vitest'
import { MemoryRouter, Route, Routes } from 'react-router'

import { ProtectedRoute } from './index'

vi.mock('../stores/auth', () => ({ useAuthStore: (selector: (state: { isLoggedIn: boolean }) => unknown) => selector({ isLoggedIn: false }) }))
afterEach(cleanup)

it('redirects an unauthenticated protected route to the account page', async () => {
  render(<MemoryRouter initialEntries={['/cart']}><Routes><Route path="/cart" element={<ProtectedRoute><div>购物车私有内容</div></ProtectedRoute>} /><Route path="/account" element={<div>登录页</div>} /></Routes></MemoryRouter>)
  expect(await screen.findByText('登录页')).toBeTruthy()
  expect(screen.queryByText('购物车私有内容')).toBeNull()
})
