<template>
  <div class="page-card">
    <div class="toolbar">
      <div class="toolbar-title">告警中心</div>
      <div>
        <el-button v-if="canExport" :icon="Download" @click="doExport">导出</el-button>
        <el-button :icon="Refresh" circle @click="onTabReload" />
      </div>
    </div>

    <!-- 统计条: P1-P4 未关闭数 + SLA 概览 -->
    <div class="stat-row">
      <div v-for="s in statCards" :key="s.label" class="stat-card" :style="{ borderTopColor: s.color }">
        <div class="stat-label">{{ s.label }}</div>
        <div class="stat-value" :style="{ color: s.color }">{{ s.value }}</div>
      </div>
    </div>

    <el-tabs v-model="tab" class="tabs module-tabs" @tab-change="onTabChange">
      <el-tab-pane label="自定义看板" name="dashboard">
        <AlertDashboard ref="dashboardRef" @open="openDetail" />
      </el-tab-pane>

      <el-tab-pane label="告警列表" name="list">
        <div class="filter-row">
          <el-select v-model="f.level" placeholder="级别" clearable style="width: 100px">
            <el-option label="P1" value="p1" /><el-option label="P2" value="p2" />
            <el-option label="P3" value="p3" /><el-option label="P4" value="p4" />
          </el-select>
          <el-select v-model="f.status" placeholder="状态" clearable style="width: 120px">
            <el-option label="活动" value="active" /><el-option label="处理中" value="processing" />
            <el-option label="已恢复" value="recovered" /><el-option label="已关闭" value="closed" />
          </el-select>
          <el-select v-model="f.alertType" placeholder="类型" clearable style="width: 130px">
            <el-option label="阈值" value="threshold" /><el-option label="缺数" value="missing" />
            <el-option label="采集失败" value="collect_fail" /><el-option label="异常值" value="anomaly" />
            <el-option label="数据源异常" value="ds_error" />
          </el-select>
          <el-input v-model="f.keyword" placeholder="告警号/指标/系统" clearable style="width: 190px" @keyup.enter="reload" />
          <el-date-picker v-model="f.range" type="daterange" value-format="YYYY-MM-DD"
            start-placeholder="开始日期" end-placeholder="结束日期" style="width: 240px" />
          <el-button type="primary" :icon="Search" @click="reload">查询</el-button>
          <el-button @click="resetFilter">重置</el-button>
        </div>
        <el-table :data="items" v-loading="loading" border stripe size="default" highlight-current-row
          @row-click="(row: AlertItem) => openDetail(row)">
          <el-table-column prop="alertNo" label="告警编号" width="170" />
          <el-table-column prop="level" label="级别" width="70">
            <template #default="{ row }"><el-tag :type="levelTag(row.level)">{{ row.level.toUpperCase() }}</el-tag></template>
          </el-table-column>
          <el-table-column prop="alertType" label="类型" width="100">
            <template #default="{ row }">{{ typeLabel(row.alertType) }}</template>
          </el-table-column>
          <el-table-column prop="systemCode" label="系统" width="140" show-overflow-tooltip />
          <el-table-column prop="metricCode" label="指标" width="160" show-overflow-tooltip />
          <el-table-column prop="status" label="状态" width="90">
            <template #default="{ row }"><el-tag :type="statusTag(row.status)">{{ statusLabel(row.status) }}</el-tag></template>
          </el-table-column>
          <el-table-column prop="triggerAt" label="触发时间" width="170">
            <template #default="{ row }">{{ fmtTs(row.triggerAt) }}</template>
          </el-table-column>
          <el-table-column prop="lastValue" label="最新值" width="90" align="right" />
          <el-table-column prop="threshold" label="阈值" width="80" align="right" />
          <el-table-column label="水位" width="80" align="right">
            <template #default="{ row }">{{ row.waterLevel != null ? (row.waterLevel * 100).toFixed(1) + '%' : '-' }}</template>
          </el-table-column>
          <el-table-column prop="durationMin" label="持续(min)" width="90" align="right">
            <template #default="{ row }">{{ row.durationMin ?? 0 }}</template>
          </el-table-column>
          <el-table-column prop="claimedBy" label="认领人" width="100">
            <template #default="{ row }">{{ row.claimedBy || '-' }}</template>
          </el-table-column>
        </el-table>
        <el-empty v-if="!loading && error" :description="error" />
        <el-empty v-else-if="!loading && items.length === 0" description="暂无告警" />
        <el-pagination
          v-model:current-page="page" :page-size="pageSize" :total="total"
          layout="total, prev, pager, next" class="pager" @current-change="reload"
        />
      </el-tab-pane>

      <el-tab-pane label="告警统计" name="stats">
        <div class="filter-row">
          <span class="filter-label">统计区间（默认近 30 天）</span>
          <el-date-picker v-model="statRange" type="daterange" value-format="YYYY-MM-DD"
            start-placeholder="开始日期" end-placeholder="结束日期" style="width: 240px" />
          <el-button type="primary" :icon="Search" @click="loadStats">查询</el-button>
        </div>
        <div class="stat-cards2">
          <div class="mini-card"><div class="mini-label">MTTA 平均响应</div><div class="mini-value">{{ fmtMin(stats?.mttaMinutes) }}</div></div>
          <div class="mini-card"><div class="mini-label">MTTR 平均闭环</div><div class="mini-value">{{ fmtMin(stats?.mttrMinutes) }}</div></div>
          <div v-for="s in (stats?.sla ?? [])" :key="s.level" class="mini-card">
            <div class="mini-label">{{ s.level.toUpperCase() }} SLA 达成率</div>
            <div class="mini-value" :style="{ color: (s.rate ?? 0) >= 90 ? '#34C759' : (s.rate ?? 0) >= 70 ? '#FF9500' : '#FF3B30' }">{{ s.rate != null ? s.rate.toFixed(1) + '%' : '-' }}</div>
          </div>
        </div>
        <div class="charts-row">
          <div class="chart-box">
            <div class="chart-title">告警趋势（按日 × 级别）</div>
            <div ref="trendRef" class="chart" />
          </div>
          <div class="chart-box">
            <div class="chart-title">高频告警 TOP 10</div>
            <div ref="topRef" class="chart" />
          </div>
        </div>
        <el-table :data="stats?.sla ?? []" border size="default" class="sla-table" v-if="(stats?.sla ?? []).length > 0">
          <el-table-column prop="level" label="级别" width="80">
            <template #default="{ row }">{{ row.level.toUpperCase() }}</template>
          </el-table-column>
          <el-table-column label="响应时限" width="110" align="right"><template #default="{ row }">{{ row.claimMin }} min</template></el-table-column>
          <el-table-column label="闭环时限" width="110" align="right"><template #default="{ row }">{{ fmtHr(row.resolveMin) }}</template></el-table-column>
          <el-table-column prop="total" label="应闭环" width="100" align="right" />
          <el-table-column prop="closed" label="已闭环" width="100" align="right" />
          <el-table-column prop="inTime" label="时限内闭环" width="110" align="right" />
          <el-table-column label="MTTA" width="110" align="right"><template #default="{ row }">{{ fmtMin(row.mttaMinutes) }}</template></el-table-column>
          <el-table-column label="MTTR" width="110" align="right"><template #default="{ row }">{{ fmtMin(row.mttrMinutes) }}</template></el-table-column>
          <el-table-column label="达成率" width="110" align="right">
            <template #default="{ row }">{{ row.rate != null ? row.rate.toFixed(1) + '%' : '-' }}</template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="告警策略" name="policies">
        <AlertPolicies embedded />
      </el-tab-pane>
    </el-tabs>

    <!-- 详情抽屉 -->
    <el-drawer v-model="drawer" size="620px" :with-header="false">
      <template v-if="detail">
        <div class="drawer-head">
          <span class="drawer-title">告警详情 {{ detail.alert.alertNo }}</span>
          <el-tag :type="levelTag(detail.alert.level)">{{ detail.alert.level.toUpperCase() }}</el-tag>
          <el-tag :type="statusTag(detail.alert.status)" style="margin-left: 8px">{{ statusLabel(detail.alert.status) }}</el-tag>
        </div>
        <el-descriptions :column="2" border size="small" class="desc">
          <el-descriptions-item label="系统">{{ detail.alert.systemCode || '-' }}</el-descriptions-item>
          <el-descriptions-item label="指标">{{ detail.alert.metricCode }}</el-descriptions-item>
          <el-descriptions-item label="触发时间">{{ fmtTs(detail.alert.triggerAt) }}</el-descriptions-item>
          <el-descriptions-item label="类型">{{ typeLabel(detail.alert.alertType) }}</el-descriptions-item>
          <el-descriptions-item label="首值">{{ detail.alert.firstValue ?? '-' }}</el-descriptions-item>
          <el-descriptions-item label="最新值">{{ detail.alert.lastValue ?? '-' }}<span v-if="detail.alert.lastValueAt" class="dim">（{{ fmtTs(detail.alert.lastValueAt) }}）</span></el-descriptions-item>
          <el-descriptions-item label="触发阈值">{{ detail.alert.threshold ?? '-' }}</el-descriptions-item>
          <el-descriptions-item label="水位">{{ detail.alert.waterLevel != null ? (detail.alert.waterLevel * 100).toFixed(1) + '%' : '-' }}</el-descriptions-item>
          <el-descriptions-item label="持续时长">{{ detail.alert.durationMin ?? 0 }} min</el-descriptions-item>
          <el-descriptions-item label="响应时限">{{ fmtTs(detail.alert.deadlineAt) }}</el-descriptions-item>
          <el-descriptions-item label="处理结果" :span="2">{{ detail.alert.closeResult || '-' }}</el-descriptions-item>
        </el-descriptions>

        <div class="drawer-sub">
          <span class="drawer-sub-title">指标趋势（触发日前 7 日 / 触发日日内）</span>
          <el-radio-group v-model="snippetGran" size="small" @change="renderSnippet">
            <el-radio-button value="day">日粒度</el-radio-button>
            <el-radio-button value="rt">日内</el-radio-button>
          </el-radio-group>
        </div>
        <div ref="snippetRef" class="chart chart-snippet" />

        <div class="drawer-sub">
          <span class="drawer-sub-title">处理轨迹</span>
        </div>
        <el-timeline class="timeline">
          <el-timeline-item
            v-for="(it, i) in detail.timeline" :key="i"
            :timestamp="fmtTs(it.time)" placement="top"
            :type="timelineType(it.event)"
          >
            <div class="tl-event">{{ eventLabel(it.event) }}<span v-if="it.user" class="tl-user"> · {{ it.user }}</span></div>
            <div v-if="it.detail" class="tl-detail">{{ it.detail }}</div>
            <div v-if="it.evidence && it.evidence.windowStart" class="tl-evidence">
              证据窗口 {{ it.evidence.windowStart }} ~ {{ it.evidence.windowEnd }}，
              {{ it.evidence.mode === 'consecutive' ? `连续 ${it.evidence.windowDays} 天` : `最近 ${it.evidence.windowDays} 天中 ${it.evidence.hitDays} 天` }}
            </div>
          </el-timeline-item>
        </el-timeline>

        <div class="drawer-actions">
          <el-button v-if="['active', 'processing'].includes(detail.alert.status)" type="primary" :disabled="detail.alert.status === 'processing'" @click="doClaim">
            {{ detail.alert.status === 'processing' ? '已认领' : '认领' }}
          </el-button>
          <el-button :disabled="noteDisabled" @click="noteDialog = true">添加备注</el-button>
          <el-button v-if="detail.alert.status !== 'closed'" type="danger" @click="closeDialog = true">关闭告警</el-button>
          <span class="dim" v-if="detail.alert.status === 'closed'">已关闭（终态, 仅可追加备注）</span>
        </div>
      </template>
    </el-drawer>

    <!-- 备注对话框 -->
    <el-dialog v-model="noteDialog" title="添加处理备注" width="480px">
      <el-input v-model="noteText" type="textarea" :rows="3" placeholder="请填写处理进展/备注（必填）" maxlength="500" show-word-limit />
      <template #footer>
        <el-button @click="noteDialog = false">取消</el-button>
        <el-button type="primary" :loading="acting" @click="doNote">提交</el-button>
      </template>
    </el-dialog>

    <!-- 关闭对话框 -->
    <el-dialog v-model="closeDialog" title="关闭告警" width="480px">
      <el-input v-model="closeResult" type="textarea" :rows="3" placeholder="请填写处理结果（必填, 如: 已扩容/误报/业务调整）" maxlength="1000" show-word-limit />
      <template #footer>
        <el-button @click="closeDialog = false">取消</el-button>
        <el-button type="danger" :loading="acting" @click="doClose">确认关闭</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, reactive, ref, watch } from 'vue'
import { Download, Refresh, Search } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import * as echarts from 'echarts'
import { http } from '@/api/http'
import { useList } from '@/composables/useList'
import { fmtTs } from '@/utils/format'
import { useAuthStore } from '@/stores/auth'
import { useQueryTab } from '@/composables/useQueryTab'
import { levelTag, statusLabel, statusTag, typeLabel } from '@/utils/alert'
import AlertPolicies from '@/views/AlertPolicies.vue'
import AlertDashboard from '@/components/AlertDashboard.vue'

interface AlertItem {
  id: number; alertNo: string; level: string; alertType: string
  systemCode: string; metricCode: string; status: string
  triggerAt: string; lastValueAt: string | null
  firstValue: number | null; lastValue: number | null
  threshold: number | null; waterLevel: number | null
  durationMin: number | null; claimedBy: string; claimedAt: string | null
  recoveredAt: string | null; closedAt: string | null; closeResult: string
  deadlineAt: string | null; silent: boolean
}
interface Detail {
  alert: AlertItem
  timeline: { time: string; event: string; user?: string; detail?: string; evidence?: Record<string, any> }[]
  trendSnippet: { day: { bizDate: string; statValue: number | null; level: string }[]; rt: { ts: string; cleanValue: number | null; judgeLevel: string }[] }
}
interface SlaRow { level: string; claimMin: number; resolveMin: number; total: number; closed: number; inTime: number; mttaMinutes: number | null; mttrMinutes: number | null; rate: number | null }
interface Stats {
  byLevel: { level: string; n: number }[]
  activeByLevel: { level: string; n: number }[]
  topMetrics: { metricCode: string; n: number }[]
  trend: { date: string; p1: number; p2: number; p3: number; p4: number }[]
  sla: SlaRow[]
  mttaMinutes: number | null; mttrMinutes: number | null
}

const auth = useAuthStore()
const canExport = computed(() => auth.isAdmin)

const dashboardRef = ref<InstanceType<typeof AlertDashboard> | null>(null)
const { activeTab: tab, onTabChange: updateTab } = useQueryTab(
  ['dashboard', 'list', 'stats', 'policies'] as const,
  'dashboard',
)
const { items, total, loading, error, page, pageSize, params, reload } = useList<AlertItem>('/api/v1/alerts')

const f = reactive<{ level: string; status: string; alertType: string; keyword: string; range: string[] | null }>({
  level: '', status: '', alertType: '', keyword: '', range: null,
})
function syncParams() {
  params.value = {
    level: f.level || undefined, status: f.status || undefined,
    alertType: f.alertType || undefined, keyword: f.keyword || undefined,
    from: f.range?.[0] || undefined, to: f.range?.[1] || undefined,
  }
}
watch(f, syncParams)

function resetFilter() {
  f.level = ''; f.status = ''; f.alertType = ''; f.keyword = ''; f.range = null
  page.value = 1
  reload()
}

// ---------- 统计条 ----------
const stats = ref<Stats | null>(null)
const statRange = ref<string[] | null>(null)
const statCards = computed(() => {
  const act = (lv: string) => stats.value?.activeByLevel?.find(x => x.level === lv)?.n ?? 0
  const p1sla = stats.value?.sla?.find(x => x.level === 'p1')
  return [
    { label: 'P1 未关闭', value: act('p1'), color: '#FF3B30' },
    { label: 'P2 未关闭', value: act('p2'), color: '#FF9500' },
    { label: 'P3 未关闭', value: act('p3'), color: '#E6A23C' },
    { label: 'P4 未关闭', value: act('p4'), color: '#909399' },
    { label: '近30天 MTTA(min)', value: fmtMin(stats.value?.mttaMinutes), color: '#1F6FEB' },
    { label: 'P1 SLA 达成率', value: p1sla?.rate != null ? p1sla.rate.toFixed(1) + '%' : '-', color: '#1F6FEB' },
  ]
})

async function loadStats() {
  const qs = statRange.value ? `from=${statRange.value[0]}&to=${statRange.value[1]}` : ''
  stats.value = (await http.get('/api/v1/alerts/stats' + (qs ? '?' + qs : ''))) as unknown as Stats
  renderStatCharts()
}

// ---------- 统计图表 ----------
let trendChart: echarts.ECharts | null = null
let topChart: echarts.ECharts | null = null
const trendRef = ref<HTMLElement | null>(null)
const topRef = ref<HTMLElement | null>(null)

function renderStatCharts() {
  const data = stats.value
  if (!data) return
  if (trendChart) {
    trendChart.setOption({
      legend: { data: ['P1', 'P2', 'P3', 'P4'], top: 0 },
      grid: { left: 40, right: 16, top: 30, bottom: 24 },
      tooltip: { trigger: 'axis' },
      xAxis: { type: 'category', data: data.trend.map(x => x.date) },
      yAxis: { type: 'value', minInterval: 1 },
      series: [
        { name: 'P1', type: 'line', stack: 't', data: data.trend.map(x => x.p1), itemStyle: { color: '#FF3B30' }, areaStyle: {} },
        { name: 'P2', type: 'line', stack: 't', data: data.trend.map(x => x.p2), itemStyle: { color: '#FF9500' }, areaStyle: {} },
        { name: 'P3', type: 'line', stack: 't', data: data.trend.map(x => x.p3), itemStyle: { color: '#E6A23C' }, areaStyle: {} },
        { name: 'P4', type: 'line', stack: 't', data: data.trend.map(x => x.p4), itemStyle: { color: '#909399' }, areaStyle: {} },
      ],
    }, true)
  }
  if (topChart) {
    const rows = [...data.topMetrics].reverse()
    topChart.setOption({
      grid: { left: 120, right: 30, top: 10, bottom: 24 },
      tooltip: { trigger: 'axis', axisPointer: { type: 'shadow' } },
      xAxis: { type: 'value', minInterval: 1 },
      yAxis: { type: 'category', data: rows.map(x => x.metricCode) },
      series: [{ type: 'bar', data: rows.map(x => x.n), itemStyle: { color: '#1F6FEB' }, barMaxWidth: 18 }],
    }, true)
  }
}

function onTabChange(name: string | number) {
  updateTab(name)
  if (name === 'stats') {
    loadStats()
    requestAnimationFrame(() => {
      if (trendRef.value && !trendChart) trendChart = echarts.init(trendRef.value)
      if (topRef.value && !topChart) topChart = echarts.init(topRef.value)
      renderStatCharts()
    })
  }
}
function onTabReload() {
  if (tab.value === 'dashboard') {
    dashboardRef.value?.reload()
  } else if (tab.value === 'list') {
    reload()
    loadStats()
  } else {
    loadStats()
  }
}

// ---------- 详情抽屉 ----------
const drawer = ref(false)
const detail = ref<Detail | null>(null)
const snippetGran = ref<'day' | 'rt'>('day')
const snippetRef = ref<HTMLElement | null>(null)
let snippetChart: echarts.ECharts | null = null

function openDetail(row: Pick<AlertItem, 'id'>) {
  http.get(`/api/v1/alerts/${row.id}`).then((d) => {
    detail.value = d as unknown as Detail
    drawer.value = true
    requestAnimationFrame(renderSnippet)
  }).catch((e) => ElMessage.error(e.message))
}

function renderSnippet() {
  if (!snippetRef.value || !detail.value) return
  if (!snippetChart) snippetChart = echarts.init(snippetRef.value)
  const s = detail.value.trendSnippet
  const a = detail.value.alert
  const markLine = []
  if (a.threshold != null && snippetGran.value === 'day') {
    markLine.push({ yAxis: a.threshold, lineStyle: { color: a.level === 'p1' ? '#FF3B30' : '#FF9500', type: 'dashed' }, label: { formatter: '阈值 ' + a.threshold } })
  }
  if (snippetGran.value === 'day') {
    snippetChart.setOption({
      grid: { left: 45, right: 16, top: 24, bottom: 24 },
      tooltip: { trigger: 'axis' },
      xAxis: { type: 'category', data: s.day.map(x => x.bizDate.slice(5)) },
      yAxis: { type: 'value' },
      series: [{
        type: 'line', data: s.day.map(x => x.statValue), connectNulls: false,
        itemStyle: { color: '#1F6FEB' },
        markLine: { symbol: 'none', data: markLine },
        markPoint: {
          data: s.day.filter(x => x.level === 'missing').map(x => ({ coord: [x.bizDate.slice(5), 0], value: '缺', symbolSize: 26, itemStyle: { color: '#FF3B30' } })),
        },
      }],
    }, true)
  } else {
    snippetChart.setOption({
      grid: { left: 45, right: 16, top: 24, bottom: 24 },
      tooltip: { trigger: 'axis' },
      xAxis: { type: 'category', data: s.rt.map(x => x.ts.slice(11, 16)) },
      yAxis: { type: 'value' },
      series: [{
        type: 'line', data: s.rt.map(x => x.cleanValue), connectNulls: false,
        itemStyle: { color: '#1F6FEB' },
        markLine: { symbol: 'none', data: markLine },
      }],
    }, true)
  }
}

// ---------- 操作 ----------
const acting = ref(false)
const noteDialog = ref(false)
const closeDialog = ref(false)
const noteText = ref('')
const closeResult = ref('')
const noteDisabled = computed(() => !detail.value)

async function doClaim() {
  if (!detail.value) return
  acting.value = true
  try {
    await http.post(`/api/v1/alerts/${detail.value.alert.id}/claim`)
    ElMessage.success('已认领')
    await refreshDetail()
    reload()
  } catch (e) { ElMessage.error(e instanceof Error ? e.message : String(e)) } finally { acting.value = false }
}
async function doNote() {
  if (!detail.value) return
  if (!noteText.value.trim()) { ElMessage.warning('备注不能为空'); return }
  acting.value = true
  try {
    await http.post(`/api/v1/alerts/${detail.value.alert.id}/note`, { note: noteText.value.trim() })
    noteText.value = ''
    noteDialog.value = false
    ElMessage.success('备注已添加')
    await refreshDetail()
  } catch (e) { ElMessage.error(e instanceof Error ? e.message : String(e)) } finally { acting.value = false }
}
async function doClose() {
  if (!detail.value) return
  if (!closeResult.value.trim()) { ElMessage.warning('关闭必须填写处理结果'); return }
  acting.value = true
  try {
    await ElMessageBox.confirm('确认关闭该告警？关闭后仅可追加备注。', '提示', { type: 'warning' })
    await http.post(`/api/v1/alerts/${detail.value.alert.id}/close`, { result: closeResult.value.trim() })
    closeResult.value = ''
    closeDialog.value = false
    ElMessage.success('告警已关闭')
    await refreshDetail()
    reload()
  } catch (e) {
    if (e !== 'cancel') ElMessage.error(e instanceof Error ? e.message : String(e))
  } finally { acting.value = false }
}
async function refreshDetail() {
  if (!detail.value) return
  const d = (await http.get(`/api/v1/alerts/${detail.value.alert.id}`)) as unknown as Detail
  detail.value = d
  requestAnimationFrame(renderSnippet)
}

// ---------- 导出 ----------
async function doExport() {
  try {
    const resp = await http.get('/api/v1/alerts/export', { responseType: 'blob' }) as unknown as Blob
    const url = URL.createObjectURL(resp)
    const a = document.createElement('a')
    a.href = url
    a.download = 'alerts.xlsx'
    a.click()
    URL.revokeObjectURL(url)
  } catch (e) { ElMessage.error(e instanceof Error ? e.message : String(e)) }
}

// ---------- 标签映射 ----------
function eventLabel(e: string): string {
  return ({ trigger: '触发', escalate: '升级', downgrade_suggestion: '降级建议', claim: '认领', note: '备注', recover: '恢复', close: '关闭' } as Record<string, string>)[e] || e
}
function timelineType(e: string): string {
  return ({ trigger: 'danger', escalate: 'warning', downgrade_suggestion: 'info', claim: 'primary', note: 'primary', recover: 'success', close: 'info' } as Record<string, string>)[e] || 'info'
}
function fmtMin(v: number | null | undefined): string {
  if (v == null) return '-'
  return v >= 60 ? (v / 60).toFixed(1) + 'h' : v.toFixed(1) + 'min'
}
function fmtHr(min: number): string {
  return min >= 60 ? (min / 60) + 'h' : min + 'min'
}

onMounted(loadStats)
onBeforeUnmount(() => {
  trendChart?.dispose(); topChart?.dispose(); snippetChart?.dispose()
})
</script>

<style scoped>
.toolbar { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; }
.toolbar-title { font-size: 15px; font-weight: 600; }
.stat-row { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: 12px; margin-bottom: 12px; }
.stat-card {
  background: #fff; border: 1px solid #ebeef5; border-top: 3px solid #1F6FEB;
  border-radius: 6px; padding: 10px 14px;
}
.stat-label { font-size: 12px; color: #909399; }
.stat-value { font-size: 24px; font-weight: 700; font-variant-numeric: tabular-nums; margin-top: 4px; }
.tabs { margin-top: 4px; }
.filter-row { display: flex; align-items: center; gap: 8px; margin-bottom: 12px; flex-wrap: wrap; }
.filter-label { font-size: 13px; color: #909399; }
.pager { margin-top: 12px; display: flex; justify-content: flex-end; }
.stat-cards2 { display: grid; grid-template-columns: repeat(auto-fit, minmax(150px, 1fr)); gap: 12px; margin-bottom: 12px; }
.mini-card { background: #fafbfc; border: 1px solid #ebeef5; border-radius: 6px; padding: 10px 14px; }
.mini-label { font-size: 12px; color: #909399; }
.mini-value { font-size: 20px; font-weight: 700; margin-top: 4px; font-variant-numeric: tabular-nums; }
.charts-row { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; margin-bottom: 12px; }
.chart-box { background: #fff; border: 1px solid #ebeef5; border-radius: 6px; padding: 8px 12px; }
.chart-title { font-size: 13px; font-weight: 600; margin: 4px 0; }
.chart { width: 100%; height: 260px; }
.sla-table { margin-top: 4px; }
.drawer-head { display: flex; align-items: center; gap: 8px; margin-bottom: 12px; }
.drawer-title { font-size: 15px; font-weight: 600; }
.desc { margin-bottom: 16px; }
.dim { color: #909399; font-size: 12px; margin-left: 4px; }
.drawer-sub { display: flex; align-items: center; justify-content: space-between; margin: 14px 0 8px; }
.drawer-sub-title { font-size: 13px; font-weight: 600; }
.chart-snippet { height: 200px; }
.timeline { padding-left: 4px; }
.tl-event { font-size: 13px; font-weight: 600; }
.tl-user { color: #909399; font-weight: 400; }
.tl-detail { font-size: 12px; color: #606266; margin-top: 2px; }
.tl-evidence { font-size: 12px; color: #b88230; background: #fdf6ec; border-radius: 4px; padding: 5px 8px; margin-top: 6px; }
.drawer-actions { margin-top: 16px; display: flex; align-items: center; gap: 8px; border-top: 1px solid #ebeef5; padding-top: 12px; }
</style>
