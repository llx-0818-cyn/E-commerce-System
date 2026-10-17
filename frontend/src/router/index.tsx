import type { ReactNode } from 'react'
import { Navigate, Route, Routes, useLocation } from 'react-router'

import { useAuthStore } from '../stores/auth'
import AccountView from '../views/AccountView'
import CartView from '../views/CartView'
import ChangePasswordView from '../views/ChangePasswordView'
import CheckoutView from '../views/CheckoutView'
import ContentDetailView from '../views/ContentDetailView'
import DesignSystemView from '../views/DesignSystemView'
import HomeView from '../views/HomeView'
import HotView from '../views/HotView'
import MessageView from '../views/MessageView'
import OrderDetailView from '../views/OrderDetailView'
import OrdersView from '../views/OrdersView'
import PublishPostView from '../views/PublishPostView'
import ProductView from '../views/ProductView'
import RegisterView from '../views/RegisterView'
import SettingsView from '../views/SettingsView'
import ShopView from '../views/ShopView'
import SellerView from '../views/SellerView'
import SearchView from '../views/SearchView'
import UserProfileView from '../views/UserProfileView'
import WorkspaceView from '../views/WorkspaceView'

export function ProtectedRoute({ children }: { children: ReactNode }) {
  const isLoggedIn = useAuthStore((state) => state.isLoggedIn)
  const location = useLocation()
  if (isLoggedIn) return children
  return <Navigate to={`/account?redirect=${encodeURIComponent(`${location.pathname}${location.search}`)}`} replace />
}

export default function AppRouter() {
  return (
    <Routes>
      <Route path="/" element={<HomeView />} />
      <Route path="/hot" element={<HotView />} />
      <Route path="/search" element={<SearchView />} />
      <Route path="/posts/new" element={<ProtectedRoute><PublishPostView /></ProtectedRoute>} />
      <Route path="/posts/:id" element={<ContentDetailView />} />
      <Route path="/shops/:id" element={<ShopView />} />
      <Route path="/products/:id" element={<ProductView />} />
      <Route path="/cart" element={<ProtectedRoute><CartView /></ProtectedRoute>} />
      <Route path="/checkout" element={<ProtectedRoute><CheckoutView /></ProtectedRoute>} />
      <Route path="/orders" element={<ProtectedRoute><OrdersView /></ProtectedRoute>} />
      <Route path="/orders/:id" element={<ProtectedRoute><OrderDetailView /></ProtectedRoute>} />
      <Route path="/seller" element={<ProtectedRoute><SellerView /></ProtectedRoute>} />
      <Route path="/flash-sales/:id" element={<ProtectedRoute><WorkspaceView title="限时抢购" /></ProtectedRoute>} />
      <Route path="/ui-kit" element={<DesignSystemView />} />
      <Route path="/account" element={<AccountView />} />
      <Route path="/account/register" element={<RegisterView />} />
      <Route path="/account/change-password" element={<ChangePasswordView />} />
      <Route path="/settings" element={<ProtectedRoute><SettingsView /></ProtectedRoute>} />
      <Route path="/u/:id" element={<UserProfileView />} />
      <Route path="/messages" element={<ProtectedRoute><MessageView /></ProtectedRoute>} />
      <Route path="/messages/:peerId" element={<ProtectedRoute><MessageView /></ProtectedRoute>} />
      <Route path="*" element={<Navigate to="/" replace />} />
    </Routes>
  )
}
