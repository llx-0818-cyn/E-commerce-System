import { create } from 'zustand'

import { ApiError } from '../api/client'
import * as socialApi from '../api/social'
import type { Account } from '../api/types'
import { useAuthStore } from './auth'

type SocialStore = {
  followers: Account[]
  vloggers: Account[]
  followersLoading: boolean
  vloggersLoading: boolean
  followersError: string
  vloggersError: string
  followerCount: number
  followingCount: number
  clear: () => void
  isFollowing: (accountId: number) => boolean
  refreshMine: () => Promise<void>
  refreshFollowers: (vloggerId?: number) => Promise<void>
  refreshVloggers: (followerId?: number) => Promise<void>
  follow: (vloggerId: number) => Promise<void>
  unfollow: (vloggerId: number) => Promise<void>
}

export const useSocialStore = create<SocialStore>()((set, get) => ({
  followers: [],
  vloggers: [],
  followersLoading: false,
  vloggersLoading: false,
  followersError: '',
  vloggersError: '',
  followerCount: 0,
  followingCount: 0,

  clear() {
    set({
      followers: [],
      vloggers: [],
      followersLoading: false,
      vloggersLoading: false,
      followersError: '',
      vloggersError: '',
      followerCount: 0,
      followingCount: 0,
    })
  },

  isFollowing(accountId) {
    return get().vloggers.some((account) => account.id === accountId)
  },

  async refreshFollowers(vloggerId) {
    if (!useAuthStore.getState().isLoggedIn) {
      get().clear()
      return
    }

    set({ followersLoading: true, followersError: '' })
    try {
      const res = await socialApi.getAllFollowers(vloggerId)
      set({ followers: res.followers, followerCount: res.followers.length })
    } catch (error) {
      set({
        followersError: error instanceof ApiError ? error.message : String(error),
        followers: [],
        followerCount: 0,
      })
    } finally {
      set({ followersLoading: false })
    }
  },

  async refreshVloggers(followerId) {
    if (!useAuthStore.getState().isLoggedIn) {
      get().clear()
      return
    }

    set({ vloggersLoading: true, vloggersError: '' })
    try {
      const res = await socialApi.getAllVloggers(followerId)
      set({ vloggers: res.vloggers, followingCount: res.vloggers.length })
    } catch (error) {
      set({
        vloggersError: error instanceof ApiError ? error.message : String(error),
        vloggers: [],
        followingCount: 0,
      })
    } finally {
      set({ vloggersLoading: false })
    }
  },

  async refreshMine() {
    await Promise.all([get().refreshFollowers(), get().refreshVloggers()])
  },

  async follow(vloggerId) {
    if (!useAuthStore.getState().isLoggedIn) throw new ApiError('需要先登录', 401)
    await socialApi.follow(vloggerId)
    await get().refreshVloggers()
  },

  async unfollow(vloggerId) {
    if (!useAuthStore.getState().isLoggedIn) throw new ApiError('需要先登录', 401)
    await socialApi.unfollow(vloggerId)
    await get().refreshVloggers()
  },
}))
