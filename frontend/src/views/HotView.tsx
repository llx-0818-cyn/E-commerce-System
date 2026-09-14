import { useEffect, useState } from 'react'
import { Link } from 'react-router'

import * as contentApi from '../api/content'
import type { ContentFeedItem } from '../api/types'
import AppShell from '../components/AppShell'

export default function HotView() {
  const [items, setItems] = useState<ContentFeedItem[]>([])

  useEffect(() => { void contentApi.listPopular({ limit: 20 }).then((page) => setItems(page.items)) }, [])

  return (
    <AppShell>
      <section className="card">
        <h1 className="title">热门内容</h1>
        <div className="grid">
          {items.map((item, index) => (
            <Link className="pill" key={item.id} to={`/posts/${item.id}`}>
              <strong>{index + 1}</strong><span>{item.title}</span><span className="muted">{item.popularity.toFixed(1)}</span>
            </Link>
          ))}
        </div>
      </section>
    </AppShell>
  )
}
