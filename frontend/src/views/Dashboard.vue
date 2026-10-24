<template>
  <div>
    <el-row :gutter="12" class="stat-row">
      <el-col :span="4" v-for="c in cards" :key="c.label">
        <div class="page-card stat-card" :style="{ borderTop: `3px solid ${c.color}` }">
          <div class="stat-label">{{ c.label }}</div>
          <div class="stat-value" :style="{ color: c.color }">{{ c.value }}</div>
          <div class="stat-desc" @click="c.to && router.push(c.to)" :class="{ clickable: !!c.to }">{{ c.desc }}</div>
        </div>
      </el-col>
    </el-row>
    <el-row :gutter="12">
      <el-col :span="12">
        <div class="page-card">
          <div class="chart-title">TOP 10 高水位指标 <span class="chart-sub">（{{ bizDate }} 业务日, 点击下钻指标详情）</span></div>
          <div ref="barRef" class="chart" />
        </div>
      </el-col>
      <el-col :span="12">
        <div class="page-card">
          <div class="chart-title">平台水位趋势（近 30 日） <span class="chart-sub">日粒度平均 / 最高水位</span></div>
          <div ref="lineRef" class="chart" />
        </div>
      </el-col>
    </el-row>
  </div>
</template>

<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import * as echarts from 'echarts'
import { http } from '@/api/http'

interface TopRow {
  metricCode: string
  name: string
  systemCode: string
  systemName: string
  statValue: number | null
  waterLevel: number | null
  level: string
}
interface Overview {
  systemCount: number
  metricCount: number
  activeMetricCount: number
  overWarnCount: number
  overDangerCount: number
  alertNewToday: number
  alertActive: number
  collectFailToday: number
  topWater: TopRow[]
  bizDate: string
}

const router = useRouter()
const overview = ref<Overview | null>(null)
const bizDate = ref('-')
const activeByLevel = ref<Record<string, number>>({})

const cards = ref([
  { label: '在管业务系统', value: '-', desc: '', color: 'var(--cap-primary)', to: '/systems' as string | null },
  { label: '活跃指标', value: '-', desc: '', color: 'var(--cap-normal)', to: '/metrics' as string | null },
  { label: '超预警 / 超危险', value: '-', desc: '最近业务日', color: 'var(--cap-warn)', to: null },
  { label: '活动告警', value: '-', desc: '', color: 'var(--cap-danger)', to: '/alerts' as string | null },
  { label: '今日新增 / 采集失败', value: '-', desc: '', color: 'var(--cap-warn)', to: '/tasks' as string | null },
])

const barRef = ref<HTMLElement>()
const lineRef = ref<HTMLElement>()
let barChart: echarts.ECharts | null = null
let lineChart: echarts.ECharts | null = null

function levelColor(l: string) {
  if (l === 'danger') return '#ff3b30'
  if (l === 'warn') return '#ff9500'
  return '#34c759'
}

async function loadOverview() {
  try {
    const d = (await http.get('/api/v1/dashboard/overview')) as unknown as Overview
    overview.value = d
    bizDate.value = d.bizDate
    cards.value = [
      { label: '在管业务系统', value: String(d.systemCount), desc: '点击进入业务系统', color: 'var(--cap-primary)', to: '/systems' },
      { label: '活跃指标', value: String(d.activeMetricCount), desc: `共 ${d.metricCount} 个指标`, color: 'var(--cap-normal)', to: '/metrics' },
      { label: '超预警 / 超危险', value: `${d.overWarnCount} / ${d.overDangerCount}`, desc: `业务日 ${d.bizDate}`, color: 'var(--cap-warn)', to: null },
      { label: '活动告警', value: String(d.alertActive), desc: `P1 ${activeByLevel.value['p1'] ?? 0} · P2 ${activeByLevel.value['p2'] ?? 0} · P3 ${activeByLevel.value['p3'] ?? 0} · P4 ${activeByLevel.value['p4'] ?? 0}`, color: 'var(--cap-danger)', to: '/alerts' },
      { label: '今日新增 / 采集失败', value: `${d.alertNewToday} / ${d.collectFailToday}`, desc: '点击进入任务监控', color: 'var(--cap-warn)', to: '/tasks' },
    ]
  } catch {
    cards.value[2] = { ...cards.value[2], desc: '加载失败' }
  }
}

async function loadAlertStats() {
  try {
    const d = (await http.get('/api/v1/alerts/stats', { params: { window: 30 } })) as unknown as { activeByLevel?: Record<string, number> }
    if (d.activeByLevel) activeByLevel.value = d.activeByLevel
  } catch { /* 非关键 */ }
}

function renderBar(d: Overview) {
  if (!barRef.value) return
  barChart = echarts.init(barRef.value)
  const items = d.topWater.filter(t => t.waterLevel != null).slice(0, 10).reverse()
  barChart.setOption({
    tooltip: {
      trigger: 'axis', axisPointer: { type: 'shadow' },
      valueFormatter: (v: number) => `${(Number(v) * 100).toFixed(1)}%`,
    },
    grid: { left: 130, right: 60, top: 30, bottom: 24 },
    xAxis: { type: 'value', max: 1, axisLabel: { formatter: (v: number) => `${Math.round(v * 100)}%`, fontSize: 10 } },
    yAxis: {
      type: 'category',
      data: items.map(t => `${t.name}（${t.systemName}）`),
      axisLabel: { fontSize: 11, width: 120, overflow: 'truncate' },
    },
    series: [{
      type: 'bar',
      data: items.map(t => ({ value: t.waterLevel as number, itemStyle: { color: levelColor(t.level) } })),
      barMaxWidth: 16,
      label: { show: true, position: 'right', formatter: (p: { value: number }) => `${(p.value * 100).toFixed(1)}%`, fontSize: 10 },
      markLine: {
        silent: true, symbol: 'none',
        lineStyle: { type: 'dashed' },
        data: [
          { xAxis: 0.8, lineStyle: { color: '#ff9500' }, label: { formatter: '预警 80%', fontSize: 10 } },
          { xAxis: 0.9, lineStyle: { color: '#ff3b30' }, label: { formatter: '危险 90%', fontSize: 10 } },
        ],
      },
    }],
  })
  barChart.on('click', (params: { name?: string; dataIndex?: number }) => {
    const t = items[params.dataIndex ?? -1]
    if (t) router.push(`/metrics/${t.metricCode}/detail`)
  })
}

async function loadTrend() {
  try {
    const d = (await http.get('/api/v1/dashboard/trend', { params: { days: 30 } })) as unknown as {
      dates: string[]; avg: (number | null)[]; max: (number | null)[]
    }
    if (!lineRef.value) return
    lineChart = echarts.init(lineRef.value)
    lineChart.setOption({
      tooltip: {
        trigger: 'axis',
        valueFormatter: (v: number) => (v == null ? '-' : `${(Number(v) * 100).toFixed(1)}%`),
      },
      legend: { data: ['平均水位', '最高水位'], right: 0, top: 0, textStyle: { fontSize: 11 } },
      grid: { left: 50, right: 20, top: 30, bottom: 28 },
      xAxis: { type: 'category', data: d.dates, axisLabel: { fontSize: 10, interval: 4 } },
      yAxis: { type: 'value', max: 1, axisLabel: { formatter: (v: number) => `${Math.round(v * 100)}%`, fontSize: 10 } },
      series: [
        {
          name: '平均水位', type: 'line', smooth: true, connectNulls: true, data: d.avg,
          itemStyle: { color: '#1f6feb' }, lineStyle: { width: 2 },
        },
        {
          name: '最高水位', type: 'line', smooth: true, connectNulls: true, data: d.max,
          itemStyle: { color: '#ff9500' }, lineStyle: { width: 1.5, type: 'dashed' },
          markLine: {
            silent: true, symbol: 'none',
            data: [
              { yAxis: 0.8, lineStyle: { color: '#ff9500', type: 'dashed' } },
              { yAxis: 0.9, lineStyle: { color: '#ff3b30', type: 'dashed' } },
            ],
            label: { show: false },
          },
        },
      ],
    })
  } catch { /* 非关键 */ }
}

function onResize() {
  barChart?.resize()
  lineChart?.resize()
}

onMounted(async () => {
  await Promise.all([loadOverview(), loadAlertStats(), loadTrend()])
  renderBar(overview.value as Overview)
  window.addEventListener('resize', onResize)
})
onBeforeUnmount(() => {
  window.removeEventListener('resize', onResize)
  barChart?.dispose()
  lineChart?.dispose()
})
</script>

<style scoped>
.stat-row { margin-bottom: 12px; }
.stat-card { border-radius: 8px; }
.stat-label { color: #909399; font-size: 13px; }
.stat-value { font-size: 26px; font-weight: 700; margin: 6px 0 2px; }
.stat-desc { color: #c0c4cc; font-size: 12px; min-height: 16px; }
.stat-desc.clickable { cursor: pointer; }
.stat-desc.clickable:hover { color: var(--cap-primary); }
.chart-title { font-size: 14px; font-weight: 600; margin-bottom: 8px; }
.chart-sub { color: #c0c4cc; font-size: 12px; font-weight: 400; }
.chart { height: 320px; }
</style>
