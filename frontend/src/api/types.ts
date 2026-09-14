export type MessageResponse = { message: string }

export type TokenResponse = {
  token: string
  refresh_token?: string
  account_id?: number
  username?: string
}

export type Account = {
  id: number
  username: string
  avatar_url?: string
  bio?: string
}

export type GetAllFollowersResponse = { followers: Account[]; follower_count?: number }
export type GetAllVloggersResponse = { vloggers: Account[]; vlogger_count?: number }

export type DirectMessage = {
  id: number
  from_id: number
  to_id: number
  content: string
  is_read: boolean
  created_at: string
}

export type ListMessagesResponse = { messages: DirectMessage[] }

export type PostImage = {
  id: number
  image_url: string
  mime_type: string
  byte_size: number
  width: number
  height: number
  sort_order?: number | null
  created_at: string
}

export type PostTag = { id: number; name: string }

export type ContentPost = {
  id: number
  author_id: number
  title: string
  body: string
  status: 'published'
  likes_count: number
  favorites_count: number
  comments_count: number
  popularity: number
  published_at: string
  created_at: string
  updated_at: string
  images: PostImage[]
  tags: PostTag[]
  product_ids: number[]
}

export type InteractionStateResult = { active: boolean; count: number }
export type PostInteractionState = { is_liked: boolean; is_favorited: boolean }
export type PostComment = {
  id: number
  post_id: number
  author_id: number
  content: string
  status: 'active'
  created_at: string
  updated_at: string
  mentioned_usernames: string[]
}
export type PostCommentPage = { comments: PostComment[]; next_before_time?: string; next_before_id?: number; has_more: boolean }

export type FeedAuthor = {
  id: number
  username: string
  avatar_url?: string
  bio?: string
}

export type FeedShopSummary = { id: number; name: string; avatar_url?: string }

export type FeedProductCard = {
  id: number
  name: string
  cover_url: string
  min_price_cent: number
}

export type ContentFeedItem = {
  id: number
  author: FeedAuthor
  title: string
  body: string
  images: PostImage[]
  tags: PostTag[]
  likes_count: number
  favorites_count: number
  comments_count: number
  popularity: number
  published_at: string
  is_liked: boolean
  is_favorited: boolean
  shop?: FeedShopSummary
  products: FeedProductCard[]
}

export type PostCursor = { published_at: string; id: number }
export type PopularityCursor = { popularity: number; published_at: string; id: number }

export type ContentFeedPage = {
  items: ContentFeedItem[]
  next_cursor?: PostCursor
  has_more: boolean
}

export type PopularContentFeedPage = {
  items: ContentFeedItem[]
  snapshot_at?: string
  next_offset: number
  next_fallback_cursor?: PopularityCursor
  has_more: boolean
  degraded: boolean
}

export type ShopStatus = 'open' | 'closed' | 'disabled'
export type ProductStatus = 'draft' | 'on_sale' | 'off_sale' | 'disabled'
export type SKUStatus = 'active' | 'inactive' | 'disabled'
export type SKUSpecs = Record<string, string>

export type Shop = {
  id: number
  owner_id: number
  name: string
  description: string
  avatar_url: string
  banner_url: string
  status: ShopStatus
  created_at: string
  updated_at: string
}

export type PublicShop = { shop: Shop; product_count: number; accepting_orders: boolean }

export type Product = {
  id: number
  shop_id: number
  category_id?: number
  name: string
  subtitle: string
  description: string
  cover_url: string
  status: ProductStatus
  sales_count: number
  created_at: string
  updated_at: string
}

export type ProductImage = {
  id: number
  product_id: number
  image_url: string
  mime_type: string
  byte_size: number
  width: number
  height: number
  sort_order: number
  created_at: string
}

export type PublicSKU = {
  id: number
  sku_code: string
  specs: SKUSpecs
  price_cent: number
  original_price_cent?: number
  available_stock: number
  status: Exclude<SKUStatus, 'disabled'>
  can_purchase: boolean
}

export type ProductSKU = PublicSKU & {
  product_id: number
  locked_stock: number
  sold_stock: number
  version: number
  status: SKUStatus
  created_at: string
  updated_at: string
}

export type PublicProductItem = { product: Product; min_price_cent?: number; can_purchase: boolean }
export type ProductCursor = { created_at: string; id: number }
export type PublicProductPage = { products: PublicProductItem[]; next_cursor?: ProductCursor; has_more: boolean }

export type PublicProductDetail = {
  product: Product
  shop: PublicShop
  images: ProductImage[]
  skus: PublicSKU[]
  min_price_cent?: number
  can_purchase: boolean
  related_posts_url: string
}
export type RelatedPostPage = { posts: ContentPost[]; next_cursor?: PostCursor; has_more: boolean }

export type CartItem = {
  id: number
  account_id: number
  sku_id: number
  quantity: number
  observed_price_cent: number
  created_at: string
  updated_at: string
}

export type Address = {
  id: number
  account_id: number
  recipient_name: string
  phone: string
  province: string
  city: string
  district: string
  detail: string
  postal_code: string
  is_default: boolean
  created_at: string
  updated_at: string
}

export type CheckoutItem = {
  cart_item_id: number
  shop_id: number
  product_id: number
  product_name: string
  cover_url: string
  sku_id: number
  sku_code: string
  specs: SKUSpecs
  quantity: number
  observed_price_cent: number
  current_price_cent: number
  subtotal_cent: number
  price_changed: boolean
  can_checkout: boolean
  issue?: string
}

export type CheckoutShopGroup = { shop_id: number; shop_name: string; items: CheckoutItem[]; subtotal_cent: number }
export type CheckoutPreview = {
  address: Address
  shops: CheckoutShopGroup[]
  total_cent: number
  has_price_changes: boolean
  has_unavailable_items: boolean
  can_submit: boolean
}

export type OrderStatus = 'pending_payment' | 'paid' | 'shipped' | 'completed' | 'canceled'
export type OrderItem = {
  id: number
  order_id: number
  product_id: number
  product_name: string
  cover_url: string
  sku_id: number
  sku_code: string
  specs: SKUSpecs
  unit_price_cent: number
  quantity: number
  subtotal_cent: number
  created_at: string
}

export type OrderAddressSnapshot = Omit<Address, 'account_id' | 'is_default' | 'created_at' | 'updated_at' | 'id'> & { address_id: number }
export type Order = {
  id: number
  submission_id: number
  order_no: string
  buyer_id: number
  shop_id: number
  status: OrderStatus
  total_cent: number
  address_snapshot: OrderAddressSnapshot
  expire_at: string
  paid_at?: string
  shipped_at?: string
  completed_at?: string
  canceled_at?: string
  created_at: string
  updated_at: string
  items?: OrderItem[]
}

export type OrderSubmission = { id: number; buyer_id: number; grand_total_cent: number; created_at: string; updated_at: string }
export type OrderGroup = { submission: OrderSubmission; orders: Order[] }
export type OrderPage = { orders: Order[]; next_cursor?: { created_at: string; id: number }; has_more: boolean }

export type PaymentRecord = {
  id: number
  payment_no: string
  order_id: number
  buyer_id: number
  provider: 'mock'
  status: 'pending' | 'succeeded' | 'failed' | 'closed'
  amount_cent: number
  session_token: string
  cashier_url: string
  created_at: string
  updated_at: string
}
export type PaymentSession = { payment: PaymentRecord }

export type FlashSaleResultCode = 'accepted' | 'order_created' | 'paid' | 'canceled' | 'failed' | 'not_preheated' | 'disabled' | 'not_started' | 'ended' | 'sold_out' | 'limit_exceeded'
export type FlashSaleReservation = {
  code: FlashSaleResultCode
  request_id?: string
  quantity?: number
  remaining_stock?: number
  duplicate?: boolean
  order_id?: number
  order_no?: string
  payment_expires_at?: string
}

export type SellerDashboard = {
  shop_id: number
  product_count: number
  sku_count: number
  available_stock: number
  locked_stock: number
  sold_stock: number
  pending_shipment_orders: number
  completed_orders: number
}
export type SellerProductPage = { products: Product[] }
export type SellerProductDetail = { product: Product; images: ProductImage[]; skus: ProductSKU[] }

export type SearchScope = 'content' | 'product' | 'shop'
export type SearchHit = {
  type: SearchScope
  id: number
  title: string
  subtitle?: string
  image_url?: string
  price_cent?: number
  published_at?: string
}
export type SearchPage = { query: string; scope: SearchScope; items: SearchHit[]; has_more: boolean; next_offset?: number }
