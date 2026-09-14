import { useState } from 'react'
import { useNavigate } from 'react-router'

import { ApiError } from '../api/client'
import * as accountApi from '../api/account'
import AppShell from '../components/AppShell'
import { useToastStore } from '../stores/toast'

export default function ChangePasswordView() {
  const navigate = useNavigate()
  const toast = useToastStore()
  const [busy, setBusy] = useState(false)
  const [form, setForm] = useState({ username: '', oldPassword: '', newPassword: '' })

  async function submit() {
    if (busy) return
    const username = form.username.trim()
    const oldPassword = form.oldPassword.trim()
    const newPassword = form.newPassword.trim()
    if (!username || !oldPassword || !newPassword) {
      toast.error('请把信息填完整')
      return
    }

    setBusy(true)
    try {
      await accountApi.changePassword(username, oldPassword, newPassword)
      toast.success('密码已修改，请重新登录')
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
          <p className="title">修改密码</p>
          <p className="subtle">不需要登录（对应后端 `/account/changePassword`）。</p>
          <div className="grid" style={{ marginTop: 12 }}>
            <div>
              <label>username</label>
              <input value={form.username} onChange={(event) => setForm((state) => ({ ...state, username: event.currentTarget.value.trim() }))} autoComplete="username" />
            </div>
            <div>
              <label>old_password</label>
              <input value={form.oldPassword} onChange={(event) => setForm((state) => ({ ...state, oldPassword: event.currentTarget.value.trim() }))} type="password" autoComplete="current-password" />
            </div>
            <div>
              <label>new_password</label>
              <input value={form.newPassword} onChange={(event) => setForm((state) => ({ ...state, newPassword: event.currentTarget.value.trim() }))} type="password" autoComplete="new-password" />
            </div>
            <div className="row" style={{ justifyContent: 'flex-end' }}>
              <button className="primary" type="button" disabled={busy} onClick={() => void submit()}>提交</button>
            </div>
          </div>
        </div>

        <div className="card">
          <p className="title">提示</p>
          <p className="muted">改密成功后后端会让旧 token 失效；请在「账号」页重新登录。</p>
        </div>
      </div>
    </AppShell>
  )
}
