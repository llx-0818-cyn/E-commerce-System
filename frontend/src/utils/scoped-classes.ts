type ClassValue = string | false | null | undefined

export function scopedClasses(styles: Readonly<Record<string, string>>, ...values: ClassValue[]) {
  const tokens = values
    .filter((value): value is string => typeof value === 'string' && value.length > 0)
    .flatMap((value) => value.split(/\s+/).filter(Boolean))

  return tokens.flatMap((token) => (styles[token] ? [token, styles[token]] : [token])).join(' ')
}
