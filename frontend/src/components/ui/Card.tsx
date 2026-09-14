import type { HTMLAttributes, PropsWithChildren } from 'react'

import styles from './Card.module.css'

type Props = PropsWithChildren<HTMLAttributes<HTMLDivElement> & { padded?: boolean; interactive?: boolean }>

export default function Card({ padded = true, interactive = false, className = '', children, ...props }: Props) {
  const classes = [styles.card, padded ? styles.padded : '', interactive ? styles.interactive : '', className].filter(Boolean).join(' ')
  return <div {...props} className={classes}>{children}</div>
}
