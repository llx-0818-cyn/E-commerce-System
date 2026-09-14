import { Heart, MessageCircle, ShoppingBag, Star } from 'lucide-react'
import { Link } from 'react-router'

import type { ContentFeedItem } from '../api/types'
import UserAvatar from './UserAvatar'
import Card from './ui/Card'
import styles from './ContentFeedCard.module.css'

const money = (cent: number) => `¥${(cent / 100).toFixed(2)}`
const fallbackAsset = (label: string) => label.includes('包') ? '/assets/feed-bag.jpg' : label.includes('杯') ? '/assets/feed-cup.jpg' : '/assets/feed-coffee.jpg'

export default function ContentFeedCard({ item }: { item: ContentFeedItem }) {
  const images = item.images.slice(0, 4)
  return <Card className={styles.card} data-testid={`feed-item-${item.id}`} interactive padded={false}>
    {images.length ? <Link aria-label={`查看 ${item.title}`} className={[styles.images, images.length === 1 ? styles.single : ''].filter(Boolean).join(' ')} to={`/posts/${item.id}`}>{images.map((image) => <img alt="" className={styles.image} key={image.id} loading="lazy" onError={(event) => { event.currentTarget.onerror = null; event.currentTarget.src = fallbackAsset(item.title) }} src={image.image_url} />)}</Link>
      : <Link className={styles.textOnly} to={`/posts/${item.id}`}><p>{item.body}</p></Link>}
    <div className={styles.body}>
      <div className={styles.author}><Link className={styles.authorLink} to={`/u/${item.author.id}`}><UserAvatar id={item.author.id} size={32} username={item.author.username} /><span className={styles.authorName}>@{item.author.username}</span></Link><time className={styles.date}>{new Date(item.published_at).toLocaleDateString()}</time></div>
      <Link to={`/posts/${item.id}`}><h2 className={styles.title}>{item.title}</h2></Link>
      {images.length > 0 && <p className={styles.copy}>{item.body}</p>}
      <div className={styles.tags}>{item.tags.map((tag) => <span className={styles.tag} key={tag.id}>#{tag.name}</span>)}</div>
      {(item.shop || item.products.length > 0) && <div className={styles.commerce}>{item.shop && <Link className={styles.shop} to={`/shops/${item.shop.id}`}><ShoppingBag size={15} />{item.shop.name}</Link>}<div className={styles.products}>{item.products.map((product) => <Link className={styles.product} key={product.id} to={`/products/${product.id}`}><img alt="" loading="lazy" onError={(event) => { event.currentTarget.onerror = null; event.currentTarget.src = fallbackAsset(product.name) }} src={product.cover_url} /><span className={styles.productName}>{product.name}</span><span className={styles.price}>{money(product.min_price_cent)}</span></Link>)}</div></div>}
      <div className={styles.metrics}><span className={styles.metric}><Heart size={14} />{item.likes_count}</span><span className={styles.metric}><Star size={14} />{item.favorites_count}</span><span className={styles.metric}><MessageCircle size={14} />{item.comments_count}</span></div>
    </div>
  </Card>
}
