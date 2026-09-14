import AppShell from '../components/AppShell'

export default function WorkspaceView({ title }: { title: string }) {
  return <AppShell><section className="card"><h1 className="title">{title}</h1><p className="subtle">暂无数据</p></section></AppShell>
}
