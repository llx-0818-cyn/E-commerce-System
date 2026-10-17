import { useEffect, useState } from 'react'
import { ArrowUpRight, Search } from 'lucide-react'
import { Link, useSearchParams } from 'react-router'
import * as searchApi from '../api/search'
import type { SearchPage, SearchScope } from '../api/types'
import AppShell from '../components/AppShell'
import Button from '../components/ui/Button'
import { EmptyState, ErrorState, Skeleton } from '../components/ui/StatePanel'
import styles from './SearchView.module.css'

export default function SearchView() {
  const [params, setParams] = useSearchParams()
  const query = params.get('q') ?? ''
  const [scope, setScope] = useState<SearchScope>('content')
  const [page, setPage] = useState<SearchPage | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    let active = true
    if (!query.trim()) return
    setPage(null); setError(''); setBusy(true)
    void searchApi.search({ q: query, scope, limit: 24 }).then((value) => { if (active) setPage(value) }).catch((reason: unknown) => { if (active) setError(reason instanceof Error ? reason.message : '搜索失败') }).finally(() => { if (active) setBusy(false) })
    return () => { active = false }
  }, [query, scope])
  async function more() {
    if (!page?.has_more || busy) return
    setBusy(true)
    try {
      const next = await searchApi.search({ q: query, scope, limit: 24, offset: page.next_offset })
      setPage({ ...next, items: [...page.items, ...next.items] })
    } catch (reason) { setError(reason instanceof Error ? reason.message : '加载失败') }
    finally { setBusy(false) }
  }
  return <AppShell><div className={styles.heading}><span>FIND YOUR EVERYDAY FAVORITES</span><h1>{query ? `关于「${query}」的灵感` : '你想寻找什么样的生活？'}</h1><p>从一篇笔记，遇见一件喜欢的东西。</p></div>
    <div className={styles.tabs} role="tablist" aria-label="搜索范围">{([['content','灵感笔记'],['product','精选好物'],['shop','生活小店']] as const).map(([value,label]) => <button role="tab" aria-selected={scope === value} className={scope === value ? styles.active : ''} key={value} onClick={() => setScope(value)}>{label}</button>)}</div>
    {!query ? <div className={styles.suggestions}><Search size={25} /><h2>从这些小美好开始</h2><div>{['马克杯','台灯','花瓶','休闲毯','收纳','桌面'].map((word) => <button key={word} onClick={() => setParams({q:word})}>{word} <ArrowUpRight size={12}/></button>)}</div></div> : error ? <ErrorState description={error} /> : !page ? <Skeleton height={280} /> : !page.items.length ? <EmptyState title="还没有找到，换个关键词试试看" /> : <><div className={styles.results}>{page.items.map((item) => <Link className={styles.result} key={item.id} to={`/${scope === 'content' ? 'posts' : scope === 'product' ? 'products' : 'shops'}/${item.id}`}>{item.image_url && <img src={item.image_url} alt={item.title} loading="lazy" />}<div><h2>{item.title}</h2><p>{item.subtitle}</p><span>{item.price_cent ? `¥${(item.price_cent/100).toFixed(2)}` : '看看详情'} <ArrowUpRight size={13}/></span></div></Link>)}</div>{page.has_more && <div className={styles.more}><Button loading={busy} onClick={() => void more()}>加载更多</Button></div>}</>}
  </AppShell>
}
