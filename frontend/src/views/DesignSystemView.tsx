import { Download, Plus, RefreshCw, Trash2 } from 'lucide-react'

import AppShell from '../components/AppShell'
import Button from '../components/ui/Button'
import Card from '../components/ui/Card'
import PageHeader from '../components/ui/PageHeader'
import { EmptyState, ErrorState, Skeleton } from '../components/ui/StatePanel'

export default function DesignSystemView() {
  return (
    <AppShell>
      <PageHeader title="界面组件" actions={<Button icon={<Plus size={16} />} variant="primary">新建</Button>} />
      <div className="grid three">
        <Card><h2 className="title">按钮</h2><div className="row"><Button icon={<Download size={16} />} variant="primary">下载</Button><Button icon={<RefreshCw size={16} />}>刷新</Button><Button aria-label="删除" icon={<Trash2 size={16} />} iconOnly title="删除" variant="danger" /></div></Card>
        <Card><h2 className="title">骨架</h2><div className="grid"><Skeleton height={96} /><Skeleton width="70%" /><Skeleton width="45%" /></div></Card>
        <Card><h2 className="title">表单</h2><label htmlFor="kit-input">名称</label><input id="kit-input" defaultValue="内容集市" /></Card>
      </div>
      <div className="grid two" style={{ marginTop: 16 }}>
        <EmptyState action={<Button size="small" variant="primary">新建内容</Button>} />
        <ErrorState action={<Button icon={<RefreshCw size={15} />} size="small">重试</Button>} />
      </div>
    </AppShell>
  )
}
