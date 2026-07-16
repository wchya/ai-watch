export type View = 'dashboard' | 'providers' | 'scenarios' | 'comparisons' | 'schedules' | 'failover' | 'maintenance' | 'reliability' | 'slos' | 'incidents' | 'events' | 'settings' | 'notification-routing' | 'diagnostics'
export type NavIcon = 'dashboard' | 'providers' | 'validation' | 'automation' | 'stability' | 'events' | 'settings'

export const routes: Record<View, { path: string; title: string }> = {
  dashboard: { path: '/', title: '总览' },
  providers: { path: '/providers', title: 'Provider' },
  scenarios: { path: '/validation/scenarios', title: '测试场景' },
  comparisons: { path: '/validation/comparisons', title: '对比历史' },
  schedules: { path: '/automation/schedules', title: '计划任务' },
  failover: { path: '/automation/failover', title: '故障切换' },
  maintenance: { path: '/automation/maintenance', title: '维护窗口' },
  reliability: { path: '/stability/reliability', title: '可靠性' },
  slos: { path: '/stability/slos', title: 'SLO 与错误预算' },
  incidents: { path: '/stability/incidents', title: '事故与复盘' },
  events: { path: '/events', title: '事件' },
  settings: { path: '/settings/general', title: '任务与通知' },
  'notification-routing': { path: '/settings/notifications', title: '通知路由' },
  diagnostics: { path: '/settings/diagnostics', title: '系统诊断' },
}

export const primaryNavigation: Array<{ label: string; icon: NavIcon; defaultView: View; views: View[] }> = [
  { label: '总览', icon: 'dashboard', defaultView: 'dashboard', views: ['dashboard'] },
  { label: 'Provider', icon: 'providers', defaultView: 'providers', views: ['providers'] },
  { label: '验证中心', icon: 'validation', defaultView: 'scenarios', views: ['scenarios', 'comparisons'] },
  { label: '自动化', icon: 'automation', defaultView: 'schedules', views: ['schedules', 'failover', 'maintenance'] },
  { label: '稳定性', icon: 'stability', defaultView: 'reliability', views: ['reliability', 'slos', 'incidents'] },
  { label: '事件记录', icon: 'events', defaultView: 'events', views: ['events'] },
  { label: '设置', icon: 'settings', defaultView: 'settings', views: ['settings', 'notification-routing', 'diagnostics'] },
]

export const centers: Array<{ label: string; views: View[] }> = [
  { label: '验证中心', views: ['scenarios', 'comparisons'] },
  { label: '自动化', views: ['schedules', 'failover', 'maintenance'] },
  { label: '稳定性', views: ['reliability', 'slos', 'incidents'] },
  { label: '设置', views: ['settings', 'notification-routing', 'diagnostics'] },
]

const legacyPaths: Record<string, View> = {
  '/scenarios': 'scenarios', '/comparisons': 'comparisons', '/schedules': 'schedules', '/failover': 'failover', '/maintenance': 'maintenance',
  '/reliability': 'reliability', '/slos': 'slos', '/incidents': 'incidents', '/notification-routing': 'notification-routing', '/diagnostics': 'diagnostics', '/settings': 'settings',
}
const pathViews = new Map([...Object.entries(routes).map(([view, route]) => [route.path, view as View] as const), ...Object.entries(legacyPaths).map(([path, view]) => [path, view] as const)])

export const routePath = (view: View) => routes[view].path
export const routeTitle = (view: View) => routes[view].title
export const isViewIn = (view: View, views: View[]) => views.includes(view)
export const centerForView = (view: View) => centers.find(center => center.views.includes(view))
export const viewFromPath = (pathname: string) => pathViews.get(pathname.replace(/\/+$/, '') || '/') ?? (/^\/(?:validation\/)?comparisons\/[^/]+\/?$/.test(pathname) ? 'comparisons' : undefined)
export const canonicalizeLegacyPath = (pathname: string) => {
  const normalized = pathname.replace(/\/+$/, '') || '/'
  const view = legacyPaths[normalized]
  return view ? routes[view].path : ''
}
