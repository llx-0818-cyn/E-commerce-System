import { useEffect, useMemo, useState } from 'react'
import { ArrowLeft, ArrowUpRight, Check, ChevronRight, Minus, Plus, ShoppingBag, ShoppingCart, Star, Store } from 'lucide-react'
import { Link, useNavigate, useParams } from 'react-router'

import * as checkoutApi from '../api/checkout'
import * as storefrontApi from '../api/storefront'
import type { ContentPost, PublicProductDetail, PublicSKU } from '../api/types'
import AppShell from '../components/AppShell'
import Button from '../components/ui/Button'
import Card from '../components/ui/Card'
import UserAvatar from '../components/UserAvatar'
import { ErrorState, Skeleton } from '../components/ui/StatePanel'
import { useCatalog } from '../hooks/useCatalog'
import { useAuthStore } from '../stores/auth'
import { useToastStore } from '../stores/toast'
import styles from './ProductView.module.css'

export default function ProductView() {
  const { id = '0' } = useParams()
  const productId = Number(id)
  const navigate = useNavigate()
  const isLoggedIn = useAuthStore((state) => state.isLoggedIn)
  const toast = useToastStore()
  const catalog = useCatalog()
  const source = catalog.products.find((product) => product.id === productId)
  const [detail, setDetail] = useState<PublicProductDetail | null>(null)
  const [related, setRelated] = useState<ContentPost[]>([])
  const [selectedId, setSelectedId] = useState(0)
  const [activeImage, setActiveImage] = useState(0)
  const [quantity, setQuantity] = useState(1)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  useEffect(() => {
    let active = true
    setError('')
    setDetail(null)
    void Promise.all([storefrontApi.getProduct(productId), storefrontApi.listRelatedPosts(productId)]).then(([value, posts]) => {
      if (!active) return
      setDetail(value); setRelated(posts.posts); setActiveImage(0); setQuantity(1)
      setSelectedId(value.skus.find((sku) => sku.can_purchase)?.id ?? value.skus[0]?.id ?? 0)
    }).catch((reason: unknown) => { if (active) setError(reason instanceof Error ? reason.message : String(reason)) })
    return () => { active = false }
  }, [productId])
  const selected = useMemo<PublicSKU | undefined>(() => detail?.skus.find((sku) => sku.id === selectedId), [detail, selectedId])
  async function addCart() {
    if (!isLoggedIn) { navigate(`/account?redirect=${encodeURIComponent(`/products/${productId}`)}`); return }
    if (!selected?.can_purchase) return
    setBusy(true)
    try { await checkoutApi.addCartItem({ sku_id: selected.id, quantity, mode: 'merge' }); toast.success('好物已装进购物袋') }
    catch (reason) { toast.error(reason instanceof Error ? reason.message : '暂时无法加入购物袋') }
    finally { setBusy(false) }
  }
  if (error) return <AppShell><ErrorState description={error} /></AppShell>
  if (!detail) return <AppShell><Skeleton height={460} /></AppShell>
  const images = detail.images.length ? detail.images : [{ id: 0, image_url: detail.product.cover_url }]
  const price = selected?.price_cent ?? detail.min_price_cent
  return <AppShell>
    <nav className={styles.breadcrumb}><Link to="/"><ArrowLeft size={13} /> 发现</Link><ChevronRight size={12} /><Link to={`/shops/${detail.shop.shop.id}`}>{detail.shop.shop.name}</Link><ChevronRight size={12} /><span>好物详情</span></nav>
    <div className={styles.layout}>
      <div className={styles.gallery}>
        <div className={styles.mainImage}><img alt={detail.product.name} src={images[activeImage]?.image_url ?? images[0].image_url} /><span className={styles.imageLabel}>THE GOOD THINGS EDIT</span></div>
        {images.length > 1 && <div className={styles.thumbnails}>{images.map((image, index) => <button aria-label={`查看商品图片 ${index + 1}`} aria-pressed={activeImage === index} className={activeImage === index ? styles.activeThumb : ''} key={image.id} onClick={() => setActiveImage(index)}><img alt="" src={image.image_url} /></button>)}</div>}
      </div>
      <section className={styles.panel}>
        <Link className={styles.shop} to={`/shops/${detail.shop.shop.id}`}><Store size={14} /> {detail.shop.shop.name}<ArrowUpRight size={13} /></Link>
        <span className={styles.eyebrow}>{source?.category ?? 'DAILY ESSENTIALS'}</span>
        <h1>{detail.product.name}</h1><p className={styles.subtitle}>{detail.product.subtitle}</p>
        {source && <a className={styles.rating} href="#product-reviews"><span><Star fill="currentColor" size={14} /> {source.rating.toFixed(1)}</span><span>{source.review_count.toLocaleString()} 条外站评价</span><ChevronRight size={12} /></a>}
        <div className={styles.pricePanel}><div className={styles.price}>{price ? <><small>¥</small>{(price / 100).toFixed(2)}</> : '暂不可售'}</div>{source && <span className={styles.priceNote}>{source.price_note}</span>}<span className={styles.checkoutNote}>本地体验商品 · 模拟交易</span></div>
        {!detail.shop.accepting_orders && <div className={styles.notice}>店铺暂停营业</div>}
        <div className={styles.sectionLabel}>选择款式 <span>{detail.skus.length} 种选择</span></div>
        <div className={styles.skus}>{detail.skus.map((sku) => <button aria-pressed={selectedId === sku.id} className={[styles.sku, selectedId === sku.id ? styles.selected : ''].filter(Boolean).join(' ')} disabled={!sku.can_purchase} key={sku.id} onClick={() => { setSelectedId(sku.id); setQuantity(1) }} type="button"><span>{Object.entries(sku.specs).map(([key, value]) => `${key}: ${value}`).join(' / ') || sku.sku_code}<span className={styles.skuCode}>{sku.sku_code}</span></span>{selectedId === sku.id ? <Check size={16} /> : <span>{sku.available_stock} 件</span>}</button>)}</div>
        <div className={styles.sectionLabel}>数量<span>{selected ? `演示库存 ${selected.available_stock} 件` : ''}</span></div>
        <div className={styles.quantity}><Button aria-label="减少数量" disabled={quantity <= 1} icon={<Minus size={14} />} iconOnly onClick={() => setQuantity((value) => Math.max(1, value - 1))} /><input aria-label="数量" min={1} max={selected?.available_stock} onChange={(event) => setQuantity(Math.min(selected?.available_stock ?? 1, Math.max(1, Number(event.currentTarget.value) || 1)))} type="number" value={quantity} /><Button aria-label="增加数量" disabled={quantity >= (selected?.available_stock ?? 0)} icon={<Plus size={14} />} iconOnly onClick={() => setQuantity((value) => Math.min(selected?.available_stock ?? 1, value + 1))} /></div>
        <div className={styles.buy}><Button disabled={!detail.can_purchase || !selected?.can_purchase} icon={<ShoppingCart size={17} />} loading={busy} onClick={() => void addCart()} variant="primary">加入购物车</Button><Button icon={<ShoppingBag size={17} />} onClick={() => navigate('/cart')}>查看购物袋</Button></div>
        {source && <a className={styles.source} href={source.source_url} target="_blank" rel="noreferrer">商品资料来自 IKEA 公开页面 · 查看原商品 <ArrowUpRight size={12} /></a>}
      </section>
    </div>
    <section className={styles.description}><div className={styles.sectionHeading}><span>01 / THE DETAILS</span><h2>好物的细节</h2></div><p>{detail.product.description}</p></section>
    {source && <section className={styles.reviews} id="product-reviews"><div className={styles.sectionHeading}><span>02 / REAL VOICES</span><h2>来自生活的声音 <small>{source.rating.toFixed(1)} / 5</small></h2><p>以下为 IKEA 对应商品的公开评价短摘录，保留原文与原作者。头像仅作示意，不是评价作者本人；这些评价不代表本站成交。</p></div><div className={styles.reviewGrid}>{source.reviews.map((review) => <article className={styles.review} key={review.id}><div className={styles.reviewTop}><UserAvatar id={review.author_id} username={review.author} src={review.avatar_url} size={36} /><div><strong>{review.author}</strong><span>IKEA 公开评价 · {review.date}</span></div><span className={styles.reviewStars}>{'★'.repeat(review.rating)}{'☆'.repeat(5 - review.rating)}</span></div><blockquote>{review.text}</blockquote><a href={review.source_url} target="_blank" rel="noreferrer">查看评价来源 <ArrowUpRight size={11} /></a></article>)}</div></section>}
    {related.length > 0 && <section className={styles.related}><div className={styles.sectionHeading}><span>03 / THE EVERYDAY STORIES</span><h2>关于它的生活灵感</h2></div><div className={styles.relatedGrid}>{related.map((post) => <Card className={styles.relatedItem} key={post.id} padded={false}><Link to={`/posts/${post.id}`}>{post.images?.[0] && <img alt={post.title} src={post.images?.[0].image_url} />}<div><strong>{post.title}</strong><p>{post.body}</p><span>读一篇笔记 <ArrowUpRight size={12} /></span></div></Link></Card>)}</div></section>}
  </AppShell>
}
