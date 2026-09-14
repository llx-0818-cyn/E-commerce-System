import { useEffect, useState } from 'react'
import { ChevronRight } from 'lucide-react'
import { Link } from 'react-router'

import * as orderApi from '../api/orders'
import type { Order, OrderStatus } from '../api/types'
import AppShell from '../components/AppShell'
import Button from '../components/ui/Button'
import Card from '../components/ui/Card'
import PageHeader from '../components/ui/PageHeader'
import { EmptyState, ErrorState, Skeleton } from '../components/ui/StatePanel'
import styles from './OrdersView.module.css'

const labels: Record<OrderStatus, string> = { pending_payment: '待支付', paid: '待发货', shipped: '待收货', completed: '已完成', canceled: '已取消' }

export default function OrdersView() {
  const [status, setStatus] = useState<OrderStatus | undefined>()
  const [orders, setOrders] = useState<Order[] | null>(null)
  const [error, setError] = useState('')
  useEffect(() => { setOrders(null); setError(''); void orderApi.listOrders(status).then((page) => setOrders(page.orders)).catch((reason: unknown) => setError(reason instanceof Error ? reason.message : String(reason))) }, [status])
  return <AppShell><PageHeader title="我的订单" /><div className={styles.filters}>{[[undefined, '全部'], ...Object.entries(labels)] .map(([value, label]) => <button className={[styles.filter, status === value ? styles.active : ''].filter(Boolean).join(' ')} key={String(value)} onClick={() => setStatus(value as OrderStatus | undefined)} type="button">{label}</button>)}</div>{error ? <ErrorState description={error} /> : orders === null ? <Skeleton height={240} /> : orders.length === 0 ? <EmptyState title="暂无订单" /> : <div className={styles.list}>{orders.map((order) => <Card className={styles.order} key={order.id}><div><div className={styles.number}>{order.order_no}</div><div className={styles.meta}><span>店铺 #{order.shop_id}</span><span>{new Date(order.created_at).toLocaleString()}</span><span>{order.items?.length ?? 0} 件商品</span></div></div><div className={styles.right}><span className={styles.status}>{labels[order.status]}</span><span className={styles.amount}>¥{(order.total_cent / 100).toFixed(2)}</span><Link to={`/orders/${order.id}`}><Button icon={<ChevronRight size={15} />} size="small">详情</Button></Link></div></Card>)}</div>}</AppShell>
}
