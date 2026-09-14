import { useEffect, useMemo, useState, type PropsWithChildren } from 'react'
import { Flame, Home, PackageSearch, ReceiptText, Search, ShoppingCart, SquarePen, Store, UserRound } from 'lucide-react'
import { createSearchParams, Link, NavLink, useLocation, useNavigate } from 'react-router'

import { getRouteName } from '../router/route-names'
import { useAuthStore } from '../stores/auth'
import { useSocialStore } from '../stores/social'
import { scopedClasses } from '../utils/scoped-classes'
import styles from './AppShell.module.css'
import Toaster from './Toaster'

type AppShellProps = PropsWithChildren<{ full?: boolean }>

export default function AppShell({ full = false, children }: AppShellProps) {
  const auth = useAuthStore()
  const social = useSocialStore()
  const { clear: clearSocial, refreshMine } = social
  const navigate = useNavigate()
  const location = useLocation()
  const [search, setSearch] = useState(() => new URLSearchParams(location.search).get('q') ?? '')
  const css = (...values: Array<string | false | null | undefined>) => scopedClasses(styles, ...values)

  useEffect(() => {
    setSearch(new URLSearchParams(location.search).get('q') ?? '')
  }, [location.search])

  useEffect(() => {
    if (auth.isLoggedIn) void refreshMine()
    else clearSocial()
  }, [auth.isLoggedIn, clearSocial, refreshMine])

  const userLabel = useMemo(() => {
    if (!auth.isLoggedIn) return '未登录'
    const username = auth.claims?.username ?? '(unknown)'
    const accountId = auth.claims?.account_id
    return accountId ? `${username} #${accountId}` : username
  }, [auth.claims, auth.isLoggedIn])

  function onSearch() {
    const query = search.trim()
    navigate({ pathname: '/search', search: query ? `?${createSearchParams({ q: query })}` : '' })
  }

  const navigationClass = (base: string) => ({ isActive }: { isActive: boolean }) =>
    css(base, isActive && 'router-link-active')

  return (
    <div className={css('dy-shell')}>
      <aside className={css('dy-aside')}>
        <Link className={css('dy-logo')} to="/">内容集市</Link>

        <nav className={css('dy-nav')}>
          <NavLink end className={navigationClass('dy-nav-link')} to="/"><Home className={css('dy-nav-icon')} />推荐</NavLink>
          <NavLink end className={navigationClass('dy-nav-link')} to="/hot"><Flame className={css('dy-nav-icon')} />热榜</NavLink>
          <NavLink end className={navigationClass('dy-nav-link')} to="/search"><PackageSearch className={css('dy-nav-icon')} />搜索</NavLink>
          <NavLink end className={navigationClass('dy-nav-link')} to="/posts/new"><SquarePen className={css('dy-nav-icon')} />发布</NavLink>
          {auth.isLoggedIn && <NavLink end className={navigationClass('dy-nav-link')} to="/cart"><ShoppingCart className={css('dy-nav-icon')} />购物车</NavLink>}
          {auth.isLoggedIn && <NavLink end className={navigationClass('dy-nav-link')} to="/orders"><ReceiptText className={css('dy-nav-icon')} />订单</NavLink>}
          <NavLink end className={navigationClass('dy-nav-link')} to="/account"><UserRound className={css('dy-nav-icon')} />账号</NavLink>
          {auth.isLoggedIn && <NavLink end className={navigationClass('dy-nav-link')} to="/seller"><Store className={css('dy-nav-icon')} />卖家</NavLink>}
        </nav>

        <div className={css('dy-aside-foot')}>
          <div className={css('dy-user')}>
            <span className={css('dy-user-dot', auth.isLoggedIn ? 'ok' : 'bad')} />
            <span className={css('dy-user-name')}>{userLabel}</span>
          </div>
          <div className={css('dy-user-actions')}>
            {!auth.isLoggedIn ? (
              <button className={css('dy-btn dy-btn-primary')} type="button" onClick={() => navigate('/account')}>登录</button>
            ) : (
              <button className={css('dy-btn dy-btn-primary')} type="button" onClick={() => navigate('/settings')}>设置</button>
            )}
          </div>
        </div>
      </aside>

      <div className={css('dy-main')}>
        <header className={css('dy-topbar')}>
          <div className={css('dy-top-left')}>
            <div className={css('dy-tabs-hint')}>{getRouteName(location.pathname)}</div>
          </div>

          <div className={css('dy-search')}>
            <input
              value={search}
              onChange={(event) => setSearch(event.currentTarget.value)}
              className={css('dy-search-input')}
              placeholder="搜索内容、商品或店铺"
              onKeyDown={(event) => { if (event.key === 'Enter') onSearch() }}
            />
            <button aria-label="搜索" className={css('dy-btn dy-btn-primary')} title="搜索" type="button" onClick={onSearch}><Search size={17} /></button>
          </div>

          <div className={css('dy-top-right')}>
            <Link className={css('dy-btn dy-btn-ghost')} to="/posts/new">发布内容</Link>
          </div>
        </header>

        <nav className={css('dy-mobile-nav')}>
          <NavLink end className={navigationClass('dy-mobile-link')} to="/">推荐</NavLink>
          <NavLink end className={navigationClass('dy-mobile-link')} to="/hot">热榜</NavLink>
          <NavLink end className={navigationClass('dy-mobile-link')} to="/posts/new">发布</NavLink>
          {auth.isLoggedIn && <NavLink end className={navigationClass('dy-mobile-link')} to="/cart">购物车</NavLink>}
          <NavLink end className={navigationClass('dy-mobile-link')} to="/account">账号</NavLink>
        </nav>

        <div className={css('dy-content', full ? 'full' : 'padded')}>
          {full ? children : <div className="container">{children}</div>}
        </div>
      </div>

      <Toaster />
    </div>
  )
}
