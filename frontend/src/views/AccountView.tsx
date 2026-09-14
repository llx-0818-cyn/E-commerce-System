import { useState } from 'react'
import { Link } from 'react-router'

import * as accountApi from '../api/account'
import { ApiError } from '../api/client'
import AppShell from '../components/AppShell'
import { useAuthStore } from '../stores/auth'
import { useToastStore } from '../stores/toast'

export default function AccountView() {
  const auth = useAuthStore()
  const toast = useToastStore()
  const [form, setForm] = useState({ username: '', password: '' })
  const [busy, setBusy] = useState(false)

  async function login() {
    setBusy(true)
    try {
      const result = await accountApi.login(form.username.trim(), form.password)
      auth.setTokens(result.token, result.refresh_token ?? '')
      toast.success('登录成功')
    } catch (error) {
      toast.error(error instanceof ApiError ? error.message : String(error))
    } finally {
      setBusy(false)
    }
  }

  return (
    <AppShell>
      <section className="card">
        {auth.isLoggedIn ? (
          <>
            <h1 className="title">@{auth.claims?.username}</h1>
            <p className="subtle">账号 #{auth.claims?.account_id}</p>
            <div className="row"><Link className="pill" to="/orders">我的订单</Link><Link className="pill" to="/seller">卖家中心</Link></div>
          </>
        ) : (
          <div className="grid" style={{ maxWidth: 420 }}>
            <h1 className="title">登录</h1>
            <input aria-label="用户名" autoComplete="username" value={form.username} onChange={(event) => setForm({ ...form, username: event.currentTarget.value })} />
            <input aria-label="密码" autoComplete="current-password" type="password" value={form.password} onChange={(event) => setForm({ ...form, password: event.currentTarget.value })} />
            <button className="primary" disabled={busy} type="button" onClick={() => void login()}>登录</button>
            <Link className="subtle" to="/account/register">注册账号</Link>
          </div>
        )}
      </section>
    </AppShell>
  )
}
