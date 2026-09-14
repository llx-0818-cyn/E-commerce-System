import { useCallback, useEffect, useRef, useState } from 'react'
import { LogIn, RefreshCw } from 'lucide-react'
import { useNavigate } from 'react-router'

import * as contentApi from '../api/content'
import type { ContentFeedItem, PostCursor } from '../api/types'
import AppShell from '../components/AppShell'
import ContentFeedCard from '../components/ContentFeedCard'
import Button from '../components/ui/Button'
import PageHeader from '../components/ui/PageHeader'
import { EmptyState, ErrorState, Skeleton } from '../components/ui/StatePanel'
import { useAuthStore } from '../stores/auth'
import styles from './HomeView.module.css'

export type FeedMode = 'latest' | 'following' | 'popular'
type FeedState = { items: ContentFeedItem[]; loading: boolean; error: string; initialized: boolean; hasMore: boolean; cursor?: PostCursor; snapshotAt?: string; nextOffset: number }
const emptyState = (): FeedState => ({ items: [], loading: false, error: '', initialized: false, hasMore: false, nextOffset: 0 })

export function mergeFeedItems(current: ContentFeedItem[], incoming: ContentFeedItem[]) {
  const seen = new Set(current.map((item) => item.id))
  return current.concat(incoming.filter((item) => !seen.has(item.id)))
}

export default function HomeView() {
  const navigate = useNavigate()
  const isLoggedIn = useAuthStore((state) => state.isLoggedIn)
  const [mode, setMode] = useState<FeedMode>('latest')
  const [states, setStates] = useState<Record<FeedMode, FeedState>>({ latest: emptyState(), following: emptyState(), popular: emptyState() })
  const stateRef = useRef(states)
  const updateStates = useCallback((updater: (value: Record<FeedMode, FeedState>) => Record<FeedMode, FeedState>) => {
    const next = updater(stateRef.current)
    stateRef.current = next
    setStates(next)
  }, [])

  const load = useCallback(async (target: FeedMode, reset: boolean) => {
    const current = stateRef.current[target]
    if (current.loading || (target === 'following' && !useAuthStore.getState().isLoggedIn)) return
    updateStates((all) => ({ ...all, [target]: { ...all[target], loading: true, error: '' } }))
    try {
      if (target === 'popular') {
        const page = await contentApi.listPopular({ limit: 12, snapshotAt: reset ? undefined : current.snapshotAt, offset: reset ? 0 : current.nextOffset })
        updateStates((all) => ({ ...all, popular: { ...all.popular, items: reset ? page.items : mergeFeedItems(all.popular.items, page.items), loading: false, initialized: true, hasMore: page.has_more, snapshotAt: page.snapshot_at, nextOffset: page.next_offset } }))
      } else {
        const page = target === 'following' ? await contentApi.listFollowing(12, reset ? undefined : current.cursor) : await contentApi.listLatest(12, reset ? undefined : current.cursor)
        updateStates((all) => ({ ...all, [target]: { ...all[target], items: reset ? page.items : mergeFeedItems(all[target].items, page.items), loading: false, initialized: true, hasMore: page.has_more, cursor: page.next_cursor } }))
      }
    } catch (error) {
      updateStates((all) => ({ ...all, [target]: { ...all[target], loading: false, initialized: true, error: error instanceof Error ? error.message : String(error) } }))
    }
  }, [updateStates])

  useEffect(() => {
    if (!states[mode].initialized && !(mode === 'following' && !isLoggedIn)) void load(mode, true)
  }, [isLoggedIn, load, mode, states])

  const state = states[mode]
  return <AppShell>
    <PageHeader title="内容发现" actions={<Button icon={<RefreshCw size={16} />} onClick={() => void load(mode, true)} size="small">刷新</Button>} />
    <div className={styles.toolbar}><div aria-label="内容流" className={styles.tabs} role="tablist">{([['latest', '推荐'], ['following', '关注'], ['popular', '热门']] as const).map(([value, label]) => <button aria-selected={mode === value} className={[styles.tab, mode === value ? styles.active : ''].filter(Boolean).join(' ')} key={value} onClick={() => setMode(value)} role="tab" type="button">{label}</button>)}</div></div>
    {mode === 'following' && !isLoggedIn ? <EmptyState action={<Button icon={<LogIn size={16} />} onClick={() => navigate('/account')} variant="primary">登录</Button>} title="登录后查看关注内容" />
      : state.error ? <ErrorState action={<Button icon={<RefreshCw size={16} />} onClick={() => void load(mode, true)}>重试</Button>} description={state.error} />
        : state.loading && state.items.length === 0 ? <div className={styles.feed}>{[1, 2, 3, 4].map((value) => <div className={styles.skeletonCard} key={value}><Skeleton height={210} /><Skeleton width="72%" height={22} /><Skeleton width="90%" /></div>)}</div>
          : state.items.length === 0 ? <EmptyState title="暂无内容" />
            : <><div className={styles.feed}>{state.items.map((item) => <ContentFeedCard item={item} key={item.id} />)}</div>{state.hasMore && <div className={styles.more}><Button loading={state.loading} onClick={() => void load(mode, false)}>加载更多</Button></div>}</>}
  </AppShell>
}
