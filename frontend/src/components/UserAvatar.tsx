import { useMemo, useState, type CSSProperties } from 'react'

import styles from './UserAvatar.module.css'
import { scopedClasses } from '../utils/scoped-classes'
import { useCatalog } from '../hooks/useCatalog'

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
  const catalog = useCatalog()
  const [failedSource, setFailedSource] = useState('')
  const source = src || catalog.people.find((person) => person.id === id)?.avatar_url
  const initial = useMemo(() => {
    const value = (username ?? '').trim()
    return value ? value.slice(0, 1).toUpperCase() : '?'
  }, [username])

  const backgroundImage = useMemo(() => {
    const seed = typeof id === 'number' ? String(id) : username
    const hue = 5 + hashToHue(seed || '0') % 35
    return `linear-gradient(135deg, hsl(${hue} 45% 73%), hsl(${hue} 30% 50%))`
  }, [id, username])

  const avatarStyle: CSSProperties = { width: `${size}px`, height: `${size}px` }
  const className = scopedClasses(styles, 'avatar')

  if (source && failedSource !== source) return <img src={source} className={className} style={avatarStyle} alt="" loading="lazy" onError={() => setFailedSource(source)} />

  return (
    <div className={className} style={{ ...avatarStyle, backgroundImage }} aria-hidden="true">
      {initial}
    </div>
  )
}
