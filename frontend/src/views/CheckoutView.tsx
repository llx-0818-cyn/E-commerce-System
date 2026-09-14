import { useCallback, useEffect, useState } from 'react'
import { MapPin, RefreshCw, Send } from 'lucide-react'
import { Link } from 'react-router'

import * as checkoutApi from '../api/checkout'
import * as orderApi from '../api/orders'
import type { Address, CartItem, CheckoutPreview, OrderGroup } from '../api/types'
import AppShell from '../components/AppShell'
import Button from '../components/ui/Button'
import Card from '../components/ui/Card'
import PageHeader from '../components/ui/PageHeader'
import { EmptyState, ErrorState, Skeleton } from '../components/ui/StatePanel'
import styles from './CheckoutView.module.css'

const issueLabel: Record<string, string> = { shop_unavailable: '店铺不可交易', product_unavailable: '商品已下架', sku_unavailable: 'SKU 不可售', stock_insufficient: '库存不足' }
const fallback = (label: string) => label.includes('包') ? '/assets/feed-bag.jpg' : label.includes('杯') ? '/assets/feed-cup.jpg' : '/assets/feed-coffee.jpg'

export default function CheckoutView() {
  const [cart, setCart] = useState<CartItem[]>([])
  const [addresses, setAddresses] = useState<Address[]>([])
  const [addressId, setAddressId] = useState(0)
  const [preview, setPreview] = useState<CheckoutPreview | null>(null)
  const [acceptChanges, setAcceptChanges] = useState(false)
  const [created, setCreated] = useState<OrderGroup | null>(null)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [form, setForm] = useState({ recipient_name: '', phone: '', province: '', city: '', district: '', detail: '', postal_code: '' })

  const load = useCallback(async () => {
    setLoading(true); setError('')
    try {
      const [cartResult, addressResult] = await Promise.all([checkoutApi.listCart(), checkoutApi.listAddresses()])
      setCart(cartResult.items); setAddresses(addressResult.addresses)
      const selected = addressResult.addresses.find((item) => item.is_default)?.id ?? addressResult.addresses[0]?.id ?? 0
      setAddressId(selected)
      if (cartResult.items.length && selected) setPreview(await checkoutApi.preview({ cart_item_ids: cartResult.items.map((item) => item.id), address_id: selected, accept_price_changes: false }))
    } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) } finally { setLoading(false) }
  }, [])
  useEffect(() => { void load() }, [load])

  async function refresh(accept = acceptChanges, selectedAddress = addressId) {
    if (!selectedAddress || cart.length === 0) return
    setPreview(await checkoutApi.preview({ cart_item_ids: cart.map((item) => item.id), address_id: selectedAddress, accept_price_changes: accept }))
  }
  async function createAddress() {
    const value = await checkoutApi.createAddress({ ...form, is_default: addresses.length === 0 })
    setAddresses((items) => items.concat(value)); setAddressId(value.id); await refresh(acceptChanges, value.id)
  }
  async function submit() {
    if (!preview?.can_submit) return
    const drafts = preview.shops.map((shop) => ({ shop_id: shop.shop_id, items: shop.items.map((item) => ({ sku_id: item.sku_id, quantity: item.quantity, expected_price_cent: item.current_price_cent })) }))
    setCreated(await orderApi.createOrder(preview.address.id, drafts, crypto.randomUUID()))
  }
  if (loading) return <AppShell><Skeleton height={420} /></AppShell>
  if (error) return <AppShell><ErrorState action={<Button onClick={() => void load()}>重试</Button>} description={error} /></AppShell>
  if (cart.length === 0) return <AppShell><EmptyState title="购物车为空" /></AppShell>
  return <AppShell><PageHeader title="确认订单" actions={<Button icon={<RefreshCw size={15} />} onClick={() => void refresh()}>刷新</Button>} /><div className={styles.layout}><div className={styles.main}><section><h2 className="title">收货地址</h2>{addresses.length > 0 && <div className={styles.addressList}>{addresses.map((address) => <label className={styles.address} key={address.id}><input checked={addressId === address.id} name="address" onChange={() => { setAddressId(address.id); void refresh(acceptChanges, address.id) }} type="radio" /><span><strong>{address.recipient_name} {address.phone}</strong><br /><span className="subtle">{address.province}{address.city}{address.district}{address.detail}</span></span></label>)}</div>}<Card className={styles.addressForm}><input aria-label="收件人" onChange={(event) => setForm({ ...form, recipient_name: event.currentTarget.value })} placeholder="收件人" /><input aria-label="手机号" onChange={(event) => setForm({ ...form, phone: event.currentTarget.value })} placeholder="手机号" /><input aria-label="省份" onChange={(event) => setForm({ ...form, province: event.currentTarget.value })} placeholder="省份" /><input aria-label="城市" onChange={(event) => setForm({ ...form, city: event.currentTarget.value })} placeholder="城市" /><input aria-label="区县" onChange={(event) => setForm({ ...form, district: event.currentTarget.value })} placeholder="区县" /><input aria-label="详细地址" className={styles.full} onChange={(event) => setForm({ ...form, detail: event.currentTarget.value })} placeholder="详细地址" /><Button className={styles.full} icon={<MapPin size={15} />} onClick={() => void createAddress()} type="button">保存地址</Button></Card></section>{preview && <section className={styles.groups}>{preview.shops.map((shop) => <Card key={shop.shop_id}><div className={styles.groupTitle}><strong>{shop.shop_name}</strong><span>¥{(shop.subtotal_cent / 100).toFixed(2)}</span></div>{shop.items.map((item) => <div className={styles.line} key={item.cart_item_id}><img alt="" onError={(event) => { event.currentTarget.onerror = null; event.currentTarget.src = fallback(item.product_name) }} src={item.cover_url} /><div><strong>{item.product_name}</strong><div className="subtle">{item.sku_code} × {item.quantity}</div>{item.price_changed && <div className={styles.changed}>¥{(item.observed_price_cent / 100).toFixed(2)} → ¥{(item.current_price_cent / 100).toFixed(2)}</div>}{item.issue && <div className={styles.issue}>{issueLabel[item.issue] ?? item.issue}</div>}</div><strong>¥{(item.subtotal_cent / 100).toFixed(2)}</strong></div>)}</Card>)}</section>}</div>{preview && <Card className={styles.summary}><h2 className="title">订单汇总</h2><div className={styles.total}><span>合计</span><span>¥{(preview.total_cent / 100).toFixed(2)}</span></div>{preview.has_price_changes && <label className={styles.confirm}><input checked={acceptChanges} onChange={(event) => { setAcceptChanges(event.currentTarget.checked); void refresh(event.currentTarget.checked) }} type="checkbox" /><span>接受最新价格</span></label>}<Button disabled={!preview.can_submit} icon={<Send size={16} />} onClick={() => void submit()} style={{ width: '100%', marginTop: 16 }} variant="primary">提交订单</Button>{created && <div className={styles.orders}>{created.orders.map((order) => <Link className="pill" key={order.id} to={`/orders/${order.id}`}>{order.order_no}</Link>)}</div>}</Card>}</div></AppShell>
}
