import { useEffect, useState } from 'react'
import { Flame } from 'lucide-react'
import * as contentApi from '../api/content'
import type { ContentFeedItem } from '../api/types'
import AppShell from '../components/AppShell'
import ContentFeedCard from '../components/ContentFeedCard'
import { EmptyState, ErrorState, Skeleton } from '../components/ui/StatePanel'
import styles from './HomeView.module.css'

export default function HotView() {
  const [items, setItems] = useState<ContentFeedItem[] | null>(null)
  const [error, setError] = useState('')
  useEffect(() => { void contentApi.listPopular({ limit: 24 }).then((page) => setItems(page.items)).catch((reason: unknown) => setError(reason instanceof Error ? reason.message : '暂时无法加载')) }, [])
  return <AppShell><div className={styles.heading}><div><span className={styles.eyebrow}>THE INSPIRATION LIST</span><h1>被喜欢的日常<span>。</span></h1><p>从热度里，找到下一份生活灵感。</p></div><span className={styles.editorNote}><Flame size={15}/> 灵感热榜</span></div>{error ? <ErrorState description={error}/> : items === null ? <Skeleton height={300}/> : !items.length ? <EmptyState title="灵感正在路上"/> : <div className={styles.feed}>{items.map((item) => <ContentFeedCard key={item.id} item={item}/>)}</div>}</AppShell>
}
