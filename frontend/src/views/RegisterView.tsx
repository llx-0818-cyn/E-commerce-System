import { useState } from 'react'
import { useNavigate } from 'react-router'

import { ApiError } from '../api/client'
import * as accountApi from '../api/account'
import AppShell from '../components/AppShell'
import { useToastStore } from '../stores/toast'

export default function RegisterView() {
  const navigate = useNavigate()
  const toast = useToastStore()
  const [busy, setBusy] = useState(false)
  const [form, setForm] = useState({ username: '', password: '' })

  async function submit() {
    if (busy) return
    const username = form.username.trim()
    const password = form.password.trim()
    if (!username || !password) {
      toast.error('请输入 username 和 password')
      return
    }

    setBusy(true)
    try {
      await accountApi.register(username, password)
      toast.success('注册成功，请登录')
      navigate('/account')
    } catch (error) {
      toast.error(error instanceof ApiError ? error.message : String(error))
    } finally {
      setBusy(false)
    }
  }

  return (
    <AppShell>
      <div className="grid two">
        <div className="card">
          <p className="title">注册</p>
          <p className="subtle">创建新账号（对应后端 `/account/register`）。</p>
          <div className="grid" style={{ marginTop: 12 }}>
            <div>
              <label>username</label>
              <input value={form.username} onChange={(event) => setForm((state) => ({ ...state, username: event.currentTarget.value.trim() }))} autoComplete="username" />
            </div>
            <div>
              <label>password</label>
              <input value={form.password} onChange={(event) => setForm((state) => ({ ...state, password: event.currentTarget.value.trim() }))} type="password" autoComplete="new-password" />
            </div>
            <div className="row" style={{ justifyContent: 'flex-end' }}>
              <button className="primary" type="button" disabled={busy} onClick={() => void submit()}>注册</button>
            </div>
          </div>
        </div>

        <div className="card">
          <p className="title">提示</p>
          <p className="muted">注册成功后会跳回「账号」页进行登录。</p>
        </div>
      </div>
    </AppShell>
  )
}
