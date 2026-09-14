import { useCallback, useEffect, useState } from 'react'
import { CircleAlert, RefreshCw, RotateCcw, XCircle } from 'lucide-react'
import { useParams } from 'react-router'

import * as orderApi from '../api/orders'
import * as paymentApi from '../api/payment'
import type { Order, PaymentSession } from '../api/types'
import AppShell from '../components/AppShell'
import Button from '../components/ui/Button'
import Card from '../components/ui/Card'
import PageHeader from '../components/ui/PageHeader'
import { ErrorState, Skeleton } from '../components/ui/StatePanel'
import styles from './OrderDetailView.module.css'

const fallback = (label: string) => label.includes('包') ? '/assets/feed-bag.jpg' : label.includes('杯') ? '/assets/feed-cup.jpg' : '/assets/feed-coffee.jpg'
const steps = ['pending_payment', 'paid', 'shipped', 'completed'] as const

export default function OrderDetailView() {
  const { id = '0' } = useParams()
  const orderId = Number(id)
  const [order, setOrder] = useState<Order | null>(null)
  const [payment, setPayment] = useState<PaymentSession | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')
  const load = useCallback(async () => { try { setOrder(await orderApi.getOrder(orderId)); setError('') } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) } }, [orderId])
  useEffect(() => { void load() }, [load])
  async function cancel() { setBusy('cancel'); try { setOrder(await orderApi.cancelOrder(orderId)) } finally { setBusy('') } }
  async function confirm() { setBusy('confirm'); try { setOrder(await orderApi.confirmReceipt(orderId)) } finally { setBusy('') } }
  async function createPayment() { if (!order) return; setBusy('create'); try { setPayment(await paymentApi.createPayment(order.id, order.total_cent)) } finally { setBusy('') } }
  async function simulate(action: 'success' | 'failure' | 'repeat') {
    if (!payment) return
    setBusy(action)
    try {
      if (action === 'success') await paymentApi.simulateSuccess(payment.payment.payment_no)
      else if (action === 'failure') await paymentApi.simulateFailure(payment.payment.payment_no)
      else await paymentApi.repeatCallback(payment.payment.payment_no)
      if (action !== 'repeat') await load()
    } finally { setBusy('') }
  }
  if (error) return <AppShell><ErrorState description={error} /></AppShell>
  if (!order) return <AppShell><Skeleton height={420} /></AppShell>
  const currentIndex = order.status === 'canceled' ? 0 : steps.indexOf(order.status as typeof steps[number])
  return <AppShell><PageHeader title={`订单 ${order.order_no}`} /><div className={styles.demo}><CircleAlert size={20} /><div><strong>演示环境，不产生真实支付</strong><div>本地 Mock 回调仅用于验证订单、支付和库存状态。</div></div></div><div className={styles.layout}><div className={styles.main}><Card><h2 className="title">商品</h2>{order.items?.map((item) => <div className={styles.item} key={item.id}><img alt="" onError={(event) => { event.currentTarget.onerror = null; event.currentTarget.src = fallback(item.product_name) }} src={item.cover_url} /><div><strong>{item.product_name}</strong><div className="subtle">{item.sku_code} · {Object.values(item.specs).join(' / ')} · ×{item.quantity}</div></div><span>¥{(item.subtotal_cent / 100).toFixed(2)}</span></div>)}</Card><Card><h2 className="title">收货地址</h2><div className={styles.address}>{order.address_snapshot.recipient_name} {order.address_snapshot.phone}<br />{order.address_snapshot.province}{order.address_snapshot.city}{order.address_snapshot.district}{order.address_snapshot.detail}</div></Card></div><aside className={styles.sidebar}><Card><h2 className="title">状态</h2><div className={styles.timeline}>{steps.map((step, index) => <div className={[styles.step, index <= currentIndex && order.status !== 'canceled' ? styles.done : ''].filter(Boolean).join(' ')} key={step}><span className={styles.dot} /><span>{step === 'pending_payment' ? '待支付' : step === 'paid' ? '已支付' : step === 'shipped' ? '已发货' : '已完成'}</span></div>)}{order.status === 'canceled' && <div className={[styles.step, styles.done].join(' ')}><span className={styles.dot} /><span>已取消</span></div>}</div><div className={styles.amount} style={{ marginTop: 16 }}>合计 ¥{(order.total_cent / 100).toFixed(2)}</div></Card>{order.status === 'pending_payment' && <Card className={styles.cashier}><strong>Mock 收银台</strong><span className="subtle">演示环境，不产生真实支付</span>{!payment ? <Button loading={busy === 'create'} onClick={() => void createPayment()} variant="primary">创建支付单</Button> : <><code>{payment.payment.payment_no}</code><div className={styles.actions}><Button loading={busy === 'success'} onClick={() => void simulate('success')} variant="primary">模拟成功</Button><Button loading={busy === 'failure'} onClick={() => void simulate('failure')} variant="danger">模拟失败</Button><Button icon={<RotateCcw size={15} />} loading={busy === 'repeat'} onClick={() => void simulate('repeat')}>重复回调</Button></div></>}<Button icon={<XCircle size={15} />} loading={busy === 'cancel'} onClick={() => void cancel()} variant="ghost">取消订单</Button></Card>}{order.status === 'shipped' && <Button loading={busy === 'confirm'} onClick={() => void confirm()} variant="primary">确认收货</Button>}<Button icon={<RefreshCw size={15} />} onClick={() => void load()} variant="ghost">刷新状态</Button></aside></div></AppShell>
}
