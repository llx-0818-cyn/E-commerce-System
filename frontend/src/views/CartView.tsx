import { useCallback, useEffect, useState } from 'react'
import { ArrowLeft, Minus, Plus, ReceiptText, ShoppingBag, Trash2 } from 'lucide-react'
import { Link, useNavigate } from 'react-router'

import * as checkoutApi from '../api/checkout'
import type { CartItem } from '../api/types'
import AppShell from '../components/AppShell'
import Button from '../components/ui/Button'
import Card from '../components/ui/Card'
import PageHeader from '../components/ui/PageHeader'
import { EmptyState, ErrorState, Skeleton } from '../components/ui/StatePanel'
import { useCatalog } from '../hooks/useCatalog'
import { useToastStore } from '../stores/toast'
import styles from './CartView.module.css'

export default function CartView() {
  const navigate = useNavigate()
  const catalog = useCatalog()
  const toast = useToastStore()
  const [items, setItems] = useState<CartItem[] | null>(null)
  const [error, setError] = useState('')
  const [busyId, setBusyId] = useState<number | null>(null)
  const load = useCallback(async () => { setError(''); try { setItems((await checkoutApi.listCart()).items) } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) } }, [])
  useEffect(() => { void load() }, [load])
  async function quantity(item: CartItem, value: number) {
    setBusyId(item.id)
    try {
      const updated = await checkoutApi.updateCartItem(item.id, Math.max(1, value))
      setItems((current) => current?.map((entry) => entry.id === item.id ? updated : entry) ?? [])
    } catch (reason) { toast.error(reason instanceof Error ? reason.message : '数量更新失败') }
    finally { setBusyId(null) }
  }
  async function remove(id: number) {
    setBusyId(id)
    try { await checkoutApi.deleteCartItem(id); setItems((current) => current?.filter((item) => item.id !== id) ?? []) }
    catch (reason) { toast.error(reason instanceof Error ? reason.message : '删除失败') }
    finally { setBusyId(null) }
  }
  const total = items?.reduce((sum, item) => sum + item.observed_price_cent * item.quantity, 0) ?? 0
  return <AppShell><PageHeader title="我的购物袋" description="把心动的小东西，带进每一个平凡日常。" />{error ? <ErrorState action={<Button onClick={() => void load()}>重试</Button>} description={error} /> : items === null ? <Skeleton height={220} /> : items.length === 0 ? <EmptyState title="购物车为空" description="先去逛一逛，遇见下一件喜欢的好物。" action={<Button icon={<ShoppingBag size={16}/>} onClick={() => navigate('/')} variant="primary">去发现好物</Button>} /> : <div className={styles.layout}><div><div className={styles.listLabel}>已选好物 <span>{items.reduce((sum,item) => sum+item.quantity,0)} 件</span></div><div className={styles.list}>{items.map((item) => {
    const product = catalog.products.find((value) => value.sku_id === item.sku_id)
    return <Card className={styles.item} key={item.id}><div className={styles.identity}>{product && <Link to={`/products/${product.id}`}><img alt={product.name} src={product.images[0]?.url}/></Link>}<div><div className={styles.name}>{product ? <Link to={`/products/${product.id}`}>{product.name}</Link> : `SKU #${item.sku_id}`}</div><p className={styles.spec}>{product?.subtitle}</p><div className={styles.observed}>加入时价格 ¥{(item.observed_price_cent / 100).toFixed(2)}</div></div></div><div className={styles.quantity}><Button aria-label="减少" disabled={busyId === item.id || item.quantity <= 1} icon={<Minus size={14} />} iconOnly onClick={() => void quantity(item, item.quantity - 1)} size="small" /><input aria-label={`SKU ${item.sku_id} 数量`} disabled={busyId === item.id} min={1} onChange={(event) => void quantity(item, Number(event.currentTarget.value))} type="number" value={item.quantity} /><Button aria-label="增加" disabled={busyId === item.id} icon={<Plus size={14} />} iconOnly onClick={() => void quantity(item, item.quantity + 1)} size="small" /></div><div className={styles.itemTotal}>¥{(item.observed_price_cent*item.quantity/100).toFixed(2)}</div><Button aria-label="删除" disabled={busyId === item.id} icon={<Trash2 size={15} />} iconOnly onClick={() => void remove(item.id)} title="删除" variant="ghost" /></Card>
  })}</div><Link className={styles.continue} to="/"><ArrowLeft size={13}/>继续寻找生活灵感</Link></div><aside className={styles.summary}><span className={styles.eyebrow}>YOUR EVERYDAY PICKS</span><h2>心动清单</h2><div className={styles.summaryLine}><span>商品金额（预估）</span><strong>¥{(total/100).toFixed(2)}</strong></div><div className={styles.summaryLine}><span>件数</span><span>{items.reduce((sum,item) => sum+item.quantity,0)} 件</span></div><div className={styles.total}><span>预估合计</span><strong>¥{(total/100).toFixed(2)}</strong></div><p>结算时会重新确认当前价格与库存。本站为本地体验站，使用模拟支付。</p><Button icon={<ReceiptText size={16} />} onClick={() => navigate('/checkout')} variant="primary">去结算</Button></aside></div>}</AppShell>
}
