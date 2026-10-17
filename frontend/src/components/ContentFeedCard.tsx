import { ArrowUpRight, Heart, Images, ShoppingBag } from 'lucide-react'
import { Link } from 'react-router'

import type { ContentFeedItem } from '../api/types'
import UserAvatar from './UserAvatar'
import Card from './ui/Card'
import styles from './ContentFeedCard.module.css'

const money = (cent: number) => `¥${(cent / 100).toFixed(2).replace(/\.00$/, '')}`

export default function ContentFeedCard({ item }: { item: ContentFeedItem }) {
  const image = item.images[0]
  const product = item.products[0]
  return <Card className={styles.card} data-testid={`feed-item-${item.id}`} interactive padded={false}>
    {image ? <Link aria-label={`查看 ${item.title}`} className={styles.images} to={`/posts/${item.id}`}>
      <img alt={item.title} className={styles.image} loading="lazy" src={image.image_url} />
      {item.images.length > 1 && <span className={styles.imageCount}><Images size={12} />{item.images.length}</span>}
      {item.tags[0] && <span className={styles.topic}>{item.tags[0].name}</span>}
    </Link> : <Link className={styles.textOnly} to={`/posts/${item.id}`}><p>{item.body}</p></Link>}
    <div className={styles.body}>
      <Link to={`/posts/${item.id}`}><h2 className={styles.title}>{item.title}</h2></Link>
      <p className={styles.copy}>{item.body}</p>
      {product && <Link className={styles.product} to={`/products/${product.id}`}><ShoppingBag size={13} /><span className={styles.productName}>{product.name}</span><span className={styles.price}>{money(product.min_price_cent)}</span><ArrowUpRight size={12} /></Link>}
      <div className={styles.author}><Link className={styles.authorLink} to={`/u/${item.author.id}`}><UserAvatar id={item.author.id} src={item.author.avatar_url} size={24} username={item.author.username} /><span className={styles.authorName}>{item.author.username}</span></Link><Link aria-label={`查看 ${item.likes_count} 个赞的笔记`} className={styles.metric} to={`/posts/${item.id}`}><Heart size={15} fill={item.is_liked ? 'currentColor' : 'none'} />{item.likes_count}</Link></div>
    </div>
  </Card>
}
