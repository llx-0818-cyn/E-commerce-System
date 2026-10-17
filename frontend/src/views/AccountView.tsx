import { useState } from 'react'
import { ArrowRight, Package, Settings, ShoppingBag, Store } from 'lucide-react'
import { Link, useNavigate, useSearchParams } from 'react-router'
import * as accountApi from '../api/account'
import { ApiError } from '../api/client'
import AppShell from '../components/AppShell'
import UserAvatar from '../components/UserAvatar'
import { useAuthStore } from '../stores/auth'
import { useToastStore } from '../stores/toast'
import styles from './AccountView.module.css'

export default function AccountView() {
  const auth = useAuthStore()
  const toast = useToastStore()
  const navigate = useNavigate()
  const [params] = useSearchParams()
  const [form, setForm] = useState({ username: '', password: '' })
  const [busy, setBusy] = useState(false)
  async function login() {
    if (!form.username.trim() || !form.password) { toast.error('请填写用户名和密码'); return }
    setBusy(true)
    try {
      const result = await accountApi.login(form.username.trim(), form.password)
      auth.setTokens(result.token, result.refresh_token ?? '')
      toast.success('欢迎回来，今天也有新的灵感')
      const redirect = params.get('redirect')
      navigate(redirect?.startsWith('/') && !redirect.startsWith('//') ? redirect : '/')
    } catch (error) { toast.error(error instanceof ApiError ? error.message : String(error)) }
    finally { setBusy(false) }
  }
  return <AppShell>{auth.isLoggedIn ? <section className={styles.profile}><span className={styles.kicker}>YOUR EVERYDAY SPACE</span><div className={styles.identity}><UserAvatar id={auth.claims?.account_id} username={auth.claims?.username ?? ''} size={85}/><div><h1>{auth.claims?.username}</h1><p>把喜欢的生活，慢慢收集起来。</p><Link to={`/u/${auth.claims?.account_id}`}>查看我的灵感主页 <ArrowRight size={12}/></Link></div></div><div className={styles.links}><Link to="/cart"><ShoppingBag/><strong>我的购物袋</strong><span>收藏一份心动</span></Link><Link to="/orders"><Package/><strong>我的订单</strong><span>看看好物到哪里了</span></Link><Link to="/seller"><Store/><strong>我的小店</strong><span>分享你的好东西</span></Link><Link to="/settings"><Settings/><strong>个人设置</strong><span>让主页更像你</span></Link></div></section> : <div className={styles.loginLayout}><aside className={styles.story}><img src="/media/catalog/10572206-1.jpg" alt="温暖的居家日常"/><div><span>GOOD THINGS, EVERYDAY.</span><h2>把喜欢的日常，<br/>一点点拾起来。</h2><p>在这里，遇见好物，也遇见认真生活的人。</p></div></aside><section className={styles.login}><span className={styles.kicker}>WELCOME TO SHIWU</span><h1>好久不见，<br/>今天有什么新灵感？</h1><p>登录拾物，继续你的生活收藏。</p><form onSubmit={(event) => { event.preventDefault(); void login() }}><label htmlFor="login-name">用户名</label><input id="login-name" aria-label="用户名" autoComplete="username" placeholder="输入你的用户名" value={form.username} onChange={(event) => setForm({ ...form, username: event.currentTarget.value })}/><label htmlFor="login-password">密码</label><input id="login-password" aria-label="密码" autoComplete="current-password" type="password" placeholder="输入密码" value={form.password} onChange={(event) => setForm({ ...form, password: event.currentTarget.value })}/><button className="primary" disabled={busy} type="submit">{busy ? '正在登录…' : '登录'}<ArrowRight size={16}/></button></form><div className={styles.register}>还没有账号？<Link to="/account/register">现在加入拾物</Link></div><div className={styles.demo}><strong>先体验一下</strong><p>本地演示账号，共用密码 MockPass123!</p><div><button onClick={() => setForm({ username: '小满同学', password: 'MockPass123!' })}>体验买家</button><button onClick={() => setForm({ username: '阿栗的厨房', password: 'MockPass123!' })}>体验店主</button></div></div></section></div>}</AppShell>
}
