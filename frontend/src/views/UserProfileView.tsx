import { useEffect, useState } from 'react'
import { useParams } from 'react-router'

import * as accountApi from '../api/account'
import type { Account } from '../api/types'
import AppShell from '../components/AppShell'
import UserAvatar from '../components/UserAvatar'

export default function UserProfileView() {
  const { id = '0' } = useParams()
  const [account, setAccount] = useState<Account | null>(null)
  useEffect(() => { void accountApi.findById(Number(id)).then(setAccount) }, [id])
  return (
    <AppShell>
      <section className="card">
        {account ? <div className="row"><UserAvatar id={account.id} username={account.username} size={64} /><div><h1 className="title">@{account.username}</h1><p className="subtle">{account.bio || `账号 #${account.id}`}</p></div></div> : <p className="subtle">加载中</p>}
      </section>
    </AppShell>
  )
}
