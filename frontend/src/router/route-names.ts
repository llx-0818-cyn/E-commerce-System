import { matchPath } from 'react-router'

const ROUTES = [
  { path: '/', name: '推荐' },
  { path: '/hot', name: '热门' },
  { path: '/search', name: '搜索' },
  { path: '/posts/new', name: '发布内容' },
  { path: '/posts/:id', name: '内容详情' },
  { path: '/shops/:id', name: '店铺' },
  { path: '/products/:id', name: '商品' },
  { path: '/cart', name: '购物车' },
  { path: '/checkout', name: '结算' },
  { path: '/orders', name: '订单' },
  { path: '/orders/:id', name: '订单详情' },
  { path: '/seller', name: '卖家中心' },
  { path: '/flash-sales/:id', name: '限时抢购' },
  { path: '/ui-kit', name: '界面组件' },
  { path: '/account', name: '账号' },
  { path: '/account/register', name: '注册' },
  { path: '/account/change-password', name: '修改密码' },
  { path: '/settings', name: '设置' },
  { path: '/u/:id', name: '用户主页' },
  { path: '/messages', name: '私信' },
  { path: '/messages/:peerId', name: '会话' },
]

export function getRouteName(pathname: string) {
  return ROUTES.find((route) => matchPath({ path: route.path, end: true }, pathname))?.name ?? ''
}
