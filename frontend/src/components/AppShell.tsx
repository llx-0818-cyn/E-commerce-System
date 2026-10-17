import { useEffect, useMemo, useState, type PropsWithChildren } from 'react'
import { Flame, Home, PackageSearch, ReceiptText, Search, ShoppingBag, ShoppingCart, SquarePen, Store, UserRound, Plus } from 'lucide-react'
import { createSearchParams, Link, NavLink, useLocation, useNavigate } from 'react-router'

import { getRouteName } from '../router/route-names'
import { useAuthStore } from '../stores/auth'
import { useSocialStore } from '../stores/social'
import { scopedClasses } from '../utils/scoped-classes'
import styles from './AppShell.module.css'
import Toaster from './Toaster'
import UserAvatar from './UserAvatar'

type AppShellProps = PropsWithChildren<{ full?: boolean }>

export default function AppShell({ full = false, children }: AppShellProps) {
  const auth = useAuthStore()
  const social = useSocialStore()
  const { clear: clearSocial, refreshMine } = social
  const navigate = useNavigate()
  const location = useLocation()
  const [search, setSearch] = useState(() => new URLSearchParams(location.search).get('q') ?? '')
  const css = (...values: Array<string | false | null | undefined>) => scopedClasses(styles, ...values)

  useEffect(() => { setSearch(new URLSearchParams(location.search).get('q') ?? '') }, [location.search])
  useEffect(() => {
    if (auth.isLoggedIn) void refreshMine()
    else clearSocial()
  }, [auth.isLoggedIn, clearSocial, refreshMine])

  const userLabel = useMemo(() => auth.isLoggedIn ? auth.claims?.username ?? '拾物用户' : '遇见你的生活灵感', [auth.claims, auth.isLoggedIn])
  function onSearch() {
    const query = search.trim()
    navigate({ pathname: '/search', search: query ? `?${createSearchParams({ q: query })}` : '' })
  }
  const navigationClass = (base: string) => ({ isActive }: { isActive: boolean }) => css(base, isActive && 'router-link-active')

  return <div className={css('dy-shell')}>
    <aside className={css('dy-aside')}>
      <div><Link className={css('dy-logo')} to="/"><span className={css('logo-mark')}><ShoppingBag size={23} strokeWidth={2.3} /></span>拾物</Link><div className={css('logo-caption')}>GOOD THINGS, EVERYDAY.</div></div>
      <nav className={css('dy-nav')} aria-label="主导航">
        <NavLink end className={navigationClass('dy-nav-link')} to="/"><Home className={css('dy-nav-icon')} />发现</NavLink>
        <NavLink end className={navigationClass('dy-nav-link')} to="/hot"><Flame className={css('dy-nav-icon')} />灵感热榜</NavLink>
        <NavLink end className={navigationClass('dy-nav-link')} to="/search"><PackageSearch className={css('dy-nav-icon')} />好物搜索</NavLink>
        <NavLink end className={navigationClass('dy-nav-link')} to="/posts/new"><SquarePen className={css('dy-nav-icon')} />发布笔记</NavLink>
        <div className={css('nav-heading')}>我的生活</div>
        <NavLink end className={navigationClass('dy-nav-link')} to="/cart"><ShoppingCart className={css('dy-nav-icon')} />购物袋</NavLink>
        <NavLink end className={navigationClass('dy-nav-link')} to="/orders"><ReceiptText className={css('dy-nav-icon')} />我的订单</NavLink>
        <NavLink end className={navigationClass('dy-nav-link')} to="/account"><UserRound className={css('dy-nav-icon')} />个人主页</NavLink>
        {auth.isLoggedIn && <NavLink end className={navigationClass('dy-nav-link')} to="/seller"><Store className={css('dy-nav-icon')} />我的小店</NavLink>}
      </nav>
      <div className={css('dy-aside-foot')}>
        <div className={css('dy-user')}><UserAvatar username={auth.claims?.username ?? '拾物'} id={auth.claims?.account_id} size={32} /><span className={css('dy-user-name')}>{userLabel}</span></div>
        <div className={css('dy-user-actions')}><button className={css('dy-btn dy-btn-primary')} type="button" onClick={() => navigate(auth.isLoggedIn ? '/settings' : '/account')}>{auth.isLoggedIn ? '管理我的主页' : '登录 / 注册'}</button></div>
        <a className={css('source-note')} href="/catalog/sources.html" target="_blank" rel="noreferrer">本地体验站 · 商品与评价来源 ↗<br />让好物回到日常，让日常有光。</a>
      </div>
    </aside>
    <div className={css('dy-main')}>
      <header className={css('dy-topbar')}>
        <div className={css('dy-top-left')}><div className={css('dy-tabs-hint')}><span className={css('top-kicker')}>THE EVERYDAY EDIT</span>{getRouteName(location.pathname)}</div></div>
        <form className={css('dy-search')} onSubmit={(event) => { event.preventDefault(); onSearch() }}>
          <input value={search} onChange={(event) => setSearch(event.currentTarget.value)} className={css('dy-search-input')} placeholder="搜一搜，你想过的生活" aria-label="搜索内容、商品或店铺" />
          <button aria-label="搜索" title="搜索" type="submit"><Search size={19} /></button>
        </form>
        <div className={css('dy-top-right')}><Link aria-label="购物袋" className={css('cart-link')} to="/cart"><ShoppingCart size={21} /></Link><Link className={css('dy-btn dy-btn-primary')} to="/posts/new"><Plus size={16} />发布笔记</Link></div>
      </header>
      <nav className={css('dy-mobile-nav')} aria-label="移动导航">
        <NavLink end className={navigationClass('dy-mobile-link')} to="/"><Home />发现</NavLink>
        <NavLink end className={navigationClass('dy-mobile-link')} to="/hot"><Flame />热榜</NavLink>
        <NavLink end className={navigationClass('dy-mobile-link')} to="/posts/new"><SquarePen />发布</NavLink>
        <NavLink end className={navigationClass('dy-mobile-link')} to="/cart"><ShoppingCart />购物袋</NavLink>
        <NavLink end className={navigationClass('dy-mobile-link')} to="/account"><UserRound />我的</NavLink>
      </nav>
      <div className={css('dy-content', full ? 'full' : 'padded')}>{full ? children : <div className="container">{children}</div>}</div>
    </div>
    <Toaster />
  </div>
}
