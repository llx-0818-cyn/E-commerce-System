import { create } from 'zustand'

export type ToastType = 'success' | 'error' | 'info'

export type Toast = {
  id: number
  type: ToastType
  message: string
}

type ToastStore = {
  toasts: Toast[]
  push: (type: ToastType, message: string, ttlMs?: number) => void
  remove: (id: number) => void
  success: (message: string) => void
  error: (message: string) => void
  info: (message: string) => void
}

let nextId = 1

export const useToastStore = create<ToastStore>()((set, get) => ({
  toasts: [],

  remove(id) {
    set((state) => ({ toasts: state.toasts.filter((toast) => toast.id !== id) }))
  },

  push(type, message, ttlMs = 2600) {
    const id = nextId++
    set((state) => ({ toasts: [...state.toasts, { id, type, message }] }))
    window.setTimeout(() => get().remove(id), ttlMs)
  },

  success(message) {
    get().push('success', message)
  },

  error(message) {
    get().push('error', message, 3600)
  },

  info(message) {
    get().push('info', message)
  },
}))
