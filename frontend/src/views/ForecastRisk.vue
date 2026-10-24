<template>
  <div class="page-card">
    <el-tabs v-model="activeTab" class="module-tabs" @tab-change="onTabChange">
      <el-tab-pane label="风险清单" name="risk" />
      <el-tab-pane label="事件因子" name="events" />
    </el-tabs>

    <div v-show="activeTab === 'risk'">
      <div class="toolbar">
      <div class="toolbar-title">
        预测风险
        <span class="sub">风险清单按风险分排序；窗口 ≤30 天使用 30 天预测，>30 天优先使用 90 天预测，90 天不可用时回退 30 天</span>
      </div>
      <div class="toolbar-right">
        <el-select v-model="days" style="width: 120px" @change="load">
          <el-option label="未来 7 天" :value="7" />
          <el-option label="未来 14 天" :value="14" />
          <el-option label="未来 30 天" :value="30" />
          <el-option label="未来 60 天" :value="60" />
          <el-option label="未来 90 天" :value="90" />
        </el-select>
        <el-input v-model="keyword" clearable placeholder="指标 / 系统" style="width: 180px" />
        <el-button :icon="Download" :disabled="items.length === 0" @click="exportCsv">导出 CSV</el-button>
        <el-button v-if="isAdmin" type="primary" :loading="recomputing" @click="openRecompute">重新计算</el-button>
        <el-button :icon="Refresh" circle @click="load" />
      </div>
    </div>

    <div class="run-row">
      <div class="run-card">
        <div class="run-label">最近预测批次</div>
        <div class="run-value">{{ run ? String(run.runDate).slice(0, 10) : '暂无' }}</div>
        <div class="run-sub">{{ run ? '模型 ' + run.modelVersion + ' · 数据截至 ' + String(run.dataEndDate).slice(0, 10) : '尚未执行统计预测' }}</div>
      </div>
      <div class="run-card">
        <div class="run-label">指标覆盖率</div>
        <div class="run-value">{{ coverageLabel }}</div>
        <div class="run-sub">{{ run ? `产出 ${run.metricsDone} / 跳过 ${run.metricsSkipped} / 合计 ${run.metricsTotal}` : '-' }}</div>
      </div>
      <div class="run-card">
        <div class="run-label">风险指标（{{ days }} 天窗口）</div>
        <div class="run-value" style="color: var(--cap-danger)">{{ items.length }}</div>
        <div class="run-sub">按风险分降序, 含预计到线时间</div>
      </div>
      <div class="run-card">
        <div class="run-label">批次状态</div>
        <div class="run-value">
          <el-tag :type="run?.status === 'success' ? 'success' : run?.status === 'running' ? 'info' : run ? 'danger' : 'info'" size="small">
            {{ runStatusLabel(run?.status) }}
          </el-tag>
        </div>
        <div class="run-sub">{{ run?.finishedAt ? '完成于 ' + fmtTs(run.finishedAt) : '-' }}</div>
      </div>
    </div>

    <el-table :data="filtered" v-loading="loading" border stripe max-height="560" empty-text="暂无风险指标" row-key="id">
      <el-table-column type="expand" width="42">
        <template #default="{ row }">
          <div class="ai-detail">
            <div class="ai-detail-head">
              <el-tag v-if="row.aiStatus" size="small" :type="aiStatusTag(row.aiStatus)">{{ aiStatusLabel(row.aiStatus) }}</el-tag>
              <span v-else class="muted">本轮无 AI 修正记录</span>
              <span v-if="row.aiModel" class="muted">模型 {{ row.aiModel }}</span>
              <span v-if="row.etaShiftDays != null" class="muted">ETA 偏移 {{ row.etaShiftDays }} 天</span>
              <span v-if="row.aiLatencyMs != null" class="muted">耗时 {{ row.aiLatencyMs }}ms</span>
            </div>
            <div v-if="row.adjustmentNote || row.aiRationale || row.aiError" class="ai-note">
              {{ row.adjustmentNote || row.aiRationale || row.aiError }}
            </div>
            <div v-if="row.suggestedEtaWarnDate || row.suggestedEtaDangerDate" class="ai-note">
              AI 建议：预警 {{ dateOnly(row.suggestedEtaWarnDate) }} / 危险 {{ dateOnly(row.suggestedEtaDangerDate) }}
            </div>
          </div>
        </template>
      </el-table-column>
      <el-table-column label="风险分" width="150" sortable :sort-method="(a: Row, b: Row) => (b.riskScore ?? 0) - (a.riskScore ?? 0)">
        <template #default="{ row }">
          <div class="risk-cell">
            <el-progress :percentage="Math.round((row.riskScore ?? 0) * 100)" :color="riskColor(row.riskScore)" :show-text="false" :stroke-width="8" />
            <span class="risk-num" :style="{ color: riskColor(row.riskScore) }">{{ fmtScore(row.riskScore) }}</span>
          </div>
        </template>
      </el-table-column>
      <el-table-column label="指标" min-width="190" show-overflow-tooltip>
        <template #default="{ row }">
          <el-button link type="primary" @click="openMetric(row)">{{ row.name || row.metricCode }}</el-button>
          <div class="code">{{ row.metricCode }}</div>
        </template>
      </el-table-column>
      <el-table-column prop="systemCode" label="系统" width="150" show-overflow-tooltip />
      <el-table-column prop="unit" label="单位" width="80" />
      <el-table-column label="状态" width="130">
        <template #default="{ row }">
          <el-tag :type="statusTag(row.status)" size="small">{{ statusLabel(row.status) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="模型" width="90">
        <template #default="{ row }">{{ modelLabel(row.modelType) }}</template>
      </el-table-column>
      <el-table-column label="AI" width="100">
        <template #default="{ row }">
          <el-tag v-if="row.aiStatus" size="small" :type="aiStatusTag(row.aiStatus)">{{ aiStatusLabel(row.aiStatus) }}</el-tag>
          <span v-else class="muted">-</span>
        </template>
      </el-table-column>
      <el-table-column label="数据质量" width="110" align="right">
        <template #default="{ row }">{{ fmtScore(row.dataQuality) }}</template>
      </el-table-column>
      <el-table-column label="置信度" width="100" align="right">
        <template #default="{ row }">{{ fmtScore(row.confidence) }}</template>
      </el-table-column>
      <el-table-column label="预计预警日" width="120">
        <template #default="{ row }">{{ dateOnly(row.etaWarnDate) }}</template>
      </el-table-column>
      <el-table-column label="预计危险日" width="120">
        <template #default="{ row }">{{ dateOnly(row.etaDangerDate) }}</template>
      </el-table-column>
      <el-table-column label="预测时窗" width="100">
        <template #default="{ row }">{{ row.horizonDays }} 天</template>
      </el-table-column>
      <el-table-column label="操作" width="130" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" size="small" @click="openMetric(row)">预测曲线</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-alert
      v-if="showQualityHint"
      class="hint"
      type="warning"
      :closable="false"
      show-icon
      title="部分指标因数据不足或质量分低于 0.8 未输出确定性 ETA, 请先补齐历史数据并完成质量检查。"
    />

    <el-dialog v-model="recomputeVisible" title="重新计算预测" width="600px">
      <el-form label-width="90px" v-loading="optionsLoading">
        <el-form-item label="业务日期">
          <el-date-picker v-model="recomputeDate" type="date" value-format="YYYY-MM-DD" :clearable="false" style="width: 180px" />
        </el-form-item>
        <el-form-item label="范围">
          <el-radio-group v-model="recomputeMode">
            <el-radio-button value="all">全部指标</el-radio-button>
            <el-radio-button value="partial">部分指标 / 系统</el-radio-button>
          </el-radio-group>
        </el-form-item>
        <template v-if="recomputeMode === 'partial'">
          <el-form-item label="业务系统">
            <el-select v-model="recomputeSystems" multiple filterable clearable collapse-tags placeholder="不选表示全部系统" style="width: 100%">
              <el-option v-for="s in systemOptions" :key="s.systemCode" :label="`${s.name}（${s.systemCode}）`" :value="s.systemCode" />
            </el-select>
          </el-form-item>
          <el-form-item label="指标">
            <el-select v-model="recomputeMetrics" multiple filterable clearable collapse-tags placeholder="不选表示所选系统全部指标" style="width: 100%">
              <el-option v-for="m in filteredMetricOptions" :key="m.metricCode" :label="`${m.name}（${m.metricCode}）`" :value="m.metricCode" />
            </el-select>
          </el-form-item>
          <div class="dialog-hint">系统和指标可组合，取交集；至少选择一项。</div>
        </template>
      </el-form>
      <template #footer>
        <el-button @click="recomputeVisible = false">取消</el-button>
        <el-button type="primary" :loading="recomputing" @click="submitRecompute">开始重算</el-button>
      </template>
    </el-dialog>
    </div>

    <ForecastEvents v-if="activeTab === 'events'" embedded />
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { Download, Refresh } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { http } from '@/api/http'
import { fmtTs } from '@/utils/format'
import { useAuthStore } from '@/stores/auth'
import { useQueryTab } from '@/composables/useQueryTab'
import ForecastEvents from '@/views/ForecastEvents.vue'

interface CurvePoint {
  bizDate: string
  value: number
  lower: number
  upper: number
}
interface Row {
  id: number
  runId: number
  metricCode: string
  name?: string
  systemCode?: string
  unit?: string
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
  curve: CurvePoint[] | null
  aiStatus?: string | null
  aiModel?: string | null
  aiRationale?: string | null
  aiError?: string | null
  adjustmentNote?: string | null
  advisoryOnly?: boolean
  etaShiftDays?: number | null
  aiLatencyMs?: number | null
  suggestedEtaWarnDate?: string | null
  suggestedEtaDangerDate?: string | null
}
interface Run {
  id: number
  runDate: string
  dataEndDate: string
  modelVersion: string
  status: string
  metricsTotal: number
  metricsDone: number
  metricsSkipped: number
  finishedAt: string | null
  scoped?: boolean
  scopeMetricsTotal?: number
  scopeMetricsDone?: number
  scopeMetricsSkipped?: number
}
interface SystemOption {
  systemCode: string
  name: string
}
interface MetricOption {
  metricCode: string
  name: string
  systemCode: string
}

const router = useRouter()
const auth = useAuthStore()
const isAdmin = computed(() => auth.isAdmin)
const { activeTab, onTabChange } = useQueryTab(['risk', 'events'] as const, 'risk')

const days = ref(30)
const keyword = ref('')
const loading = ref(false)
const recomputing = ref(false)
const items = ref<Row[]>([])
const run = ref<Run | null>(null)
const recomputeVisible = ref(false)
const recomputeMode = ref<'all' | 'partial'>('all')
const recomputeDate = ref(todayString())
const recomputeSystems = ref<string[]>([])
const recomputeMetrics = ref<string[]>([])
const optionsLoading = ref(false)
const systemOptions = ref<SystemOption[]>([])
const metricOptions = ref<MetricOption[]>([])

const filtered = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  if (!kw) return items.value
  return items.value.filter(
    (r) =>
      r.metricCode.toLowerCase().includes(kw) ||
      (r.name ?? '').toLowerCase().includes(kw) ||
      (r.systemCode ?? '').toLowerCase().includes(kw),
  )
})

const filteredMetricOptions = computed(() => {
  if (recomputeSystems.value.length === 0) return metricOptions.value
  const allowed = new Set(recomputeSystems.value)
  return metricOptions.value.filter((metric) => allowed.has(metric.systemCode))
})

watch(recomputeSystems, () => {
  if (recomputeSystems.value.length === 0) return
  const allowed = new Set(filteredMetricOptions.value.map((metric) => metric.metricCode))
  recomputeMetrics.value = recomputeMetrics.value.filter((code) => allowed.has(code))
})

function todayString(): string {
  const now = new Date()
  const pad = (value: number) => String(value).padStart(2, '0')
  return `${now.getFullYear()}-${pad(now.getMonth() + 1)}-${pad(now.getDate())}`
}

const coverageLabel = computed(() => {
  const r = run.value
  if (!r || r.metricsTotal === 0) return '-'
  return ((r.metricsDone / r.metricsTotal) * 100).toFixed(1) + '%'
})

const showQualityHint = computed(() =>
  items.value.some((r) => r.status === 'insufficient_data' || r.status === 'insufficient_quality') ||
  (run.value?.metricsSkipped ?? 0) > 0,
)

async function load() {
  loading.value = true
  try {
    const data = (await http.get('/api/v1/forecasts/risk', { params: { days: days.value } })) as unknown as {
      run: Run | null
      items: Row[]
      count: number
    }
    run.value = data.run ?? null
    items.value = data.items ?? []
  } catch {
    run.value = null
    items.value = []
  } finally {
    loading.value = false
  }
}

async function openRecompute() {
  recomputeVisible.value = true
  recomputeDate.value = todayString()
  recomputeMode.value = 'all'
  recomputeSystems.value = []
  recomputeMetrics.value = []
  if (systemOptions.value.length === 0 || metricOptions.value.length === 0) {
    await loadRecomputeOptions()
  }
}

async function loadRecomputeOptions() {
  optionsLoading.value = true
  try {
    const systems = (await http.get('/api/v1/systems', { params: { page: 1, pageSize: 200 } })) as unknown as { items: SystemOption[] }
    systemOptions.value = systems.items ?? []
    const first = (await http.get('/api/v1/metrics', { params: { page: 1, pageSize: 200 } })) as unknown as { items: MetricOption[]; total: number }
    const all = [...(first.items ?? [])]
    const total = first.total ?? all.length
    for (let page = 2; all.length < total; page += 1) {
      const data = (await http.get('/api/v1/metrics', { params: { page, pageSize: 200 } })) as unknown as { items: MetricOption[] }
      if (!data.items?.length) break
      all.push(...data.items)
    }
    metricOptions.value = all
  } catch {
    systemOptions.value = []
    metricOptions.value = []
  } finally {
    optionsLoading.value = false
  }
}

async function submitRecompute() {
  const systemCodes = recomputeMode.value === 'partial' ? recomputeSystems.value : []
  const metricCodes = recomputeMode.value === 'partial' ? recomputeMetrics.value : []
  if (recomputeMode.value === 'partial' && systemCodes.length === 0 && metricCodes.length === 0) {
    ElMessage.warning('请至少选择一个业务系统或指标')
    return
  }
  recomputing.value = true
  try {
    const result = (await http.post('/api/v1/forecasts/recompute', {
      date: recomputeDate.value,
      systemCodes,
      metricCodes,
    })) as unknown as Run
    const done = result.scopeMetricsDone ?? result.metricsDone
    const skipped = result.scopeMetricsSkipped ?? result.metricsSkipped
    ElMessage.success(`预测已重新计算：${done} 个指标产出结果, ${skipped} 个跳过`)
    recomputeVisible.value = false
    await load()
  } catch { /* 拦截器已提示 */ } finally {
    recomputing.value = false
  }
}

function exportCsv() {
  const header = [
    '指标编码', '指标名称', '系统', '单位', '时窗(天)', '状态', '模型', '风险分', '置信度', '数据质量',
    '预计预警日', '预计危险日', 'AI状态', 'AI模型', 'AI理由', 'AI建议预警日', 'AI建议危险日', '解释',
  ]
  const lines = [header.join(',')]
  for (const r of filtered.value) {
    lines.push([
      r.metricCode, r.name ?? '', r.systemCode ?? '', r.unit ?? '',
      String(r.horizonDays), statusLabel(r.status), modelLabel(r.modelType),
      fmtScore(r.riskScore), fmtScore(r.confidence), fmtScore(r.dataQuality),
      dateOnly(r.etaWarnDate), dateOnly(r.etaDangerDate),
      r.aiStatus ? aiStatusLabel(r.aiStatus) : '',
      r.aiModel ?? '',
      csvCell(r.adjustmentNote || r.aiRationale || r.aiError || ''),
      dateOnly(r.suggestedEtaWarnDate), dateOnly(r.suggestedEtaDangerDate),
      csvCell(r.explanation),
    ].join(','))
  }
  const blob = new Blob(['\ufeff' + lines.join('\n')], { type: 'text/csv;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `forecast-risk-${days.value}d.csv`
  a.click()
  URL.revokeObjectURL(url)
}

function csvCell(v: string): string {
  return '"' + String(v ?? '').replace(/"/g, '""') + '"'
}

function openMetric(row: Row) {
  router.push({ name: 'metric-detail', params: { code: row.metricCode }, query: { horizon: String(row.horizonDays) } })
}

function fmtScore(v: number | null | undefined): string {
  return v == null ? '-' : Number(v).toFixed(3)
}
function dateOnly(v: string | null | undefined): string {
  return v ? String(v).slice(0, 10) : '-'
}
function riskColor(v: number | null | undefined): string {
  const n = v ?? 0
  return n >= 0.8 ? '#FF3B30' : n >= 0.6 ? '#FF9500' : n >= 0.4 ? '#1F6FEB' : '#909399'
}
function statusLabel(s: string | undefined): string {
  return ({
    success: '命中风险', never: '窗口内未到线', insufficient_data: '数据不足',
    insufficient_quality: '质量不足',
  } as Record<string, string>)[s ?? ''] ?? (s ?? '-')
}
function statusTag(s: string): string {
  return ({ success: 'danger', never: 'success', insufficient_data: 'info', insufficient_quality: 'warning' } as Record<string, string>)[s] ?? 'info'
}
function modelLabel(m: string | undefined): string {
  return ({ linear: '线性', growth: '增长率', ai_adjusted: 'AI 修正' } as Record<string, string>)[m ?? ''] ?? (m ?? '-')
}
function aiStatusLabel(s: string | null | undefined): string {
  return ({ success: 'AI 修正', oversized: 'AI 建议', invalid: 'AI 降级', disabled: 'AI 未启用' } as Record<string, string>)[s ?? ''] ?? (s ?? '-')
}
function aiStatusTag(s: string | null | undefined): string {
  return ({ success: 'success', oversized: 'warning', invalid: 'danger', disabled: 'info' } as Record<string, string>)[s ?? ''] ?? 'info'
}
function runStatusLabel(s: string | undefined): string {
  return ({ success: '成功', running: '运行中', failed: '失败' } as Record<string, string>)[s ?? ''] ?? '暂无'
}

onMounted(load)
</script>

<style scoped>
.module-tabs { margin-bottom: 12px; }
.toolbar { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; flex-wrap: wrap; gap: 8px; }
.toolbar-title { font-size: 15px; font-weight: 600; }
.sub { color: #c0c4cc; font-size: 12px; font-weight: 400; margin-left: 10px; }
.toolbar-right { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.run-row { display: grid; grid-template-columns: repeat(4, 1fr); gap: 12px; margin-bottom: 12px; }
.run-card { background: #fff; border: 1px solid #ebeef5; border-radius: 6px; padding: 10px 14px; }
.run-label { font-size: 12px; color: #909399; }
.run-value { font-size: 21px; font-weight: 700; margin-top: 4px; font-variant-numeric: tabular-nums; }
.run-sub { font-size: 12px; color: #909399; margin-top: 4px; }
.risk-cell { display: flex; align-items: center; gap: 8px; }
.risk-cell :deep(.el-progress) { flex: 1; }
.risk-num { font-size: 12px; font-variant-numeric: tabular-nums; width: 42px; text-align: right; }
.code { font-size: 12px; color: #909399; }
.muted { color: #c0c4cc; }
.ai-detail { display: flex; flex-direction: column; gap: 6px; padding: 4px 8px; }
.ai-detail-head { display: flex; align-items: center; gap: 12px; flex-wrap: wrap; font-size: 12px; color: #909399; }
.ai-note { font-size: 12px; color: #606266; background: #f7f9fc; border-radius: 4px; padding: 6px 10px; white-space: pre-wrap; }
.dialog-hint { margin-left: 90px; font-size: 12px; color: #909399; }
.hint { margin-top: 12px; }
</style>
