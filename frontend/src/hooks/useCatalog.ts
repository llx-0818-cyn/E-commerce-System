import { useEffect, useState } from 'react'

export type SourceReview = { id: number; author_id: number; author: string; avatar_url: string; text: string; rating: number; date: string; source_url: string }
export type CatalogProduct = { id: number; code: string; sku_id: number; name: string; subtitle: string; images: {url: string}[]; category: string; rating: number; review_count: number; source_url: string; review_source_url: string; price_note: string; source_currency: string; source_price: number; reviews: SourceReview[] }
export type CatalogPerson = { id: number; username: string; avatar_url: string }
export type Catalog = { products: CatalogProduct[]; people: CatalogPerson[]; collected_at?: string }
const empty: Catalog = { products: [], people: [] }
let pending: Promise<Catalog> | undefined

export function useCatalog() {
  const [catalog, setCatalog] = useState<Catalog>(empty)
  useEffect(() => {
    let active = true
    pending ??= fetch('/catalog/provenance.json').then((response) => response.ok ? response.json() as Promise<Catalog> : empty).catch(() => empty)
    void pending.then((value) => { if (active) setCatalog(value) })
    return () => { active = false }
  }, [])
  return catalog
}
