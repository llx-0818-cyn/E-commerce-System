import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { useLocation, useNavigate, useParams } from 'react-router'

import * as accountApi from '../api/account'
import { ApiError } from '../api/client'
import * as messageApi from '../api/message'
import type { Account, DirectMessage } from '../api/types'
import AppShell from '../components/AppShell'
import UserAvatar from '../components/UserAvatar'
import { useAuthStore } from '../stores/auth'
import { useSocialStore } from '../stores/social'
import { useToastStore } from '../stores/toast'
import { scopedClasses } from '../utils/scoped-classes'
import styles from './MessageView.module.css'

type ChatState = { loading: boolean; sending: boolean; error: string; peer: Account | null; messages: DirectMessage[] }

function formatTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '' : date.toLocaleString([], { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
}

export default function MessageView() {
  const params = useParams()
  const location = useLocation()
  const navigate = useNavigate()
  const auth = useAuthStore()
  const social = useSocialStore()
  const toast = useToastStore()
  const peerId = typeof params.peerId === 'string' ? Number(params.peerId) : 0
  const myId = auth.claims?.account_id ?? 0
  const hasPeer = Number.isFinite(peerId) && peerId > 0
  const listElement = useRef<HTMLDivElement | null>(null)
  const [content, setContent] = useState('')
  const [state, setState] = useState<ChatState>({ loading: false, sending: false, error: '', peer: null, messages: [] })
  const css = (...values: Array<string | false | null | undefined>) => scopedClasses(styles, ...values)
  const orderedMessages = useMemo(() => [...state.messages].sort((a, b) => new Date(a.created_at).getTime() - new Date(b.created_at).getTime()), [state.messages])
  const canSend = content.trim().length > 0 && !state.sending && !!state.peer && peerId > 0
  const contactItems = useMemo(() => {
    const contacts = new Map<number, Account>()
    for (const user of social.vloggers) contacts.set(user.id, user)
    for (const user of social.followers) contacts.set(user.id, user)
    return [...contacts.values()].filter((user) => user.id !== myId)
  }, [myId, social.followers, social.vloggers])
  const contactLoading = social.vloggersLoading || social.followersLoading
  const contactError = social.vloggersError || social.followersError

  const loadChat = useCallback(async () => {
    if (!useAuthStore.getState().isLoggedIn) {
      navigate(`/account?redirect=${encodeURIComponent(location.pathname + location.search + location.hash)}`)
      return
    }
    if (!hasPeer) {
      await useSocialStore.getState().refreshMine()
      setState({ loading: false, sending: false, error: '', peer: null, messages: [] })
      return
    }
    if (!Number.isFinite(peerId) || peerId <= 0) {
      setState((current) => ({ ...current, error: '无效的用户 id' }))
      return
    }
    if (peerId === (useAuthStore.getState().claims?.account_id ?? 0)) {
      setState((current) => ({ ...current, error: '请选择其他用户发送私信' }))
      return
    }

    setState((current) => ({ ...current, loading: true, error: '' }))
    try {
      const [peer, result] = await Promise.all([accountApi.findById(peerId), messageApi.listMessages(peerId)])
      setState((current) => ({ ...current, peer, messages: result.messages ?? [], loading: false }))
    } catch (error) {
      setState((current) => ({ ...current, error: error instanceof ApiError ? error.message : String(error), peer: null, messages: [], loading: false }))
    }
  }, [hasPeer, location.hash, location.pathname, location.search, navigate, peerId])

  useEffect(() => {
    setState((current) => ({ ...current, peer: null, messages: [] }))
    setContent('')
    void loadChat()
  }, [loadChat])

  useEffect(() => {
    const frame = requestAnimationFrame(() => {
      if (listElement.current) listElement.current.scrollTop = listElement.current.scrollHeight
    })
    return () => cancelAnimationFrame(frame)
  }, [state.messages])

  async function send() {
    const text = content.trim()
    if (!text || state.sending || !state.peer) return
    setState((current) => ({ ...current, sending: true }))
    try {
      const message = await messageApi.sendMessage(peerId, text)
      setState((current) => ({ ...current, messages: [message, ...current.messages] }))
      setContent('')
    } catch (error) {
      toast.error(error instanceof ApiError ? error.message : String(error))
    } finally {
      setState((current) => ({ ...current, sending: false }))
    }
  }

  return (
    <AppShell>
      <section className={css('chat-shell')}>
        {!hasPeer ? (
          <div className={css('contact-panel')}>
            <div className={css('contact-head')}>
              <div><p className={css('title')}>私信</p><p className={css('subtle')}>选择关注或粉丝里的用户开始聊天。</p></div>
              <button className={css('ghost small')} type="button" disabled={contactLoading} onClick={() => void social.refreshMine()}>刷新</button>
            </div>
            <div className={css('contact-body')}>
              {contactLoading ? <div className={css('chat-hint')}>加载中…</div>
                : contactError ? <div className={css('chat-hint bad')}>{contactError}</div>
                  : contactItems.length === 0 && <div className={css('empty')}><div className={css('empty-title')}>暂无可聊天用户</div><div className={css('empty-text')}>关注别人或拥有粉丝后，可以从这里开始私信。</div></div>}
              {contactItems.map((user) => (
                <button key={user.id} className={css('contact-row')} type="button" onClick={() => navigate(`/messages/${user.id}`)}>
                  <UserAvatar username={user.username} id={user.id} size={42} />
                  <span className={css('contact-meta')}><span className={css('contact-name')}>@{user.username}</span><span className={css('contact-id mono')}>#{user.id}</span></span>
                  <span className={css('contact-action')}>聊天</span>
                </button>
              ))}
            </div>
          </div>
        ) : (
          <div className={css('chat-panel')}>
            <header className={css('chat-head')}>
              <button className={css('icon-btn')} type="button" title="返回" onClick={() => navigate(-1)}>‹</button>
              <button className={css('peer')} type="button" disabled={!state.peer} onClick={() => { if (state.peer) navigate(`/u/${state.peer.id}`) }}>
                <UserAvatar username={state.peer?.username ?? 'User'} id={state.peer?.id ?? peerId} size={44} />
                <span className={css('peer-meta')}><span className={css('peer-name')}>@{state.peer?.username ?? '加载中'}</span><span className={css('peer-id mono')}>#{state.peer?.id ?? peerId}</span></span>
              </button>
              <button className={css('ghost small')} type="button" disabled={state.loading} onClick={() => void loadChat()}>刷新</button>
            </header>

            <div ref={listElement} className={css('message-list')}>
              {state.loading ? <div className={css('chat-hint')}>加载中…</div>
                : state.error ? <div className={css('chat-hint bad')}>{state.error}</div>
                  : orderedMessages.length === 0 && <div className={css('empty')}><div className={css('empty-title')}>开始聊天</div><div className={css('empty-text')}>给 @{state.peer?.username ?? '对方'} 发第一条消息。</div></div>}
              {orderedMessages.map((message) => {
                const mine = message.from_id === myId
                return (
                  <div key={message.id} className={css('bubble-row', mine && 'mine')}>
                    {!mine && <UserAvatar username={state.peer?.username ?? 'User'} id={state.peer?.id ?? message.from_id} size={32} />}
                    <div className={css('bubble-wrap')}><div className={css('bubble')}>{message.content}</div><div className={css('bubble-time')}>{formatTime(message.created_at)}</div></div>
                  </div>
                )
              })}
            </div>

            <footer className={css('composer')}>
              <textarea
                value={content}
                placeholder="输入私信内容"
                disabled={!!state.error || state.loading || state.sending}
                onChange={(event) => setContent(event.currentTarget.value)}
                onKeyDown={(event) => {
                  if (event.key === 'Enter' && !event.altKey && !event.ctrlKey && !event.metaKey && !event.shiftKey) {
                    event.preventDefault()
                    void send()
                  }
                }}
              />
              <button className={css('primary send-btn')} type="button" disabled={!canSend} onClick={() => void send()}>{state.sending ? '发送中' : '发送'}</button>
            </footer>
          </div>
        )}
      </section>
    </AppShell>
  )
}
