<template>
  <div class="page-card" v-loading="loading">
    <div class="toolbar">
      <div class="toolbar-title">
        <el-button link :icon="ArrowLeft" @click="$router.push('/metrics')" class="back" />
        <span v-if="node">{{ node.name }}（{{ node.metricCode }}）</span>
        <el-tag v-if="node" size="small" :type="node.metricKind === 'aggregate' ? 'danger' : 'warning'" style="margin-left: 8px">
          {{ node.metricKind === 'aggregate' ? '汇总指标' : '子指标' }}
        </el-tag>
      </div>
      <div class="toolbar-right">
        <el-radio-group v-model="granularity" size="small" @change="loadModules">
          <el-radio-button value="day">日粒度</el-radio-button>
          <el-radio-button value="rt">日内数据</el-radio-button>
        </el-radio-group>
      </div>
    </div>

    <!-- 概览 -->
    <div class="info-row" v-if="modulesData">
      <div class="info-card">
        <div class="info-label">统计模块</div>
        <div class="info-value">{{ modulesData.moduleCount }}</div>
        <div class="info-sub">按模块标识归组</div>
      </div>
      <div class="info-card">
        <div class="info-label">{{ node?.metricKind === 'aggregate' ? '子指标' : '统计模块指标' }}</div>
        <div class="info-value">{{ modulesData.childMetricCount }}</div>
        <div class="info-sub">参与计算/展示的叶子指标</div>
      </div>
      <div class="info-card" v-if="node?.metricKind === 'aggregate'">
        <div class="info-label">汇总口径</div>
        <div class="info-sub" style="margin-top: 6px">主模块: {{ node.mainModule || '首个模块' }}(日值/阈值告警口径); 模块按统计方式合并, 配置权重指标时按加权均值计算</div>
      </div>
      <div class="info-card">
        <div class="info-label">时间范围</div>
        <div class="info-value" style="font-size: 15px; line-height: 1.4">{{ rangeLabel }}</div>
        <div class="info-sub">{{ granularity === 'day' ? '日粒度 (stat_value)' : '日内按同一时间戳对齐' }}</div>
      </div>
    </div>

    <!-- 统计模块卡片 -->
    <el-empty v-if="!loading && modulesData && modulesData.moduleCount === 0"
      description="暂无统计模块, 请在指标树中为该节点添加子指标/统计模块" />
    <div class="module-grid">
      <div class="module-card" v-for="m in modulesData?.modules ?? []" :key="m.module">
        <div class="module-head">
          <div class="module-title">
            <span class="module-name">{{ m.name || m.module }}</span>
            <el-tag size="small" :type="aggTagType(m.aggMode)">{{ aggLabel(m.aggMode) }}</el-tag>
            <span class="module-unit" v-if="m.unit && m.unit !== '-'">{{ m.unit }}</span>
          </div>
          <div class="module-latest">
            <span class="latest-value">{{ fmtNum(m.latest) }}</span>
            <span class="latest-ts">{{ granularity === 'day' ? dateOnly(m.latestTs) : fmtTs(m.latestTs) }}</span>
          </div>
        </div>
        <div class="module-chart" :ref="(el) => setChartRef(m.module, el)" />
        <div class="module-stats">
          <div class="ms"><span class="ms-k">最大</span><span class="ms-v">{{ fmtNum(m.stats?.max) }}</span></div>
          <div class="ms"><span class="ms-k">最小</span><span class="ms-v">{{ fmtNum(m.stats?.min) }}</span></div>
          <div class="ms"><span class="ms-k">平均</span><span class="ms-v">{{ fmtNum(m.stats?.avg) }}</span></div>
          <div class="ms"><span class="ms-k">点数</span><span class="ms-v">{{ m.stats?.count ?? 0 }}</span></div>
        </div>
        <div class="module-children" v-if="m.children?.length">
          <el-tooltip v-for="ch in m.children" :key="ch.code"
            :content="ch.code + (ch.statMode ? ' · 统计: ' + statModeLabel(ch.statMode) : '') + (ch.weighted ? ' · 加权' : '')"
            placement="top">
            <div class="child-row" @click="goMetric(ch.code)">
              <span class="child-name">{{ ch.name }}{{ ch.weighted ? ' ×权重' : '' }}</span>
              <span class="child-val">{{ fmtNum(ch.latest) }}<em v-if="ch.unit && ch.unit !== '-'">{{ ch.unit }}</em></span>
            </div>
          </el-tooltip>
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
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
const node = ref<Record<string, any> | null>(null)
const modulesData = ref<Record<string, any> | null>(null)
const granularity = ref<'day' | 'rt'>('day')

const chartRefs = new Map<string, HTMLElement>()
const charts = new Map<string, echarts.ECharts>()

function setChartRef(module: string, el: unknown) {
  if (el instanceof HTMLElement) {
    chartRefs.set(module, el)
  }
}

const rangeLabel = computed(() => {
  const s = modulesData.value?.modules
  if (!s?.length) return '-'
  const first = s[0]?.series?.[0]?.ts
  const arr = s[0]?.series ?? []
  const last = arr.length ? arr[arr.length - 1].ts : null
  if (!first) return '-'
  const f = (t: string) => (granularity.value === 'day' ? String(t).slice(0, 10) : String(t).slice(0, 16).replace('T', ' '))
  return f(first) + ' ~ ' + (last ? f(last) : '-')
})

function fmtNum(v: unknown): string {
  if (v == null || v === '') return '-'
  const n = Number(v)
  return isFinite(n) ? n.toLocaleString('zh-CN', { maximumFractionDigits: 4 }) : String(v)
}
function dateOnly(ts: unknown): string {
  return ts ? String(ts).slice(0, 10) : '-'
}
function statModeLabel(mode: string): string {
  return ({ latest: '最新值', max: '最大值', min: '最小值', avg: '平均值', sum: '求和' } as Record<string, string>)[mode] || mode || '-'
}

function aggLabel(mode: string): string {
  return ({ self: '直接值', sum: '求和', max: '取最大', min: '取最小', avg: '取均值', weighted_avg: '加权均值' } as Record<string, string>)[mode] || mode
}
function aggTagType(mode: string): string {
  return ({ self: 'info', sum: 'success', max: 'warning', min: 'info', avg: '', weighted_avg: 'danger' } as Record<string, string>)[mode] || 'info'
}
function goMetric(metricCode: string) {
  router.push(`/metrics/${metricCode}/detail`)
}

async function loadNode() {
  const r = (await http.get(`/api/v1/metrics/${code}`)) as unknown as Record<string, any>
  node.value = (r && (r.metric ?? r)) as Record<string, any>
  const kind = node.value?.metricKind
  if (kind !== 'group' && kind !== 'aggregate') {
    ElMessage.warning('该指标不是汇总指标/子指标')
  }
}

async function loadModules() {
  loading.value = true
  try {
    modulesData.value = (await http.get(`/api/v1/metrics/${code}/modules`, { params: { granularity: granularity.value, days: 30 } })) as unknown as Record<string, any>
    await nextTick()
    renderAllCharts()
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : String(e))
  } finally {
    loading.value = false
  }
}

function renderAllCharts() {
  for (const m of modulesData.value?.modules ?? []) {
    renderChart(m)
  }
}

function renderChart(m: Record<string, any>) {
  const el = chartRefs.get(m.module)
  if (!el) return
  if (!charts.has(m.module)) {
    charts.set(m.module, echarts.init(el))
  }
  const chart = charts.get(m.module)!
  const series = m.series ?? []
  const xData = series.map((p: any) => (granularity.value === 'day' ? String(p.ts).slice(5, 10) : String(p.ts).slice(5, 16).replace('T', ' ')))
  const values = series.map((p: any) => p.value)
  chart.setOption({
    grid: { left: 55, right: 16, top: 16, bottom: 28 },
    tooltip: { trigger: 'axis' },
    xAxis: { type: 'category', data: xData, axisLabel: { fontSize: 10 } },
    yAxis: { type: 'value', scale: true, axisLabel: { fontSize: 10 } },
    series: [{
      name: m.name,
      type: 'line',
      data: values,
      smooth: true,
      showSymbol: series.length <= 60,
      symbolSize: 5,
      lineStyle: { color: '#1F6FEB', width: 2 },
      itemStyle: { color: '#1F6FEB' },
      areaStyle: { color: 'rgba(31,111,235,0.08)' },
    }],
  }, true)
}

function onResize() {
  for (const ch of charts.values()) ch.resize()
}

watch([granularity], () => { loadModules() })
onMounted(async () => {
  await loadNode()
  await loadModules()
  window.addEventListener('resize', onResize)
})
onBeforeUnmount(() => {
  window.removeEventListener('resize', onResize)
  for (const ch of charts.values()) ch.dispose()
  charts.clear()
})
</script>

<style scoped>
.toolbar { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; }
.toolbar-title { font-size: 15px; font-weight: 600; display: flex; align-items: center; }
.back { margin-right: 4px; }
.toolbar-right { display: flex; gap: 8px; align-items: center; }
.info-row { display: grid; grid-template-columns: repeat(4, 1fr); gap: 12px; margin-bottom: 12px; }
.info-card { background: #fff; border: 1px solid #ebeef5; border-radius: 6px; padding: 10px 14px; }
.info-label { font-size: 12px; color: #909399; }
.info-value { font-size: 22px; font-weight: 700; margin-top: 4px; font-variant-numeric: tabular-nums; }
.info-sub { font-size: 12px; color: #909399; margin-top: 4px; }
.module-grid { display: grid; grid-template-columns: repeat(2, 1fr); gap: 12px; }
.module-card { background: #fff; border: 1px solid #ebeef5; border-radius: 6px; padding: 12px 14px; }
.module-head { display: flex; align-items: flex-start; justify-content: space-between; margin-bottom: 4px; }
.module-title { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.module-name { font-size: 14px; font-weight: 600; }
.module-unit { font-size: 12px; color: #909399; }
.module-latest { text-align: right; }
.latest-value { font-size: 20px; font-weight: 700; font-variant-numeric: tabular-nums; }
.latest-ts { font-size: 12px; color: #909399; margin-left: 6px; }
.module-chart { width: 100%; height: 220px; }
.module-stats { display: flex; gap: 18px; padding: 6px 0 4px; flex-wrap: wrap; }
.ms { display: flex; flex-direction: column; }
.ms-k { font-size: 12px; color: #909399; }
.ms-v { font-size: 14px; font-weight: 600; font-variant-numeric: tabular-nums; }
.module-children { display: flex; flex-direction: column; gap: 2px; padding-top: 6px; border-top: 1px dashed #ebeef5; }
.child-row { display: flex; align-items: center; justify-content: space-between; gap: 10px; cursor: pointer; padding: 3px 6px; border-radius: 4px; }
.child-row:hover { background: #f5f7fa; }
.child-name { font-size: 12px; color: #606266; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.child-val { font-size: 12px; font-weight: 600; font-variant-numeric: tabular-nums; white-space: nowrap; }
.child-val em { font-style: normal; color: #909399; font-weight: 400; margin-left: 2px; }
</style>
