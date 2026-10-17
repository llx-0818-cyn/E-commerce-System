import { useCallback, useEffect, useRef, useState } from 'react'
import { ArrowUpRight, Coffee, Lightbulb, LogIn, RefreshCw, Sparkles } from 'lucide-react'
import { Link, useNavigate } from 'react-router'

import * as contentApi from '../api/content'
import type { ContentFeedItem, PostCursor } from '../api/types'
import AppShell from '../components/AppShell'
import ContentFeedCard from '../components/ContentFeedCard'
import Button from '../components/ui/Button'
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
  const [topic, setTopic] = useState('全部')
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
        const page = await contentApi.listPopular({ limit: 24, snapshotAt: reset ? undefined : current.snapshotAt, offset: reset ? 0 : current.nextOffset })
        updateStates((all) => ({ ...all, popular: { ...all.popular, items: reset ? page.items : mergeFeedItems(all.popular.items, page.items), loading: false, initialized: true, hasMore: page.has_more, snapshotAt: page.snapshot_at, nextOffset: page.next_offset } }))
      } else {
        const page = target === 'following' ? await contentApi.listFollowing(24, reset ? undefined : current.cursor) : await contentApi.listLatest(24, reset ? undefined : current.cursor)
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
  const visibleItems = topic === '全部' ? state.items : state.items.filter((item) => item.tags.some((tag) => tag.name === topic))
  const heroItems = [state.items.find((item) => item.tags.some((tag) => tag.name === '温暖织物')), state.items.find((item) => item.tags.some((tag) => tag.name === '杯壶日常')), state.items.find((item) => item.tags.some((tag) => tag.name === '氛围灯光'))]
  return <AppShell>
    <div className={styles.heading}><div><span className={styles.eyebrow}>A LITTLE INSPIRATION, EVERY DAY</span><h1>发现生活的另一面<span>。</span></h1><p>一些值得分享的小事，和让日常更美好的东西。</p></div><span className={styles.editorNote}><Sparkles size={14} /> 拾物 · 生活灵感集</span></div>
    <div className={styles.features}>
      <Link className={[styles.feature, styles.featureMain].join(' ')} to={heroItems[0] ? `/posts/${heroItems[0].id}` : '/search?q=温暖织物'}><div><span className={styles.featureIndex}>01 / THE SLOW LIFE</span><h2>把日子，<br />过成喜欢的样子。</h2><span className={styles.featureCta}>寻找家的松弛感 <ArrowUpRight size={14} /></span></div>{heroItems[0]?.images[0] && <img src={heroItems[0].images[0].image_url} alt="温暖的居家灵感" />}</Link>
      <Link className={[styles.feature, styles.featureCoffee].join(' ')} to={heroItems[1] ? `/posts/${heroItems[1].id}` : '/search?q=杯壶日常'}><span className={styles.featureIndex}><Coffee size={15} /> MORNING RITUAL</span><h2>一杯的时间</h2><p>从好好喝水开始</p>{heroItems[1]?.images[0] && <img src={heroItems[1].images[0].image_url} alt="杯壶日常" />}<ArrowUpRight className={styles.featureArrow} size={20} /></Link>
      <Link className={[styles.feature, styles.featureLight].join(' ')} to={heroItems[2] ? `/posts/${heroItems[2].id}` : '/search?q=氛围灯光'}><span className={styles.featureIndex}><Lightbulb size={15} /> LITTLE WARMTH</span><h2>留一盏温柔</h2><p>让夜晚也有好心情</p>{heroItems[2]?.images[0] && <img src={heroItems[2].images[0].image_url} alt="居家灯光" />}<ArrowUpRight className={styles.featureArrow} size={20} /></Link>
    </div>
    <div className={styles.toolbar}><div aria-label="内容流" className={styles.tabs} role="tablist">{([['latest', '推荐'], ['following', '关注'], ['popular', '热门']] as const).map(([value, label]) => <button aria-selected={mode === value} className={[styles.tab, mode === value ? styles.active : ''].filter(Boolean).join(' ')} key={value} onClick={() => { setMode(value); setTopic('全部') }} role="tab" type="button">{label}</button>)}</div><Button icon={<RefreshCw size={13} />} onClick={() => void load(mode, true)} size="small" variant="ghost">换一换</Button></div>
    <div aria-label="生活分类" className={styles.topics}>{['全部', '杯壶日常', '氛围灯光', '温暖织物', '花器摆件', '厨房收纳', '桌面好物'].map((value) => <button aria-pressed={topic === value} className={topic === value ? styles.topicActive : ''} key={value} onClick={() => setTopic(value)}>{value === '全部' ? '为你精选' : value}</button>)}</div>
    {mode === 'following' && !isLoggedIn ? <EmptyState action={<Button icon={<LogIn size={16} />} onClick={() => navigate('/account')} variant="primary">登录</Button>} title="登录后查看关注内容" />
      : state.error ? <ErrorState action={<Button icon={<RefreshCw size={16} />} onClick={() => void load(mode, true)}>重试</Button>} description={state.error} />
        : state.loading && state.items.length === 0 ? <div className={styles.feed}>{[1, 2, 3, 4].map((value) => <div className={styles.skeletonCard} key={value}><Skeleton height={210} /><Skeleton width="72%" height={22} /><Skeleton width="90%" /></div>)}</div>
          : visibleItems.length === 0 ? <EmptyState title="这个分类暂时没有内容" />
            : <><div className={styles.feed}>{visibleItems.map((item) => <ContentFeedCard item={item} key={item.id} />)}</div>{state.hasMore && <div className={styles.more}><Button loading={state.loading} onClick={() => void load(mode, false)}>加载更多</Button></div>}</>}
  </AppShell>
}
