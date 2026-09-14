import { useCallback, useEffect, useState } from 'react'
import { Minus, Plus, ReceiptText, Trash2 } from 'lucide-react'
import { useNavigate } from 'react-router'

import * as checkoutApi from '../api/checkout'
import type { CartItem } from '../api/types'
import AppShell from '../components/AppShell'
import Button from '../components/ui/Button'
import Card from '../components/ui/Card'
import PageHeader from '../components/ui/PageHeader'
import { EmptyState, ErrorState, Skeleton } from '../components/ui/StatePanel'
import styles from './CartView.module.css'

export default function CartView() {
  const navigate = useNavigate()
  const [items, setItems] = useState<CartItem[] | null>(null)
  const [error, setError] = useState('')
  const load = useCallback(async () => { try { setItems((await checkoutApi.listCart()).items) } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) } }, [])
  useEffect(() => { void load() }, [load])
  async function quantity(item: CartItem, value: number) {
    const updated = await checkoutApi.updateCartItem(item.id, Math.max(1, value))
    setItems((current) => current?.map((entry) => entry.id === item.id ? updated : entry) ?? [])
  }
  async function remove(id: number) { await checkoutApi.deleteCartItem(id); setItems((current) => current?.filter((item) => item.id !== id) ?? []) }
  return <AppShell><PageHeader title="购物车" />{error ? <ErrorState action={<Button onClick={() => void load()}>重试</Button>} description={error} /> : items === null ? <Skeleton height={220} /> : items.length === 0 ? <EmptyState title="购物车为空" /> : <><div className={styles.list}>{items.map((item) => <Card className={styles.item} key={item.id}><div><div className={styles.name}>SKU #{item.sku_id}</div><div className="subtle">加入时价格 ¥{(item.observed_price_cent / 100).toFixed(2)}</div></div><div className={styles.quantity}><Button aria-label="减少" icon={<Minus size={14} />} iconOnly onClick={() => void quantity(item, item.quantity - 1)} size="small" /><input aria-label={`SKU ${item.sku_id} 数量`} min={1} onChange={(event) => void quantity(item, Number(event.currentTarget.value))} type="number" value={item.quantity} /><Button aria-label="增加" icon={<Plus size={14} />} iconOnly onClick={() => void quantity(item, item.quantity + 1)} size="small" /></div><Button aria-label="删除" icon={<Trash2 size={15} />} iconOnly onClick={() => void remove(item.id)} title="删除" variant="danger" /></Card>)}</div><div className={styles.footer}><Button icon={<ReceiptText size={16} />} onClick={() => navigate('/checkout')} variant="primary">去结算</Button></div></>}</AppShell>
}
