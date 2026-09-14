import { LoaderCircle } from 'lucide-react'
import type { ButtonHTMLAttributes, ReactNode } from 'react'

import styles from './Button.module.css'

type Props = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: 'primary' | 'secondary' | 'ghost' | 'danger'
  size?: 'small' | 'medium'
  icon?: ReactNode
  loading?: boolean
  iconOnly?: boolean
}

export default function Button({ variant = 'secondary', size = 'medium', icon, loading = false, iconOnly = false, className = '', children, disabled, ...props }: Props) {
  const classes = [styles.button, styles[variant], size === 'small' ? styles.small : '', iconOnly ? styles.iconOnly : '', className].filter(Boolean).join(' ')
  return (
    <button {...props} aria-busy={loading} className={classes} disabled={disabled || loading}>
      {loading ? <LoaderCircle aria-hidden className={styles.spinner} size={16} /> : icon}
      {!iconOnly && children}
    </button>
  )
}
