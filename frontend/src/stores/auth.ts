import { create } from 'zustand'

import { decodeJwtPayload, type JwtPayload } from '../utils/jwt'

const ACCESS_KEY = 'access_token'
const REFRESH_KEY = 'refresh_token'

function readStored(key: string): string | null {
  try { return localStorage.getItem(key) } catch { return null }
}

function writeStored(key: string, value: string) {
  try { localStorage.setItem(key, value) } catch { return }
}

function removeStored(key: string) {
  try { localStorage.removeItem(key) } catch { return }
}

type AuthStore = {
  token: string | null
  refreshToken: string | null
  isLoggedIn: boolean
  claims: JwtPayload | null
  setToken: (newToken: string) => void
  setTokens: (access: string, refresh: string) => void
  clearTokens: () => void
}

const initialToken = readStored(ACCESS_KEY)

export const useAuthStore = create<AuthStore>()((set) => ({
  token: initialToken,
  refreshToken: readStored(REFRESH_KEY),
  isLoggedIn: !!initialToken,
  claims: initialToken ? decodeJwtPayload(initialToken) : null,

  setToken(newToken) {
    writeStored(ACCESS_KEY, newToken)
    set({ token: newToken, isLoggedIn: true, claims: decodeJwtPayload(newToken) })
  },

  setTokens(access, refresh) {
    writeStored(ACCESS_KEY, access)
    writeStored(REFRESH_KEY, refresh)
    set({
      token: access,
      refreshToken: refresh,
      isLoggedIn: true,
      claims: decodeJwtPayload(access),
    })
  },

  clearTokens() {
    removeStored(ACCESS_KEY)
    removeStored(REFRESH_KEY)
    set({ token: null, refreshToken: null, isLoggedIn: false, claims: null })
  },
}))
