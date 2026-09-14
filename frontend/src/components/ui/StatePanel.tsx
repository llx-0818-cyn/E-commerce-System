import { CircleAlert, Inbox } from 'lucide-react'
import type { CSSProperties, ReactNode } from 'react'

import styles from './StatePanel.module.css'

export function Skeleton({ width = '100%', height = 16 }: { width?: CSSProperties['width']; height?: CSSProperties['height'] }) {
  return <div aria-hidden className={styles.skeleton} style={{ width, height }} />
}

export function EmptyState({ title = '暂无数据', description, action }: { title?: string; description?: string; action?: ReactNode }) {
  return <div className={styles.panel}><div><Inbox aria-hidden className={styles.icon} size={28} /><div className={styles.title}>{title}</div>{description && <p className={styles.text}>{description}</p>}{action}</div></div>
}

export function ErrorState({ title = '加载失败', description, action }: { title?: string; description?: string; action?: ReactNode }) {
  return <div className={styles.panel} role="alert"><div><CircleAlert aria-hidden className={styles.danger} size={28} /><div className={styles.title}>{title}</div>{description && <p className={styles.text}>{description}</p>}{action}</div></div>
}
