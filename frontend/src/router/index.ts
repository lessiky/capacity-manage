import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'
import { useBrandingStore } from '@/stores/branding'

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/login', name: 'login', component: () => import('@/views/Login.vue'), meta: { title: '登录' } },
    {
      path: '/',
      component: () => import('@/layouts/MainLayout.vue'),
      redirect: '/dashboard',
      children: [
        { path: 'dashboard', name: 'dashboard', component: () => import('@/views/Dashboard.vue'), meta: { title: '首页', icon: 'Odometer' } },
        { path: 'systems', name: 'systems', component: () => import('@/views/Systems.vue'), meta: { title: '业务系统', icon: 'OfficeBuilding' } },
        { path: 'systems/view', name: 'system-view', component: () => import('@/views/SystemView.vue'), meta: { title: '系统视图', icon: 'DataBoard' } },
        { path: 'metrics', name: 'metrics', component: () => import('@/views/Metrics.vue'), meta: { title: '指标管理', icon: 'DataLine' } },
        { path: 'metrics/:code/detail', name: 'metric-detail', component: () => import('@/views/MetricDetail.vue'), meta: { title: '指标详情' } },
        { path: 'metrics/:code/aggregate-detail', name: 'metric-aggregate-detail', component: () => import('@/views/MetricAggregateDetail.vue'), meta: { title: '汇总指标' } },
        { path: 'data', name: 'data', component: () => import('@/views/DataManage.vue'), meta: { title: '数据管理', icon: 'Grid' } },
        { path: 'datasources', name: 'datasources', component: () => import('@/views/Datasources.vue'), meta: { title: '数据源', icon: 'Coin' } },
        { path: 'flows', name: 'flows', component: () => import('@/views/Flows.vue'), meta: { title: '采集管理', icon: 'SetUp' } },
        { path: 'scripts', redirect: { name: 'flows', query: { tab: 'scripts' } } },
        { path: 'tasks', redirect: { name: 'flows', query: { tab: 'tasks' } } },
        { path: 'alerts', name: 'alerts', component: () => import('@/views/Alerts.vue'), meta: { title: '告警中心', icon: 'Bell' } },
        { path: 'alert-policies', redirect: { name: 'alerts', query: { tab: 'policies' } } },
        { path: 'notifications', name: 'notifications', component: () => import('@/views/Notifications.vue'), meta: { title: '通知订阅', icon: 'Message' } },
        { path: 'reports', name: 'reports', component: () => import('@/views/Reports.vue'), meta: { title: '报表中心', icon: 'Document' } },
        { path: 'forecast-risk', name: 'forecast-risk', component: () => import('@/views/ForecastRisk.vue'), meta: { title: '预测风险', icon: 'TrendCharts' } },
        { path: 'forecast-events', redirect: { name: 'forecast-risk', query: { tab: 'events' } } },
        { path: 'open-api', name: 'open-api', component: () => import('@/views/OpenApiAdmin.vue'), meta: { title: '开放 API', icon: 'Connection' } },
        { path: 'tradingdays', name: 'tradingdays', component: () => import('@/views/TradingDays.vue'), meta: { title: '交易日', icon: 'Calendar' } },
        { path: 'admin', name: 'admin', component: () => import('@/views/Admin.vue'), meta: { title: '系统管理', icon: 'Setting' } },
      ],
    },
    { path: '/:pathMatch(.*)*', redirect: '/dashboard' },
  ],
})

router.beforeEach(async (to) => {
  const auth = useAuthStore()
  const branding = useBrandingStore()
  if (auth.isLoggedIn && !auth.user) {
    await auth.restore().catch(() => undefined)
  }
  if (to.name !== 'login' && !auth.isLoggedIn) {
    return { name: 'login', query: { redirect: to.fullPath } }
  }
  if (to.name === 'login' && auth.isLoggedIn) {
    return { name: 'dashboard' }
  }
  branding.setPageTitle((to.meta?.title as string) || '')
  return true
})

export default router
