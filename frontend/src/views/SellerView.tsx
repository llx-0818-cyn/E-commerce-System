import { useCallback, useEffect, useState } from 'react'
import { Box, PackagePlus, RefreshCw, Store, Truck } from 'lucide-react'

import * as orderApi from '../api/orders'
import * as storefrontApi from '../api/storefront'
import type { Order, Product, SellerDashboard, SellerProductDetail, Shop } from '../api/types'
import AppShell from '../components/AppShell'
import Button from '../components/ui/Button'
import Card from '../components/ui/Card'
import PageHeader from '../components/ui/PageHeader'
import { EmptyState, Skeleton } from '../components/ui/StatePanel'
import styles from './SellerView.module.css'

type Tab = 'overview' | 'shop' | 'products' | 'orders'

export default function SellerView() {
  const [tab, setTab] = useState<Tab>('overview')
  const [dashboard, setDashboard] = useState<SellerDashboard | null>(null)
  const [shop, setShop] = useState<Shop | null>(null)
  const [products, setProducts] = useState<Product[]>([])
  const [selected, setSelected] = useState<SellerProductDetail | null>(null)
  const [orders, setOrders] = useState<Order[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState('')
  const [shopForm, setShopForm] = useState({ name: '', description: '', avatar_url: '', banner_url: '' })
  const [productForm, setProductForm] = useState({ name: '', subtitle: '', description: '', cover_url: '' })
  const [skuForm, setSKUForm] = useState({ sku_code: '', price_cent: 0, available_stock: 0 })

  const load = useCallback(async () => {
    setLoading(true); setError('')
    try {
      const [dashboardValue, shopValue, productPage] = await Promise.all([storefrontApi.getDashboard(), storefrontApi.getOwnedShop(), storefrontApi.listSellerProducts()])
      setDashboard(dashboardValue); setShop(shopValue); setShopForm({ name: shopValue.name, description: shopValue.description, avatar_url: shopValue.avatar_url, banner_url: shopValue.banner_url }); setProducts(productPage.products)
      setOrders((await orderApi.listSellerOrders(shopValue.id, 'paid')).orders)
      if (productPage.products[0]) setSelected(await storefrontApi.getSellerProduct(productPage.products[0].id))
    } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) } finally { setLoading(false) }
  }, [])
  useEffect(() => { void load() }, [load])

  async function saveShop() {
    if (shop) setShop(await storefrontApi.updateShop(shop.id, shopForm))
    else { const value = await storefrontApi.createShop(shopForm); setShop(value); await load() }
  }
  async function createProduct() {
    const value = await storefrontApi.createProduct(productForm)
    setProducts((items) => [value, ...items]); setSelected(await storefrontApi.getSellerProduct(value.id)); setProductForm({ name: '', subtitle: '', description: '', cover_url: '' })
  }
  async function createSKU() {
    if (!selected) return
    await storefrontApi.createSKU(selected.product.id, skuForm)
    setSelected(await storefrontApi.getSellerProduct(selected.product.id)); setSKUForm({ sku_code: '', price_cent: 0, available_stock: 0 })
  }
  async function stock(id: number, delta: number) { await storefrontApi.adjustStock(id, delta); if (selected) setSelected(await storefrontApi.getSellerProduct(selected.product.id)); await refreshDashboard() }
  async function refreshDashboard() { if (shop) setDashboard(await storefrontApi.getDashboard()) }
  async function ship(id: number) { await orderApi.shipOrder(id); setOrders((items) => items.filter((item) => item.id !== id)); await refreshDashboard() }

  if (loading) return <AppShell><Skeleton height={420} /></AppShell>
  if (error && !shop) return <AppShell><PageHeader title="创建店铺" /><Card><div className={styles.form}><input aria-label="店铺名称" onChange={(event) => setShopForm({ ...shopForm, name: event.currentTarget.value })} placeholder="店铺名称" value={shopForm.name} /><textarea aria-label="店铺简介" className={styles.full} onChange={(event) => setShopForm({ ...shopForm, description: event.currentTarget.value })} placeholder="店铺简介" value={shopForm.description} /><div className={styles.full}><Button icon={<Store size={15} />} onClick={() => void saveShop()} variant="primary">创建店铺</Button></div></div></Card></AppShell>
  return <AppShell><PageHeader actions={<Button icon={<RefreshCw size={15} />} onClick={() => void load()}>刷新</Button>} title="卖家中心" /><div className={styles.tabs}>{([['overview', '概览'], ['shop', '店铺'], ['products', '商品与库存'], ['orders', '待发货']] as const).map(([value, label]) => <button className={[styles.tab, tab === value ? styles.active : ''].filter(Boolean).join(' ')} key={value} onClick={() => setTab(value)} type="button">{label}</button>)}</div>
    {tab === 'overview' && dashboard && <div className={styles.stats}>{[['商品', dashboard.product_count], ['SKU', dashboard.sku_count], ['可用库存', dashboard.available_stock], ['待发货', dashboard.pending_shipment_orders], ['锁定库存', dashboard.locked_stock], ['累计售出', dashboard.sold_stock], ['已完成', dashboard.completed_orders]].map(([label, value]) => <Card className={styles.stat} key={String(label)}><span className="subtle">{label}</span><div className={styles.statValue}>{value}</div></Card>)}</div>}
    {tab === 'shop' && <Card><div className={styles.form}><input aria-label="店铺名称" onChange={(event) => setShopForm({ ...shopForm, name: event.currentTarget.value })} placeholder="店铺名称" value={shopForm.name} /><input aria-label="店铺头像" onChange={(event) => setShopForm({ ...shopForm, avatar_url: event.currentTarget.value })} placeholder="头像 URL" value={shopForm.avatar_url} /><textarea aria-label="店铺简介" className={styles.full} onChange={(event) => setShopForm({ ...shopForm, description: event.currentTarget.value })} placeholder="店铺简介" value={shopForm.description} /><div className={styles.full}><Button icon={<Store size={15} />} onClick={() => void saveShop()} variant="primary">保存店铺</Button>{shop && <Button onClick={() => void storefrontApi.setShopStatus(shop.id, shop.status === 'open' ? 'closed' : 'open')} style={{ marginLeft: 8 }}>{shop.status === 'open' ? '暂停营业' : '开始营业'}</Button>}</div></div></Card>}
    {tab === 'products' && <><Card><div className={styles.toolbar}><strong>新建商品</strong><Button icon={<PackagePlus size={15} />} onClick={() => void createProduct()} variant="primary">创建</Button></div><div className={styles.form}><input aria-label="商品名称" onChange={(event) => setProductForm({ ...productForm, name: event.currentTarget.value })} placeholder="商品名称" value={productForm.name} /><input aria-label="商品副标题" onChange={(event) => setProductForm({ ...productForm, subtitle: event.currentTarget.value })} placeholder="副标题" value={productForm.subtitle} /><textarea aria-label="商品描述" className={styles.full} onChange={(event) => setProductForm({ ...productForm, description: event.currentTarget.value })} placeholder="商品描述" value={productForm.description} /></div></Card><div className={styles.productLayout} style={{ marginTop: 16 }}><div className={styles.productList}>{products.map((product) => <button className={[styles.productButton, selected?.product.id === product.id ? styles.selected : ''].filter(Boolean).join(' ')} key={product.id} onClick={() => void storefrontApi.getSellerProduct(product.id).then(setSelected)} type="button"><strong>{product.name}</strong><span className="subtle">{product.status}</span></button>)}</div>{selected ? <Card><div className={styles.toolbar}><div><strong>{selected.product.name}</strong><div className="subtle">{selected.product.status}</div></div><Button onClick={() => void storefrontApi.setProductStatus(selected.product.id, selected.product.status === 'on_sale' ? 'off_sale' : 'on_sale')}>{selected.product.status === 'on_sale' ? '下架' : '上架'}</Button></div><div className={styles.table}>{selected.skus.map((sku) => <div className={styles.row} key={sku.id}><span><strong>{Object.values(sku.specs).join(' / ') || sku.sku_code}</strong><span className={styles.code}>{sku.sku_code}</span></span><span className={styles.stock}>{sku.available_stock}</span><Button onClick={() => void stock(sku.id, 1)} size="small">+1</Button><Button onClick={() => void stock(sku.id, -1)} size="small">-1</Button><Button onClick={() => void storefrontApi.setSKUStatus(sku.id, sku.status === 'active' ? 'inactive' : 'active')} size="small">{sku.status === 'active' ? '停用' : '启用'}</Button></div>)}</div><div className={styles.form} style={{ marginTop: 16 }}><input aria-label="SKU Code" onChange={(event) => setSKUForm({ ...skuForm, sku_code: event.currentTarget.value })} placeholder="SKU Code" value={skuForm.sku_code} /><input aria-label="价格" min={1} onChange={(event) => setSKUForm({ ...skuForm, price_cent: Number(event.currentTarget.value) })} placeholder="价格（分）" type="number" value={skuForm.price_cent || ''} /><input aria-label="初始库存" min={0} onChange={(event) => setSKUForm({ ...skuForm, available_stock: Number(event.currentTarget.value) })} placeholder="初始库存" type="number" value={skuForm.available_stock || ''} /><Button icon={<Box size={15} />} onClick={() => void createSKU()}>添加 SKU</Button></div></Card> : <EmptyState title="请选择商品" />}</div></>}
    {tab === 'orders' && (orders.length === 0 ? <EmptyState title="暂无待发货订单" /> : <div className={styles.orders}>{orders.map((order) => <Card className={styles.order} key={order.id}><div><strong>{order.order_no}</strong><div className="subtle">¥{(order.total_cent / 100).toFixed(2)}</div></div><span>{order.items?.length ?? 0} 件</span><Button icon={<Truck size={15} />} onClick={() => void ship(order.id)} variant="primary">发货</Button></Card>)}</div>)}
  </AppShell>
}
