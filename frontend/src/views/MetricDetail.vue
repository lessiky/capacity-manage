<template>
  <div class="page-card" v-loading="loading">
    <div class="toolbar">
      <div class="toolbar-title">
        <el-button link :icon="ArrowLeft" @click="$router.push('/metrics')" class="back" />
        <span v-if="metric">{{ metric.name }}（{{ metric.metricCode }}）</span>
      </div>
      <div class="toolbar-right">
        <label class="switch-label">
          <el-switch v-model="showForecastCard" size="small" />
          <span>容量预测</span>
        </label>
        <el-radio-group v-model="granularity" size="small" @change="loadTrend">
          <el-radio-button value="day">日粒度</el-radio-button>
          <el-radio-button value="rt">日内数据</el-radio-button>
        </el-radio-group>
      </div>
    </div>

    <!-- 概览卡片 -->
    <div class="info-row" v-if="metric">
      <div class="info-card">
        <div class="info-label">当前值</div>
        <div class="info-value">{{ currentLabel }}</div>
        <div class="info-sub">{{ metric.unit }}</div>
      </div>
      <div class="info-card">
        <div class="info-label">容量水位</div>
        <div class="info-value" :style="{ color: waterColor }">{{ waterLabel }}</div>
        <div class="info-sub">基线 {{ metric.baseline ?? '-' }}</div>
      </div>
      <div class="info-card">
        <div class="info-label">三级阈值</div>
        <div class="info-thr">
          <span class="thr warn">预警 {{ metric.warnLine ?? '-' }}</span>
          <span class="thr danger">危险 {{ metric.dangerLine ?? '-' }}</span>
        </div>
        <div class="info-sub">{{ statModeLabel(metric.statMode) }} / {{ metric.judgeDirection === 'lower_worse' ? '越低越危险' : '越高越危险' }}</div>
      </div>
      <div class="info-card">
        <div class="info-label">最近告警</div>
        <div class="info-value" :style="{ color: lastAlert ? alertColor(lastAlert.level) : '#34C759' }">
          {{ lastAlert ? lastAlert.level.toUpperCase() : '无' }}
        </div>
        <div class="info-sub" @click="lastAlert && $router.push('/alerts')" :class="{ link: !!lastAlert }">
          {{ lastAlert ? fmtTs(lastAlert.triggerAt) + ' ' + statusLabel(lastAlert.status) : '-' }}
        </div>
      </div>
      <div class="info-card">
        <div class="info-label">采集健康</div>
        <div class="info-value" :style="{ color: healthColor }">{{ healthLabel }}</div>
        <div class="info-sub">{{ healthSub }}</div>
      </div>
    </div>

    <!-- 统计预测（M7）: 叠加预测曲线 / 置信区间 / ETA / 解释 -->
    <div class="fc-card" v-if="showForecastCard" v-loading="forecastLoading">
      <div class="fc-head">
        <div class="chart-title">
          容量预测<span class="fc-sub">虚线为统计预测曲线, 阴影为 95% 置信区间（仅日粒度显示）</span>
        </div>
        <el-radio-group v-model="horizon" size="small" @change="loadForecast">
          <el-radio-button :value="30">未来 30 天</el-radio-button>
          <el-radio-button :value="90">未来 90 天</el-radio-button>
        </el-radio-group>
      </div>
      <template v-if="forecast">
        <div class="fc-row">
          <div class="fc-item"><div class="fc-k">预测模型</div><div class="fc-v">{{ modelLabel(forecast.modelType) }}</div></div>
          <div class="fc-item"><div class="fc-k">预测状态</div><div class="fc-v"><el-tag size="small" :type="fcStatusTag">{{ fcStatusLabel }}</el-tag></div></div>
          <div class="fc-item"><div class="fc-k">预计到预警线</div><div class="fc-v">{{ dateOnly(forecast.etaWarnDate) }}</div></div>
          <div class="fc-item"><div class="fc-k">预计到危险线</div><div class="fc-v">{{ dateOnly(forecast.etaDangerDate) }}</div></div>
          <div class="fc-item" v-if="forecast.aiStatus === 'oversized' && forecast.suggestedEtaWarnDate">
            <div class="fc-k">AI 建议预警日</div><div class="fc-v" style="color: #FF9500">{{ dateOnly(forecast.suggestedEtaWarnDate) }}</div>
          </div>
          <div class="fc-item" v-if="forecast.aiStatus === 'oversized' && forecast.suggestedEtaDangerDate">
            <div class="fc-k">AI 建议危险日</div><div class="fc-v" style="color: #FF3B30">{{ dateOnly(forecast.suggestedEtaDangerDate) }}</div>
          </div>
          <div class="fc-item"><div class="fc-k">风险评分</div><div class="fc-v" :style="{ color: riskColor(forecast.riskScore) }">{{ fmt3(forecast.riskScore) }}</div></div>
          <div class="fc-item"><div class="fc-k">模型置信度</div><div class="fc-v">{{ fmt3(forecast.confidence) }}</div></div>
          <div class="fc-item"><div class="fc-k">数据质量</div><div class="fc-v" :style="{ color: (forecast.dataQuality ?? 1) < 0.8 ? '#FF9500' : undefined }">{{ fmt3(forecast.dataQuality) }}</div></div>
          <div class="fc-item"><div class="fc-k">预测批次</div><div class="fc-v">#{{ forecast.runId }}</div></div>
        </div>
        <div class="fc-explain">{{ forecast.explanation || '暂无预测说明' }}</div>
        <!-- AI 修正对比与降级提示（M8, Task 9） -->
        <div class="fc-ai" v-if="forecast.aiStatus">
          <el-tag size="small" :type="aiStatusTag">{{ aiStatusLabel }}</el-tag>
          <span class="fc-ai-meta" v-if="forecast.aiModel">模型 {{ forecast.aiModel }}</span>
          <span class="fc-ai-meta" v-if="forecast.etaShiftDays != null && forecast.etaShiftDays > 0">ETA 偏移 {{ forecast.etaShiftDays }} 天</span>
          <span class="fc-ai-meta" v-if="forecast.aiLatencyMs != null">耗时 {{ forecast.aiLatencyMs }}ms</span>
          <span class="fc-ai-meta" v-if="forecast.eventFactorVersion">事件因子 {{ forecast.eventFactorVersion }}</span>
        </div>
        <div class="fc-ai" v-else-if="forecast.eventFactorVersion">
          <span class="fc-ai-meta">事件因子版本 {{ forecast.eventFactorVersion }}</span>
        </div>
        <div class="fc-ai-note" :class="{ degrade: isAiDegraded }" v-if="aiNote">{{ aiNote }}</div>
      </template>
      <el-empty v-else-if="!forecastLoading" :image-size="60" description="该指标暂无预测结果, 请等待每日预测批次或由管理员重新计算" />
    </div>

    <!-- 趋势图 -->
    <div class="chart-card">
      <div class="chart-toolbar">
        <div class="chart-title">指标趋势（叠加基线/预警/危险参考线, 超线标注, 缺数标记）</div>
        <div class="range-group">
          <label class="switch-label" v-if="granularity === 'day'">
            <el-switch v-model="showForecastLine" size="small" />
            <span>预测线</span>
          </label>
          <el-radio-group v-if="granularity === 'day' && showForecastLine && !showForecastCard" v-model="horizon" size="small" @change="loadForecast">
            <el-radio-button :value="30">30 天</el-radio-button>
            <el-radio-button :value="90">90 天</el-radio-button>
          </el-radio-group>
          <template v-if="rangeMode !== 'custom'">
            <el-radio-group v-model="days" size="small" @change="loadTrend">
              <el-radio-button :value="7">7 天</el-radio-button>
              <el-radio-button :value="30">30 天</el-radio-button>
              <el-radio-button :value="90">90 天</el-radio-button>
            </el-radio-group>
          </template>
          <el-date-picker v-else v-model="customRange" type="daterange" value-format="YYYY-MM-DD"
            start-placeholder="开始" end-placeholder="结束" size="small" style="width: 240px" @change="loadTrend" />
          <el-button size="small" @click="toggleCustom">{{ rangeMode === 'custom' ? '预设' : '自定义' }}</el-button>
        </div>
      </div>
      <div ref="chartRef" class="chart" />
    </div>

    <!-- 区间统计 + 历史峰谷值 -->
    <div class="stat-row" v-if="trend">
      <div class="stat-card">
        <div class="stat-label">区间最大值</div>
        <div class="stat-value">{{ fmtNum(stats?.max) }}</div>
        <div class="stat-sub">{{ rangeTsLabel(stats?.maxTs) }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">区间最小值</div>
        <div class="stat-value">{{ fmtNum(stats?.min) }}</div>
        <div class="stat-sub">{{ rangeTsLabel(stats?.minTs) }}</div>
      </div>
      <div class="stat-card">
        <div class="stat-label">区间平均值</div>
        <div class="stat-value">{{ fmtNum(stats?.avg) }}</div>
        <div class="stat-sub">{{ stats?.count ?? 0 }} 个数据点</div>
      </div>
      <div class="peak-card">
        <div class="stat-label">
          <span>历史峰谷值（已落地存储）</span>
          <el-button link size="small" type="primary" @click="recalcPeaks">重算</el-button>
        </div>
        <div class="peak-grid">
          <div class="peak-item"><span class="peak-k">峰值</span><span class="peak-v">{{ fmtNum(peaks?.maxValue) }}</span><span class="peak-d">{{ dateOnly(peaks?.maxBizDate) }}</span></div>
          <div class="peak-item"><span class="peak-k">谷值</span><span class="peak-v">{{ fmtNum(peaks?.minValue) }}</span><span class="peak-d">{{ dateOnly(peaks?.minBizDate) }}</span></div>
        </div>
        <div class="stat-sub">已更新 {{ peaks?.updatedAt ? fmtTs(peaks.updatedAt) : '-' }}</div>
      </div>
    </div>

    <!-- 区间数据明细 -->
    <div class="table-card">
      <div class="chart-title">区间数据明细（{{ series.length }} 条）</div>
      <el-table :data="series" size="small" max-height="360" empty-text="区间内暂无数据">
        <el-table-column :label="granularity === 'day' ? '日期' : '时间'" min-width="130">
          <template #default="{ row }">{{ tsLabel(row.ts) }}</template>
        </el-table-column>
        <el-table-column label="数值" min-width="150" align="right">
          <template #default="{ row }"><span class="num">{{ fmtNum(row.value) }}</span></template>
        </el-table-column>
        <el-table-column label="级别" width="100">
          <template #default="{ row }">
            <el-tag v-if="row.judgeLevel && row.judgeLevel !== 'normal'" :type="levelTag(row.judgeLevel)" size="small">{{ levelLabel(row.judgeLevel) }}</el-tag>
            <span v-else class="muted">正常</span>
          </template>
        </el-table-column>
        <el-table-column v-if="granularity === 'rt'" label="异常" min-width="160">
          <template #default="{ row }">{{ row.isAnomaly ? (row.anomalyReason || '异常') : '-' }}</template>
        </el-table-column>
      </el-table>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ArrowLeft } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import * as echarts from 'echarts'
import { http } from '@/api/http'
import { fmtTs } from '@/utils/format'

const route = useRoute()
const router = useRouter()
const code = route.params.code as string

const loading = ref(false)
const metric = ref<Record<string, any> | null>(null)
const trend = ref<Record<string, any> | null>(null)
const granularity = ref<'day' | 'rt'>('day')
const days = ref(30)
const rangeMode = ref<'preset' | 'custom'>('preset')
const customRange = ref<string[] | null>(null)

const chartRef = ref<HTMLElement | null>(null)
let chart: echarts.ECharts | null = null

// ---------- 统计预测（M7, Task 8） ----------
interface ForecastCurvePoint { bizDate: string; value: number; lower: number; upper: number }
interface ForecastData {
  runId: number
  metricCode: string
  horizonDays: number
  modelType: string
  status: string
  etaWarnDate: string | null
  etaDangerDate: string | null
  riskScore: number | null
  confidence: number | null
  dataQuality: number | null
  slope: number | null
  explanation: string
  curve: ForecastCurvePoint[] | null
  aiStatus?: string | null
  advisoryOnly?: boolean
  aiModel?: string | null
  aiRationale?: string | null
  aiError?: string | null
  etaShiftDays?: number | null
  aiLatencyMs?: number | null
  eventFactorVersion?: string | null
  adjustmentNote?: string | null
  suggestedEtaWarnDate?: string | null
  suggestedEtaDangerDate?: string | null
  baseSnapshot?: { curve?: ForecastCurvePoint[] | null } | null
  adjustedSnapshot?: { curve?: ForecastCurvePoint[] | null } | null
}
const horizon = ref(30)
const forecast = ref<ForecastData | null>(null)
const forecastLoading = ref(false)
const showForecastCard = ref(localStorage.getItem('cap_metric_show_forecast_card') !== '0')
const showForecastLine = ref(localStorage.getItem('cap_metric_show_forecast_line') !== '0')
const fcStatusLabel = computed(() => ({
  success: '命中风险', never: '窗口内未到线', insufficient_data: '数据不足', insufficient_quality: '质量不足',
} as Record<string, string>)[forecast.value?.status ?? ''] ?? '暂无结果')
const fcStatusTag = computed(() =>
  ({ success: 'danger', never: 'success', insufficient_data: 'info', insufficient_quality: 'warning' } as Record<string, string>)[forecast.value?.status ?? ''] ?? 'info',
)
function modelLabel(m?: string): string {
  return ({ linear: '线性回归', growth: '增长率', ai_adjusted: 'AI 修正' } as Record<string, string>)[m ?? ''] ?? (m || '-')
}
const aiStatusLabel = computed(() => ({
  success: 'AI 修正生效', oversized: 'AI 建议仅参考', invalid: 'AI 已降级', disabled: 'AI 未启用',
} as Record<string, string>)[forecast.value?.aiStatus ?? ''] ?? '')
const aiStatusTag = computed(() =>
  ({ success: 'success', oversized: 'warning', invalid: 'danger', disabled: 'info' } as Record<string, string>)[forecast.value?.aiStatus ?? ''] ?? 'info',
)
const isAiDegraded = computed(() => ['oversized', 'invalid', 'disabled'].includes(forecast.value?.aiStatus ?? ''))
const aiNote = computed(() => {
  const f = forecast.value
  if (!f?.aiStatus) return ''
  if (f.aiStatus === 'success') {
    return f.aiRationale ? `AI 修正理由：${f.aiRationale}` : ''
  }
  if (f.aiStatus === 'oversized') {
    return f.adjustmentNote || (f.aiRationale ? `AI 建议：${f.aiRationale}` : '')
  }
  return f.adjustmentNote || f.aiRationale || f.aiError || 'AI 修正不可用，已回退统计预测，主流程不受影响。'
})
function riskColor(v: number | null | undefined): string {
  const n = v ?? 0
  return n >= 0.8 ? '#FF3B30' : n >= 0.6 ? '#FF9500' : n >= 0.4 ? '#1F6FEB' : '#909399'
}
function fmt3(v: number | null | undefined): string {
  return v == null ? '-' : Number(v).toFixed(3)
}

const series = computed(() => trend.value?.series ?? [])
const stats = computed(() => trend.value?.stats ?? null)
const peaks = computed(() => trend.value?.peaks ?? null)

function fmtNum(v: unknown): string {
  if (v == null || v === '') return '-'
  const n = Number(v)
  return isFinite(n) ? n.toLocaleString('zh-CN', { maximumFractionDigits: 8 }) : String(v)
}
function tsLabel(ts: unknown): string {
  if (!ts) return '-'
  const s = String(ts)
  return granularity.value === 'day' ? s.slice(0, 10) : s.slice(0, 16).replace('T', ' ')
}
function rangeTsLabel(ts: unknown): string {
  if (!ts) return '-'
  const s = String(ts)
  return granularity.value === 'day' ? s.slice(0, 10) : s.slice(0, 16).replace('T', ' ')
}
function dateOnly(ts: unknown): string {
  return ts ? String(ts).slice(0, 10) : '-'
}
function levelLabel(lv: string): string {
  return ({ warn: '预警', danger: '危险', missing: '缺数' } as Record<string, string>)[lv] || lv
}
function levelTag(lv: string): string {
  return ({ warn: 'warning', danger: 'danger', missing: 'info' } as Record<string, string>)[lv] || 'info'
}

async function recalcPeaks() {
  try {
    const r = (await http.post(`/api/v1/metrics/${code}/peaks/recalc`)) as Record<string, any> | null
    if (trend.value) trend.value.peaks = r?.peaks ?? null
    ElMessage.success('历史峰值已重算')
  } catch { /* 错误提示已由 http 拦截器处理 */ }
}
const lastPoint = computed(() => {
  const s = series.value
  for (let i = s.length - 1; i >= 0; i--) {
    if (s[i].value != null) return s[i]
  }
  return null
})
const currentLabel = computed(() => lastPoint.value?.value != null ? Number(lastPoint.value.value).toLocaleString() : '-')
const waterValue = computed(() => {
  const m = metric.value
  const v = lastPoint.value?.value
  if (m == null || v == null || m.baseline == null || m.baseline === 0) return null
  let wl = m.judgeDirection === 'lower_worse' ? (m.baseline - v) / m.baseline : v / m.baseline
  return Math.min(1, Math.max(0, wl))
})
const waterLabel = computed(() => waterValue.value != null ? (waterValue.value * 100).toFixed(1) + '%' : '-')
const waterColor = computed(() => {
  const wl = waterValue.value
  if (wl == null) return '#909399'
  return wl >= 0.9 ? '#FF3B30' : wl >= 0.8 ? '#FF9500' : '#34C759'
})

// 最近告警 / 采集健康
const lastAlert = ref<Record<string, any> | null>(null)
const flowInfo = ref<Record<string, any> | null>(null)
const healthStatus = ref('')
const lastTask = ref<Record<string, any> | null>(null)
const healthLabel = computed(() => {
  if (lastTask.value && ['running'].includes(lastTask.value.status)) return '采集中'
  if (lastTask.value) return taskLabel(lastTask.value.status)
  return flowInfo.value ? '待采集' : '-'
})
const healthColor = computed(() => {
  const t = lastTask.value?.status
  if (t === 'running') return '#1F6FEB'
  if (t === 'success') return '#34C759'
  if (t === 'failed' || t === 'timeout') return '#FF3B30'
  if (healthStatus.value === 'error') return '#FF3B30'
  if (healthStatus.value === 'ok') return '#34C759'
  return '#909399'
})
const healthSub = computed(() => {
  const bits: string[] = []
  if (flowInfo.value) bits.push('流程 ' + flowInfo.value.flowCode)
  if (healthStatus.value) bits.push('数据源' + ({ ok: '正常', error: '异常' } as Record<string, string>)[healthStatus.value] || '')
  if (lastTask.value) bits.push('上次采集 ' + fmtTs(lastTask.value.triggeredAt))
  return bits.join(' · ') || '-'
})
function taskLabel(s: string): string {
  return ({ success: '正常', failed: '失败', timeout: '超时', running: '采集中' } as Record<string, string>)[s] || s
}
function statusLabel(s: string): string {
  return ({ active: '活动', processing: '处理中', recovered: '已恢复', closed: '已关闭' } as Record<string, string>)[s] || s
}
function statModeLabel(mode?: string): string {
  return ({ latest: '最新值', max: '最大值', min: '最小值', avg: '平均值', sum: '求和' } as Record<string, string>)[mode ?? ''] || (mode ?? '-')
}

function alertColor(lv: string): string {
  return lv === 'p1' ? '#FF3B30'
    : lv === 'p2' ? '#FF9500'
    : lv === 'p3' ? '#409EFF'
    : '#909399'
}

async function loadMetric() {
  // 详情接口返回 {latest, metric}包装，取内层 metric
  const r = (await http.get(`/api/v1/metrics/${code}`)) as unknown as Record<string, any>
  metric.value = (r && (r.metric ?? r)) as Record<string, any>
  // 最近告警
  const alerts = (await http.get('/api/v1/alerts', { params: { metricCode: code, pageSize: 1 } })) as any
  lastAlert.value = alerts.items?.[0] ?? null
  // 流程 + 数据源健康
  const flows = (await http.get('/api/v1/flows', { params: { metricCode: code, pageSize: 1 } })) as any
  const fl = flows.items?.[0]
  flowInfo.value = fl ?? null
  if (fl?.dataSourceId) {
    try {
      const ds = (await http.get(`/api/v1/data-sources/${fl.dataSourceId}`)) as any
      healthStatus.value = ds.healthStatus || ''
    } catch { /* 数据源不可见时忽略 */ }
  }
  // 最近任务
  try {
    const tasks = (await http.get('/api/v1/tasks', { params: { metricCode: code, pageSize: 1 } })) as any
    lastTask.value = tasks.items?.[0] ?? null
  } catch { /* 忽略 */ }
}

async function loadTrend() {
  loading.value = true
  try {
    const params: Record<string, unknown> = { granularity: granularity.value }
    if (rangeMode.value === 'custom' && customRange.value?.length === 2) {
      params.from = customRange.value[0]
      params.to = customRange.value[1]
    } else {
      params.days = days.value
    }
    trend.value = (await http.get(`/api/v1/metrics/${code}/trend`, { params })) as unknown as Record<string, any>
    requestAnimationFrame(renderChart)
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : String(e))
  } finally {
    loading.value = false
  }
}

async function loadForecast() {
  forecastLoading.value = true
  try {
    forecast.value = (await http.get(`/api/v1/metrics/${code}/forecast`, {
      params: { horizon: horizon.value },
      silent: true,
    })) as unknown as ForecastData
  } catch {
    forecast.value = null
  } finally {
    forecastLoading.value = false
    requestAnimationFrame(renderChart)
  }
}

function toggleCustom() {
  if (rangeMode.value === 'custom') {
    rangeMode.value = 'preset'
    loadTrend()
  } else {
    // 预填近 30 天
    const to = new Date(), from = new Date()
    from.setDate(from.getDate() - 29)
    const fmt = (d: Date) => d.toISOString().slice(0, 10)
    customRange.value = [fmt(from), fmt(to)]
    rangeMode.value = 'custom'
    loadTrend()
  }
}

function renderChart() {
  if (!chartRef.value || !trend.value || !metric.value) return
  if (!chart) chart = echarts.init(chartRef.value)
  const m = metric.value
  const s = series.value
  const isDay = granularity.value === 'day'
  const xData = s.map((p: any) => (isDay ? String(p.ts).slice(5, 10) : String(p.ts).slice(5, 16)))
  const values = s.map((p: any) => (p.value != null ? Number(p.value) : null))

  const markLine: any[] = []
  if (m.baseline != null) markLine.push({ yAxis: Number(m.baseline), lineStyle: { color: '#909399', type: 'dotted' }, label: { formatter: '基线 ' + m.baseline } })
  if (m.warnLine != null) markLine.push({ yAxis: Number(m.warnLine), lineStyle: { color: '#FF9500', type: 'dashed' }, label: { formatter: '预警 ' + m.warnLine } })
  if (m.dangerLine != null) markLine.push({ yAxis: Number(m.dangerLine), lineStyle: { color: '#FF3B30', type: 'dashed' }, label: { formatter: '危险 ' + m.dangerLine } })

  // 超线标注: 日粒度按 level 着色 + 区间背景; 日内按 judgeLevel 着色
  const overData: any[] = new Array(s.length).fill(null)
  const markArea: any[] = []
  let areaStart: number | null = null
  s.forEach((p: any, i: number) => {
    const lv = isDay ? p.judgeLevel : p.judgeLevel
    if (isDay && p.judgeLevel === 'missing') return
    if (lv === 'danger' || lv === 'warn') {
      overData[i] = { value: values[i], itemStyle: { color: lv === 'danger' ? '#FF3B30' : '#FF9500' } }
    } else if (values[i] != null) {
      overData[i] = { value: values[i] }
    }
    const over = isDay && (p.judgeLevel === 'warn' || p.judgeLevel === 'danger')
    if (over && areaStart == null) areaStart = i
    if (!over && areaStart != null) {
      markArea.push([{ xAxis: xData[areaStart], itemStyle: { color: 'rgba(255,149,0,0.08)' } }, { xAxis: xData[i - 1] }])
      areaStart = null
    }
  })
  if (areaStart != null) {
    markArea.push([{ xAxis: xData[areaStart], itemStyle: { color: 'rgba(255,149,0,0.08)' } }, { xAxis: xData[s.length - 1] }])
  }

  // 缺数标记（日粒度）
  const missingPoints: any[] = (trend.value.missingDates ?? []).map((d: string) => {
    const idx = xData.indexOf(d.slice(5, 10))
    return idx >= 0 ? { coord: [idx, 0], value: '缺', symbol: 'pin', symbolSize: 34, itemStyle: { color: '#FF3B30' }, label: { color: '#fff', fontSize: 11 } } : null
  }).filter(Boolean)

  // 预测叠加（日粒度且有预测结果时）: 虚线预测曲线 + 95% 置信区间 + ETA 标线
  const fc = isDay && showForecastLine.value ? forecast.value : null
  const forecastSeries: any[] = []
  let axisData: string[] = xData
  let histData: any[] = overData
  if (fc && (fc.curve?.length ?? 0) > 0) {
    const curve = fc.curve ?? []
    const pad = new Array(curve.length).fill(null)
    const lead = new Array(xData.length).fill(null)
    const lineData: any[] = [...lead, ...curve.map((p) => Number(p.value))]
    const lowerData: any[] = [...lead, ...curve.map((p) => Math.max(0, Number(p.lower)))]
    const bandData: any[] = [...lead, ...curve.map((p) => Math.max(0, Number(p.upper) - Number(p.lower)))]
    const anchor = values.length > 0 && values[values.length - 1] != null ? Number(values[values.length - 1]) : null
    if (anchor != null) {
      lineData[xData.length - 1] = anchor
      lowerData[xData.length - 1] = anchor
      bandData[xData.length - 1] = 0
    }
    axisData = [...xData, ...curve.map((p) => String(p.bizDate).slice(5, 10))]
    histData = [...overData, ...pad]
    const etaLines: any[] = []
    const pushEta = (d: unknown, label: string, color: string) => {
      if (!d) return
      const key = String(d).slice(5, 10)
      if (!axisData.includes(key)) return
      etaLines.push({ xAxis: key, lineStyle: { color, type: 'dotted' }, label: { formatter: label, fontSize: 10, color } })
    }
    pushEta(fc.etaWarnDate, '预警线 ETA', '#FF9500')
    pushEta(fc.etaDangerDate, '危险线 ETA', '#FF3B30')
    forecastSeries.push(
      {
        name: '95% 置信区间', type: 'line', data: bandData, stack: 'confidence-band',
        symbol: 'none', lineStyle: { opacity: 0 }, areaStyle: { color: 'rgba(123,97,255,0.16)' },
        silent: true, tooltip: { show: false },
      },
      {
        name: '置信下界', type: 'line', data: lowerData, stack: 'confidence-band',
        symbol: 'none', lineStyle: { opacity: 0 }, areaStyle: { opacity: 0 }, silent: true, tooltip: { show: false },
      },
      {
        name: fc.aiStatus === 'success' ? 'AI 修正' : '统计预测', type: 'line', data: lineData, connectNulls: false, showSymbol: false,
        lineStyle: { color: '#7B61FF', width: 2, type: 'dashed' }, itemStyle: { color: '#7B61FF' },
        markLine: { symbol: 'none', data: etaLines },
      },
    )
    // AI 修正对比（M8）: 叠加统计基线或 AI 建议曲线，按 bizDate 对齐主曲线坐标轴。
    const aiCurve = Array.isArray(fc.aiStatus ? (fc.aiStatus === 'oversized' ? fc.adjustedSnapshot?.curve : fc.baseSnapshot?.curve) : null)
      ? (fc.aiStatus === 'oversized' ? fc.adjustedSnapshot?.curve : fc.baseSnapshot?.curve) ?? []
      : []
    if ((fc.aiStatus === 'success' || fc.aiStatus === 'oversized') && aiCurve.length > 0) {
      const byDate = new Map<string, number>()
      aiCurve.forEach((p) => byDate.set(String(p.bizDate).slice(5, 10), Number(p.value)))
      const aiData: any[] = axisData.map((d) => (byDate.has(d) ? byDate.get(d) : null))
      forecastSeries.push({
        name: fc.aiStatus === 'oversized' ? 'AI 建议' : '统计预测(AI前)',
        type: 'line', data: aiData, connectNulls: false, showSymbol: false,
        lineStyle: { color: fc.aiStatus === 'oversized' ? '#FF9500' : '#909399', width: 1.5, type: 'dotted' },
        itemStyle: { color: fc.aiStatus === 'oversized' ? '#FF9500' : '#909399' },
      })
    }
  }

  const legendData = (forecast.value?.aiStatus === 'success' ? ['AI 修正'] : ['统计预测']).concat('95% 置信区间')
  if (forecastSeries.some((s) => s.name === '统计预测(AI前)')) legendData.push('统计预测(AI前)')
  if (forecastSeries.some((s) => s.name === 'AI 建议')) legendData.push('AI 建议')
  chart.setOption({
    grid: { left: 50, right: 24, top: 30, bottom: 40 },
    tooltip: { trigger: 'axis' },
    legend: { show: forecastSeries.length > 0, bottom: 0, data: legendData, textStyle: { fontSize: 11 } },
    xAxis: { type: 'category', data: axisData, axisLabel: { fontSize: 11 } },
    yAxis: { type: 'value', scale: true },
    dataZoom: axisData.length > 100 ? [{ type: 'slider', height: 18, bottom: 24 }] : [],
    series: [
      {
        name: m.name,
        type: 'line',
        data: histData,
        connectNulls: false,
        showSymbol: s.length <= 100,
        symbolSize: 6,
        lineStyle: { color: '#1F6FEB', width: 2 },
        itemStyle: { color: '#1F6FEB' },
        markLine: { symbol: 'none', data: markLine },
        markArea: { silent: true, data: markArea },
        markPoint: { data: missingPoints },
      },
      ...forecastSeries,
    ],
  }, true)
}

async function ensureForecast() {
  if (showForecastCard.value || showForecastLine.value) {
    if (!forecast.value) {
      await loadForecast()
    } else {
      requestAnimationFrame(renderChart)
    }
  } else {
    forecast.value = null
    requestAnimationFrame(renderChart)
  }
}

watch(showForecastCard, (value) => {
  localStorage.setItem('cap_metric_show_forecast_card', value ? '1' : '0')
  ensureForecast()
})
watch(showForecastLine, (value) => {
  localStorage.setItem('cap_metric_show_forecast_line', value ? '1' : '0')
  ensureForecast()
})
watch([granularity], () => { loadTrend() })
onMounted(async () => {
  const queryHorizon = Number(route.query.horizon)
  if (queryHorizon === 30 || queryHorizon === 90) horizon.value = queryHorizon
  await loadMetric()
  if (showForecastCard.value || showForecastLine.value) await loadForecast()
  await loadTrend()
  const onResize = () => chart?.resize()
  window.addEventListener('resize', onResize)
  onBeforeUnmount(() => {
    window.removeEventListener('resize', onResize)
    chart?.dispose()
  })
})
</script>

<style scoped>
.toolbar { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; }
.toolbar-title { font-size: 15px; font-weight: 600; display: flex; align-items: center; }
.back { margin-right: 4px; }
.toolbar-right { display: flex; gap: 8px; align-items: center; }
.switch-label { display: inline-flex; align-items: center; gap: 6px; font-size: 12px; color: #606266; }
.info-row { display: grid; grid-template-columns: repeat(5, 1fr); gap: 12px; margin-bottom: 12px; }
.info-card { background: #fff; border: 1px solid #ebeef5; border-radius: 6px; padding: 10px 14px; }
.info-label { font-size: 12px; color: #909399; }
.info-value { font-size: 22px; font-weight: 700; margin-top: 4px; font-variant-numeric: tabular-nums; }
.info-sub { font-size: 12px; color: #909399; margin-top: 4px; }
.link { color: #1F6FEB; cursor: pointer; }
.info-thr { display: flex; flex-direction: column; gap: 2px; margin-top: 4px; }
.thr { font-size: 14px; font-weight: 600; }
.thr.warn { color: #FF9500; }
.thr.danger { color: #FF3B30; }
.fc-card { background: #fff; border: 1px solid #ebeef5; border-radius: 6px; padding: 10px 14px 12px; margin-bottom: 12px; }
.fc-head { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 8px; }
.fc-sub { color: #c0c4cc; font-size: 12px; font-weight: 400; margin-left: 8px; }
.fc-row { display: grid; grid-template-columns: repeat(4, 1fr); gap: 10px 18px; margin: 8px 0; }
.fc-k { font-size: 12px; color: #909399; }
.fc-v { font-size: 16px; font-weight: 600; font-variant-numeric: tabular-nums; margin-top: 2px; }
.fc-explain { font-size: 12px; color: #606266; background: #f7f9fc; border-radius: 4px; padding: 8px 10px; }
.fc-ai { display: flex; align-items: center; gap: 10px; flex-wrap: wrap; margin-top: 8px; }
.fc-ai-meta { font-size: 12px; color: #909399; }
.fc-ai-note { font-size: 12px; color: #606266; background: #f7f9fc; border-radius: 4px; padding: 6px 10px; margin-top: 6px; }
.fc-ai-note.degrade { color: #b26a00; background: #fff7e6; border: 1px solid #ffe0a3; }
.chart-card { background: #fff; border: 1px solid #ebeef5; border-radius: 6px; padding: 8px 12px 12px; }
.chart-toolbar { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 8px; }
.chart-title { font-size: 13px; font-weight: 600; margin: 4px 0; }
.range-group { display: flex; align-items: center; gap: 8px; }
.chart { width: 100%; height: 380px; }
.stat-row { display: grid; grid-template-columns: repeat(3, 1fr) 1.5fr; gap: 12px; margin: 12px 0; }
.stat-card, .peak-card, .table-card { background: #fff; border: 1px solid #ebeef5; border-radius: 6px; padding: 10px 14px; }
.stat-label { font-size: 12px; color: #909399; display: flex; align-items: center; justify-content: space-between; }
.stat-value { font-size: 22px; font-weight: 700; margin-top: 4px; font-variant-numeric: tabular-nums; }
.stat-sub { font-size: 12px; color: #909399; margin-top: 4px; }
.peak-grid { display: flex; gap: 28px; margin-top: 6px; flex-wrap: wrap; }
.peak-item { display: flex; align-items: baseline; gap: 6px; }
.peak-k { font-size: 12px; color: #909399; }
.peak-v { font-size: 18px; font-weight: 700; font-variant-numeric: tabular-nums; }
.peak-d { font-size: 12px; color: #909399; }
.num { font-variant-numeric: tabular-nums; }
.muted { color: #c0c4cc; }
</style>
