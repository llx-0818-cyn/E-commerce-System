import { useMemo, type CSSProperties } from 'react'

import styles from './UserAvatar.module.css'
import { scopedClasses } from '../utils/scoped-classes'

type UserAvatarProps = {
  username: string
  id?: number
  size?: number
  src?: string
}

function hashToHue(input: string) {
  let hash = 0
  for (let index = 0; index < input.length; index += 1) {
    hash = (hash * 31 + input.charCodeAt(index)) >>> 0
  }
  return hash % 360
}

export default function UserAvatar({ username, id, size = 40, src }: UserAvatarProps) {
  const initial = useMemo(() => {
    const value = (username ?? '').trim()
    return value ? value.slice(0, 1).toUpperCase() : '?'
  }, [username])

  const backgroundImage = useMemo(() => {
    const seed = typeof id === 'number' ? String(id) : username
    const hue = hashToHue(seed || '0')
    return `linear-gradient(135deg, hsl(${hue} 90% 55%), hsl(${(hue + 40) % 360} 90% 55%))`
  }, [id, username])

  const avatarStyle: CSSProperties = { width: `${size}px`, height: `${size}px` }
  const className = scopedClasses(styles, 'avatar')

  if (src) return <img src={src} className={className} style={avatarStyle} alt="" />

  return (
    <div className={className} style={{ ...avatarStyle, backgroundImage }} aria-hidden="true">
      {initial}
    </div>
  )
}
