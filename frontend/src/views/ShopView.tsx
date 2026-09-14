import { useEffect, useState } from 'react'
import { CirclePause, Store as StoreIcon } from 'lucide-react'
import { Link, useParams } from 'react-router'

import * as storefrontApi from '../api/storefront'
import type { PublicProductItem, PublicShop } from '../api/types'
import AppShell from '../components/AppShell'
import Card from '../components/ui/Card'
import { EmptyState, ErrorState, Skeleton } from '../components/ui/StatePanel'
import styles from './ShopView.module.css'

const fallback = (label: string) => label.includes('包') ? '/assets/feed-bag.jpg' : label.includes('杯') ? '/assets/feed-cup.jpg' : '/assets/feed-coffee.jpg'

export default function ShopView() {
  const { id = '0' } = useParams()
  const shopId = Number(id)
  const [shop, setShop] = useState<PublicShop | null>(null)
  const [products, setProducts] = useState<PublicProductItem[]>([])
  const [error, setError] = useState('')
  useEffect(() => { void Promise.all([storefrontApi.getShop(shopId), storefrontApi.listShopProducts(shopId, 50)]).then(([value, page]) => { setShop(value); setProducts(page.products) }).catch((reason: unknown) => setError(reason instanceof Error ? reason.message : String(reason))) }, [shopId])
  if (error) return <AppShell><ErrorState description={error} /></AppShell>
  if (!shop) return <AppShell><Skeleton height={360} /></AppShell>
  return <AppShell>
    <div className={styles.banner}><img alt="" onError={(event) => { event.currentTarget.onerror = null; event.currentTarget.src = '/assets/feed-coffee.jpg' }} src={shop.shop.banner_url} /></div>
    <div className={styles.profile}><div className={styles.identity}><img alt="" className={styles.avatar} onError={(event) => { event.currentTarget.onerror = null; event.currentTarget.src = '/assets/feed-cup.jpg' }} src={shop.shop.avatar_url} /><div><h1 className="title">{shop.shop.name}</h1><p className="subtle">{shop.shop.description}</p></div></div><span className={[styles.status, shop.accepting_orders ? styles.open : styles.closed].join(' ')}>{shop.accepting_orders ? <><StoreIcon size={14} />营业中</> : <><CirclePause size={14} />暂停营业</>}</span></div>
    {products.length === 0 ? <EmptyState title="暂无商品" /> : <div className={styles.products}>{products.map((item) => <Card className={styles.product} key={item.product.id} padded={false}><Link to={`/products/${item.product.id}`}><img alt="" loading="lazy" onError={(event) => { event.currentTarget.onerror = null; event.currentTarget.src = fallback(item.product.name) }} src={item.product.cover_url} /><div className={styles.productBody}><h2 className={styles.productName}>{item.product.name}</h2><p className="subtle">{item.product.subtitle}</p><div className={styles.price}>{item.min_price_cent ? `¥${(item.min_price_cent / 100).toFixed(2)}` : '暂不可售'}</div></div></Link></Card>)}</div>}
  </AppShell>
}
