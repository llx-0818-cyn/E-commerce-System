import { useMemo, useState } from 'react'
import { useNavigate } from 'react-router'

import { ApiError } from '../api/client'
import * as accountApi from '../api/account'
import AppShell from '../components/AppShell'
import UserAvatar from '../components/UserAvatar'
import { useAuthStore } from '../stores/auth'
import { useToastStore } from '../stores/toast'
import { scopedClasses } from '../utils/scoped-classes'
import styles from './SettingsView.module.css'

export default function SettingsView() {
  const navigate = useNavigate()
  const auth = useAuthStore()
  const toast = useToastStore()
  const [busy, setBusy] = useState(false)
  const [rename, setRename] = useState({ open: false, newUsername: '' })
  const css = (...values: Array<string | false | null | undefined>) => scopedClasses(styles, ...values)
  const me = useMemo(() => ({ id: auth.claims?.account_id ?? 0, username: auth.claims?.username ?? '' }), [auth.claims])

  function openRename() {
    if (!auth.isLoggedIn) return
    setRename({ open: true, newUsername: me.username })
  }

  async function submitRename() {
    if (!auth.isLoggedIn || busy) return
    const newUsername = rename.newUsername.trim()
    if (!newUsername) {
      toast.error('请输入新用户名')
      return
    }

    setBusy(true)
    try {
      const res = await accountApi.rename(newUsername)
      auth.setToken(res.token)
      setRename((state) => ({ ...state, open: false }))
      toast.success('改名成功（已刷新 token）')
    } catch (error) {
      toast.error(error instanceof ApiError ? error.message : String(error))
    } finally {
      setBusy(false)
    }
  }

  async function onLogout() {
    if (!auth.isLoggedIn || busy || !window.confirm('确认退出登录？')) return
    setBusy(true)
    try {
      await accountApi.logout()
    } catch (error) {
      const message = error instanceof ApiError ? error.message : String(error)
      toast.error(`登出失败：${message}`)
    } finally {
      auth.clearTokens()
      setRename((state) => ({ ...state, open: false }))
      toast.info('已退出登录')
      setBusy(false)
      navigate('/')
    }
  }

  if (!auth.isLoggedIn) {
    return (
      <AppShell>
        <div className={css('grid two')}>
          <div className={css('card')}>
            <p className={css('title')}>设置</p>
            <p className={css('subtle')}>需要先登录后才能进行改名/退出等操作。</p>
            <div className={css('row')} style={{ marginTop: 12, justifyContent: 'flex-end' }}>
              <button className={css('primary')} type="button" onClick={() => navigate('/account')}>去登录</button>
            </div>
          </div>
          <div className={css('card')}>
            <p className={css('title')}>提示</p>
            <p className={css('muted')}>登录入口在「账号」页。</p>
          </div>
        </div>
      </AppShell>
    )
  }

  return (
    <AppShell>
      <div className={css('grid two')}>
        <div className={css('card')}>
          <div className={css('row')} style={{ justifyContent: 'space-between', alignItems: 'flex-start' }}>
            <div className={css('row')} style={{ gap: 12, alignItems: 'center' }}>
              <UserAvatar username={me.username} id={me.id} size={56} />
              <div>
                <div className={css('title')} style={{ margin: 0 }}>@{me.username}</div>
                <div className={css('subtle mono')}>#{me.id}</div>
              </div>
            </div>
          </div>

          <div className={css('card')} style={{ marginTop: 14 }}>
            <div className={css('row')} style={{ justifyContent: 'space-between', alignItems: 'center' }}>
              <p className={css('title')} style={{ margin: 0 }}>账号设置</p>
              <button className={css('ghost')} type="button" disabled={busy} onClick={openRename}>改名</button>
            </div>

            {rename.open && (
              <div className={css('grid')} style={{ marginTop: 12 }}>
                <div>
                  <label>new_username</label>
                  <input
                    value={rename.newUsername}
                    onChange={(event) => setRename((state) => ({ ...state, newUsername: event.currentTarget.value.trim() }))}
                    onKeyDown={(event) => { if (event.key === 'Enter') void submitRename() }}
                  />
                </div>
                <div className={css('row')} style={{ justifyContent: 'flex-end' }}>
                  <button type="button" disabled={busy} onClick={() => setRename((state) => ({ ...state, open: false }))}>取消</button>
                  <button className={css('primary')} type="button" disabled={busy} onClick={() => void submitRename()}>提交</button>
                </div>
              </div>
            )}
          </div>

          <div className={css('card')} style={{ marginTop: 14 }}>
            <p className={css('title')}>账号安全</p>
            <div className={css('row')}>
              <button className={css('ghost')} type="button" disabled={busy} onClick={() => navigate('/account/change-password')}>修改密码</button>
              <button className={css('danger')} type="button" disabled={busy} onClick={() => void onLogout()}>退出登录</button>
            </div>
          </div>
        </div>

        <div className={css('card')}>
          <p className={css('title')}>说明</p>
          <div className={css('grid')} style={{ marginTop: 10 }}>
            <div className={css('pill ok')}>改名后会返回新 token，旧 token 立即失效</div>
            <div className={css('pill ok')}>退出登录会清空本地 token</div>
            <div className={css('pill')}>修改密码无需登录，但成功后会让旧 token 失效</div>
          </div>
        </div>
      </div>
    </AppShell>
  )
}
