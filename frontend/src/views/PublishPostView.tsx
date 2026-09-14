import { useEffect, useState } from 'react'
import { ArrowLeft, ArrowRight, ImagePlus, Send, Trash2 } from 'lucide-react'
import { useNavigate } from 'react-router'

import * as contentApi from '../api/content'
import * as storefrontApi from '../api/storefront'
import type { PostImage, PublicProductItem } from '../api/types'
import AppShell from '../components/AppShell'
import Button from '../components/ui/Button'
import Card from '../components/ui/Card'
import PageHeader from '../components/ui/PageHeader'
import styles from './PublishPostView.module.css'

export function moveItem<T>(items: T[], from: number, to: number) {
  const copy = [...items]
  const [item] = copy.splice(from, 1)
  if (item !== undefined) copy.splice(to, 0, item)
  return copy
}

export default function PublishPostView() {
  const navigate = useNavigate()
  const [form, setForm] = useState({ title: '', body: '', tags: '' })
  const [images, setImages] = useState<PostImage[]>([])
  const [products, setProducts] = useState<PublicProductItem[]>([])
  const [selectedProducts, setSelectedProducts] = useState<number[]>([])
  const [dragIndex, setDragIndex] = useState<number | null>(null)
  const [busy, setBusy] = useState(false)

  useEffect(() => {
    void storefrontApi.getDashboard().then((dashboard) => storefrontApi.listShopProducts(dashboard.shop_id, 50)).then((page) => setProducts(page.products)).catch(() => setProducts([]))
  }, [])

  async function upload(files: FileList | null) {
    if (!files) return
    setBusy(true)
    try {
      const uploaded = await Promise.all(Array.from(files).slice(0, Math.max(0, 9 - images.length)).map(contentApi.uploadImage))
      setImages((items) => items.concat(uploaded))
    } finally { setBusy(false) }
  }

  async function submit() {
    if (!form.title.trim() || !form.body.trim()) return
    setBusy(true)
    try {
      const post = await contentApi.createPost({ title: form.title, body: form.body, image_ids: images.map((item) => item.id), tags: form.tags.split(/[,，\s]+/).filter(Boolean), product_ids: selectedProducts })
      navigate(`/posts/${post.id}`)
    } finally { setBusy(false) }
  }

  return <AppShell>
    <PageHeader title="发布内容" />
    <Card><form className={styles.form} onSubmit={(event) => { event.preventDefault(); void submit() }}>
      <div className={styles.section}><label htmlFor="post-title">标题</label><input id="post-title" maxLength={200} onChange={(event) => setForm({ ...form, title: event.currentTarget.value })} value={form.title} /></div>
      <div className={styles.section}><label htmlFor="post-body">正文</label><textarea id="post-body" maxLength={10000} onChange={(event) => setForm({ ...form, body: event.currentTarget.value })} value={form.body} /></div>
      <div className={styles.section}><label htmlFor="post-tags">标签</label><input id="post-tags" onChange={(event) => setForm({ ...form, tags: event.currentTarget.value })} value={form.tags} /></div>
      <div className={styles.section}><label htmlFor="post-images">图片</label><input accept="image/jpeg,image/png,image/gif" id="post-images" multiple onChange={(event) => void upload(event.currentTarget.files)} type="file" /><div className={styles.images}>{images.map((image, index) => <div className={styles.image} draggable key={image.id} onDragEnd={() => setDragIndex(null)} onDragOver={(event) => event.preventDefault()} onDragStart={() => setDragIndex(index)} onDrop={() => { if (dragIndex !== null) setImages((items) => moveItem(items, dragIndex, index)); setDragIndex(null) }}><img alt="" loading="lazy" src={image.image_url} /><div className={styles.imageActions}><Button aria-label="向前移动图片" disabled={index === 0} icon={<ArrowLeft size={15} />} iconOnly onClick={() => setImages((items) => moveItem(items, index, index - 1))} size="small" title="向前移动" /><Button aria-label="向后移动图片" disabled={index === images.length - 1} icon={<ArrowRight size={15} />} iconOnly onClick={() => setImages((items) => moveItem(items, index, index + 1))} size="small" title="向后移动" /><Button aria-label="移除图片" icon={<Trash2 size={15} />} iconOnly onClick={() => setImages((items) => items.filter((item) => item.id !== image.id))} size="small" title="移除图片" variant="danger" /></div></div>)}</div></div>
      {products.length > 0 && <div className={styles.section}><span className="subtle">关联商品</span><div className={styles.products}>{products.map((item) => <label className={styles.product} key={item.product.id}><input checked={selectedProducts.includes(item.product.id)} onChange={(event) => setSelectedProducts((ids) => event.currentTarget.checked ? ids.concat(item.product.id) : ids.filter((id) => id !== item.product.id))} type="checkbox" /><span>{item.product.name}</span><span className="subtle">{item.min_price_cent ? `¥${(item.min_price_cent / 100).toFixed(2)}` : '不可售'}</span></label>)}</div></div>}
      <div className={styles.footer}><Button icon={<ImagePlus size={16} />} onClick={() => document.getElementById('post-images')?.click()} type="button">添加图片</Button><Button icon={<Send size={16} />} loading={busy} type="submit" variant="primary">发布</Button></div>
    </form></Card>
  </AppShell>
}
