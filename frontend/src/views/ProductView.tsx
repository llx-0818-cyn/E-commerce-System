import { useEffect, useMemo, useState } from 'react'
import { Minus, Plus, ShoppingCart, Store } from 'lucide-react'
import { Link, useNavigate, useParams } from 'react-router'

import * as checkoutApi from '../api/checkout'
import * as storefrontApi from '../api/storefront'
import type { ContentPost, PublicProductDetail, PublicSKU } from '../api/types'
import AppShell from '../components/AppShell'
import Button from '../components/ui/Button'
import Card from '../components/ui/Card'
import { ErrorState, Skeleton } from '../components/ui/StatePanel'
import { useAuthStore } from '../stores/auth'
import { useToastStore } from '../stores/toast'
import styles from './ProductView.module.css'

const fallback = (label: string) => label.includes('包') ? '/assets/feed-bag.jpg' : label.includes('杯') ? '/assets/feed-cup.jpg' : '/assets/feed-coffee.jpg'

export default function ProductView() {
  const { id = '0' } = useParams()
  const productId = Number(id)
  const navigate = useNavigate()
  const isLoggedIn = useAuthStore((state) => state.isLoggedIn)
  const toast = useToastStore()
  const [detail, setDetail] = useState<PublicProductDetail | null>(null)
  const [related, setRelated] = useState<ContentPost[]>([])
  const [selectedId, setSelectedId] = useState(0)
  const [quantity, setQuantity] = useState(1)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => { void Promise.all([storefrontApi.getProduct(productId), storefrontApi.listRelatedPosts(productId)]).then(([value, posts]) => { setDetail(value); setRelated(posts.posts); setSelectedId(value.skus.find((sku) => sku.can_purchase)?.id ?? value.skus[0]?.id ?? 0) }).catch((reason: unknown) => setError(reason instanceof Error ? reason.message : String(reason))) }, [productId])
  const selected = useMemo<PublicSKU | undefined>(() => detail?.skus.find((sku) => sku.id === selectedId), [detail, selectedId])
  async function addCart() {
    if (!isLoggedIn) { navigate('/account'); return }
    if (!selected?.can_purchase) return
    setBusy(true)
    try { await checkoutApi.addCartItem({ sku_id: selected.id, quantity, mode: 'merge' }); toast.success('已加入购物车') } finally { setBusy(false) }
  }
  if (error) return <AppShell><ErrorState description={error} /></AppShell>
  if (!detail) return <AppShell><Skeleton height={460} /></AppShell>
  const images = detail.images.length ? detail.images : [{ id: 0, image_url: detail.product.cover_url }]
  return <AppShell>
    <div className={styles.layout}><div className={styles.gallery}>{images.map((image) => <img alt="" key={image.id} onError={(event) => { event.currentTarget.onerror = null; event.currentTarget.src = fallback(detail.product.name) }} src={image.image_url} />)}</div><section className={styles.panel}><Link className="subtle" to={`/shops/${detail.shop.shop.id}`}><Store size={14} /> {detail.shop.shop.name}</Link><h1>{detail.product.name}</h1><p className={styles.subtitle}>{detail.product.subtitle}</p><div className={styles.price}>{detail.min_price_cent ? `¥${(detail.min_price_cent / 100).toFixed(2)}` : '暂不可售'}</div>{!detail.shop.accepting_orders && <div className={styles.notice}>店铺暂停营业</div>}<div className={styles.skus}>{detail.skus.map((sku) => <button className={[styles.sku, selectedId === sku.id ? styles.selected : ''].filter(Boolean).join(' ')} disabled={!sku.can_purchase} key={sku.id} onClick={() => setSelectedId(sku.id)} type="button"><span>{Object.entries(sku.specs).map(([key, value]) => `${key}: ${value}`).join(' / ') || sku.sku_code}<span className={styles.skuCode}>{sku.sku_code}</span></span><span>{sku.available_stock} 件</span></button>)}</div><div className={styles.quantity}><Button aria-label="减少数量" icon={<Minus size={15} />} iconOnly onClick={() => setQuantity((value) => Math.max(1, value - 1))} /><input aria-label="数量" min={1} onChange={(event) => setQuantity(Math.max(1, Number(event.currentTarget.value)))} type="number" value={quantity} /><Button aria-label="增加数量" icon={<Plus size={15} />} iconOnly onClick={() => setQuantity((value) => value + 1)} /></div><div className={styles.buy}><Button disabled={!detail.can_purchase || !selected?.can_purchase} icon={<ShoppingCart size={16} />} loading={busy} onClick={() => void addCart()} variant="primary">加入购物车</Button></div></section></div>
    <section className={styles.description}><h2 className="title">商品详情</h2>{detail.product.description}</section>
    {related.length > 0 && <section className={styles.related}><h2 className="title">相关内容</h2><div className={styles.relatedGrid}>{related.map((post) => <Card className={styles.relatedItem} key={post.id}><Link to={`/posts/${post.id}`}><strong>{post.title}</strong><p className="subtle">{post.body}</p></Link></Card>)}</div></section>}
  </AppShell>
}
