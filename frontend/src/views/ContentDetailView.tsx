import { useCallback, useEffect, useState } from 'react'
import { Heart, Send, Star, Store, Trash2, UserMinus, UserPlus } from 'lucide-react'
import { Link, useNavigate, useParams } from 'react-router'

import * as accountApi from '../api/account'
import * as contentApi from '../api/content'
import * as interactionApi from '../api/interaction'
import * as storefrontApi from '../api/storefront'
import type { Account, ContentPost, PostComment, PostInteractionState, PublicProductDetail } from '../api/types'
import AppShell from '../components/AppShell'
import Button from '../components/ui/Button'
import Card from '../components/ui/Card'
import PageHeader from '../components/ui/PageHeader'
import { ErrorState, Skeleton } from '../components/ui/StatePanel'
import UserAvatar from '../components/UserAvatar'
import { useAuthStore } from '../stores/auth'
import { useSocialStore } from '../stores/social'
import styles from './ContentDetailView.module.css'

const fallbackAsset = (label: string) => label.includes('包') ? '/assets/feed-bag.jpg' : label.includes('杯') ? '/assets/feed-cup.jpg' : '/assets/feed-coffee.jpg'

export default function ContentDetailView() {
  const { id = '0' } = useParams()
  const postId = Number(id)
  const navigate = useNavigate()
  const auth = useAuthStore()
  const social = useSocialStore()
  const [post, setPost] = useState<ContentPost | null>(null)
  const [author, setAuthor] = useState<Account | null>(null)
  const [products, setProducts] = useState<PublicProductDetail[]>([])
  const [comments, setComments] = useState<PostComment[]>([])
  const [interaction, setInteraction] = useState<PostInteractionState>({ is_liked: false, is_favorited: false })
  const [comment, setComment] = useState('')
  const [error, setError] = useState('')
  const [busy, setBusy] = useState('')

  const load = useCallback(async () => {
    setError('')
    try {
      const value = await contentApi.getPost(postId)
      setPost(value)
      const [account, commentPage, productValues] = await Promise.all([
        accountApi.findById(value.author_id), interactionApi.listComments(postId),
        Promise.all(value.product_ids.map((productId) => storefrontApi.getProduct(productId))),
      ])
      setAuthor(account)
      setComments(commentPage.comments)
      setProducts(productValues)
      if (useAuthStore.getState().isLoggedIn) setInteraction(await interactionApi.getState(postId))
    } catch (reason) { setError(reason instanceof Error ? reason.message : String(reason)) }
  }, [postId])

  useEffect(() => { void load() }, [load])

  async function toggle(type: 'like' | 'favorite') {
    if (!auth.isLoggedIn) { navigate('/account'); return }
    setBusy(type)
    try {
      if (type === 'like') {
        const result = await interactionApi.setLike(postId, !interaction.is_liked)
        setInteraction((value) => ({ ...value, is_liked: result.active }))
        setPost((value) => value ? { ...value, likes_count: result.count } : value)
      } else {
        const result = await interactionApi.setFavorite(postId, !interaction.is_favorited)
        setInteraction((value) => ({ ...value, is_favorited: result.active }))
        setPost((value) => value ? { ...value, favorites_count: result.count } : value)
      }
    } finally { setBusy('') }
  }

  async function submitComment() {
    if (!auth.isLoggedIn) { navigate('/account'); return }
    if (!comment.trim()) return
    setBusy('comment')
    try {
      const value = await interactionApi.createComment(postId, comment)
      setComments((items) => [value, ...items])
      setPost((item) => item ? { ...item, comments_count: item.comments_count + 1 } : item)
      setComment('')
    } finally { setBusy('') }
  }

  async function removeComment(id: number) {
    await interactionApi.deleteComment(id)
    setComments((items) => items.filter((item) => item.id !== id))
  }

  async function toggleFollow() {
    if (!author || !auth.isLoggedIn) { navigate('/account'); return }
    if (social.isFollowing(author.id)) await social.unfollow(author.id)
    else await social.follow(author.id)
  }

  if (error) return <AppShell><ErrorState action={<Button onClick={() => void load()}>重试</Button>} description={error} /></AppShell>
  if (!post) return <AppShell><div className="grid"><Skeleton height={360} /><Skeleton width="60%" height={28} /><Skeleton /></div></AppShell>

  return <AppShell>
    <PageHeader title={post.title} />
    <div className={styles.layout}>
      <article className={styles.article}>
        {post.images.length > 0 && <div className={[styles.gallery, post.images.length === 1 ? styles.single : ''].filter(Boolean).join(' ')}>{post.images.map((image) => <img alt="" key={image.id} onError={(event) => { event.currentTarget.onerror = null; event.currentTarget.src = fallbackAsset(post.title) }} src={image.image_url} />)}</div>}
        <div className={styles.meta}><Link className={styles.author} to={`/u/${post.author_id}`}><UserAvatar id={post.author_id} size={40} username={author?.username ?? ''} /><strong>@{author?.username ?? post.author_id}</strong></Link>{author && author.id !== auth.claims?.account_id && <Button icon={social.isFollowing(author.id) ? <UserMinus size={15} /> : <UserPlus size={15} />} onClick={() => void toggleFollow()} size="small">{social.isFollowing(author.id) ? '已关注' : '关注'}</Button>}</div>
        <p className={styles.copy}>{post.body}</p>
        <div className={styles.tags}>{post.tags.map((tag) => <span key={tag.id}>#{tag.name}</span>)}</div>
        <div className={styles.actions}><Button aria-label="点赞" icon={<Heart fill={interaction.is_liked ? 'currentColor' : 'none'} size={16} />} loading={busy === 'like'} onClick={() => void toggle('like')}>{post.likes_count}</Button><Button aria-label="收藏" icon={<Star fill={interaction.is_favorited ? 'currentColor' : 'none'} size={16} />} loading={busy === 'favorite'} onClick={() => void toggle('favorite')}>{post.favorites_count}</Button></div>
        <section className={styles.comments}><h2 className="title">评论 {post.comments_count}</h2><div className={styles.commentForm}><input aria-label="评论内容" maxLength={2000} onChange={(event) => setComment(event.currentTarget.value)} value={comment} /><Button icon={<Send size={16} />} loading={busy === 'comment'} onClick={() => void submitComment()} variant="primary">发送</Button></div><div className={styles.commentList}>{comments.map((item) => <div className={styles.comment} key={item.id}><div><p>{item.content}</p><span className="subtle">用户 #{item.author_id} · {new Date(item.created_at).toLocaleString()}</span></div>{item.author_id === auth.claims?.account_id && <Button aria-label="删除评论" icon={<Trash2 size={15} />} iconOnly onClick={() => void removeComment(item.id)} size="small" title="删除评论" variant="ghost" />}</div>)}</div></section>
      </article>
      <aside className={styles.sidebar}>{products.length > 0 && <h2 className="title">关联商品</h2>}{products.map((value) => <Card key={value.product.id}><Link className={styles.product} to={`/products/${value.product.id}`}><img alt="" loading="lazy" onError={(event) => { event.currentTarget.onerror = null; event.currentTarget.src = fallbackAsset(value.product.name) }} src={value.product.cover_url} /><div><h3 className={styles.productName}>{value.product.name}</h3><div className={styles.price}>{value.min_price_cent ? `¥${(value.min_price_cent / 100).toFixed(2)}` : '暂不可售'}</div></div></Link>{value.shop && <Link className="subtle" to={`/shops/${value.shop.shop.id}`}><Store size={13} /> {value.shop.shop.name}</Link>}</Card>)}</aside>
    </div>
  </AppShell>
}
