import { useEffect, useState } from 'react'
import { Link, useNavigate, useParams } from 'react-router'
import { ArrowUpRight, UserPlus } from 'lucide-react'
import { getJson } from '../api/client'
import type { Account, ContentPost } from '../api/types'
import AppShell from '../components/AppShell'
import ContentFeedCard from '../components/ContentFeedCard'
import UserAvatar from '../components/UserAvatar'
import Button from '../components/ui/Button'
import { EmptyState, ErrorState, Skeleton } from '../components/ui/StatePanel'
import { useAuthStore } from '../stores/auth'
import { useSocialStore } from '../stores/social'
import feedStyles from './HomeView.module.css'
import styles from './UserProfileView.module.css'

type Profile = { author: Account; content_count: number; received_likes_count: number; is_following: boolean; posts: ContentPost[] }
export default function UserProfileView() {
  const { id = '0' } = useParams()
  const [profile, setProfile] = useState<Profile | null>(null)
  const [error, setError] = useState('')
  const [busy, setBusy] = useState(false)
  const auth = useAuthStore()
  const social = useSocialStore()
  const navigate = useNavigate()
  useEffect(() => {
    let active = true
    setError(''); setProfile(null)
    void getJson<Profile>(`/api/authors/${id}/profile`).then((value) => { if (active) setProfile(value) }).catch((reason: unknown) => { if (active) setError(reason instanceof Error ? reason.message : '主页加载失败') })
    return () => { active = false }
  }, [id])
  async function follow() {
    if (!auth.isLoggedIn) { navigate(`/account?redirect=${encodeURIComponent(`/u/${id}`)}`); return }
    setBusy(true)
    try { if (social.isFollowing(Number(id))) await social.unfollow(Number(id)); else await social.follow(Number(id)) }
    finally { setBusy(false) }
  }
  return <AppShell>{error ? <ErrorState description={error}/> : !profile ? <Skeleton height={300}/> : <><section className={styles.profile}><span className={styles.kicker}>A LIFE WORTH SHARING</span><div className={styles.identity}><UserAvatar id={profile.author.id} src={profile.author.avatar_url} username={profile.author.username} size={90}/><div><h1>{profile.author.username}</h1><p>{profile.author.bio}</p><div className={styles.stats}><span><strong>{profile.content_count}</strong> 篇笔记</span><span><strong>{profile.received_likes_count}</strong> 次喜欢</span></div></div>{auth.claims?.account_id === profile.author.id ? <Link className={styles.edit} to="/settings">编辑资料 <ArrowUpRight size={13}/></Link> : <Button icon={<UserPlus size={14}/>} onClick={() => void follow()} loading={busy} variant="primary">{social.isFollowing(Number(id)) ? '已关注' : '关注'}</Button>}</div></section><div className={styles.section}><h2>生活笔记 <span>{profile.posts.length}</span></h2><span>把日常过成自己的样子</span></div>{!profile.posts.length ? <EmptyState title="这里的故事，正在慢慢开始"/> : <div className={feedStyles.feed}>{profile.posts.map((post) => <ContentFeedCard key={post.id} item={{...post,author:profile.author,is_liked:false,is_favorited:false,products:[]}}/>)}</div>}</>}</AppShell>
}
