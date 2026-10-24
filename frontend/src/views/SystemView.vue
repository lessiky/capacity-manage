<template>
  <div class="page-card">
    <div class="toolbar">
      <div class="toolbar-title">系统视图<span class="sub">最近业务日（昨日）水位 · 点击卡片下钻指标详情</span></div>
      <div class="toolbar-right">
        <el-select v-model="code" filterable placeholder="选择业务系统" style="width: 260px" @change="load">
          <el-option v-for="s in systems" :key="s.systemCode" :label="s.name + ' (' + s.systemCode + ')'" :value="s.systemCode" />
        </el-select>
        <el-button :icon="Refresh" circle @click="load" />
      </div>
    </div>

    <el-alert v-if="!code" type="info" :closable="false" title="请选择业务系统，查看其全部活跃指标的容量水位" />

    <div v-else v-loading="loading">
      <el-empty v-if="!loading && (!overview || overview.count === 0)" description="该系统暂无活跃指标" />
      <el-row v-else-if="overview && overview.count > 0" :gutter="12">
        <el-col v-for="m in overview.metrics" :key="m.metricCode" :span="6" class="metric-col">
          <div class="metric-card" @click="goDetail(m)">
            <div class="metric-head">
              <span class="metric-name" :title="m.name">{{ m.name }}</span>
              <el-tag v-if="kindLabel(m.metricKind)" size="small" :type="m.metricKind === 'aggregate' ? 'danger' : 'warning'" effect="plain">{{ kindLabel(m.metricKind) }}</el-tag>
              <el-tag size="small" :type="levelTag(m.level)">{{ levelLabel(m.level) }}</el-tag>
            </div>
            <div class="metric-code">{{ m.metricCode }}</div>
            <div class="metric-value">
              <span class="num">{{ fmtVal(m.statValue) }}</span>
              <span class="unit">{{ m.unit || '' }}</span>
            </div>
            <el-progress :percentage="pct(m.waterLevel)" :color="levelColor(m.level)" :stroke-width="10" :show-text="false" />
            <div class="metric-wl">
              <span>水位 {{ m.waterLevel == null ? '-' : (m.waterLevel * 100).toFixed(1) + '%' }}</span>
              <span class="detail-link">详情 →</span>
            </div>
          </div>
        </el-col>
      </el-row>
    </div>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { Refresh } from '@element-plus/icons-vue'
import { http } from '@/api/http'

interface SysRow {
  systemCode: string
  name: string
}
interface MetricRow {
  metricCode: string
  name: string
  unit: string
  metricKind?: string
  statValue: number | null
  waterLevel: number | null
  level: string
}
interface Overview {
  systemCode: string
  metrics: MetricRow[]
  count: number
}

const router = useRouter()
const systems = ref<SysRow[]>([])
const code = ref('')
const overview = ref<Overview | null>(null)
const loading = ref(false)

async function loadSystems() {
  try {
    const d = (await http.get('/api/v1/systems', { params: { page: 1, pageSize: 200 } })) as unknown as { items: SysRow[] }
    systems.value = d.items ?? []
    if (!code.value && systems.value.length > 0) {
      code.value = systems.value[0].systemCode
      load()
    }
  } catch { /* 下拉加载失败不阻塞 */ }
}

async function load() {
  if (!code.value) {
    overview.value = null
    return
  }
  loading.value = true
  try {
    overview.value = (await http.get(`/api/v1/systems/${code.value}/overview`)) as unknown as Overview
  } catch {
    overview.value = null
  } finally {
    loading.value = false
  }
}

function goDetail(m: MetricRow) {
  const kind = m.metricKind ?? 'normal'
  if (kind === 'group' || kind === 'aggregate') {
    router.push(`/metrics/${m.metricCode}/aggregate-detail`)
  } else {
    router.push(`/metrics/${m.metricCode}/detail`)
  }
}
function kindLabel(kind?: string): string {
  return ({ aggregate: '汇总', group: '子指标' } as Record<string, string>)[kind ?? ''] ?? ''
}

function fmtVal(v: number | null): string {
  if (v == null) return '-'
  return v >= 100 ? v.toFixed(0) : v.toFixed(2)
}

function pct(wl: number | null): number {
  if (wl == null) return 0
  return Math.min(100, Math.max(0, Math.round(wl * 100)))
}

const LEVEL_LABELS: Record<string, string> = { normal: '正常', warn: '超预警', danger: '超危险', missing: '缺数', nodata: '无数据' }
const levelLabel = (l: string) => LEVEL_LABELS[l] ?? l
function levelTag(l: string): string {
  return ({ normal: 'success', warn: 'warning', danger: 'danger', missing: 'danger', nodata: 'info' } as Record<string, string>)[l] || 'info'
}
function levelColor(l: string): string {
  return l === 'danger' || l === 'missing' ? '#ff3b30' : l === 'warn' ? '#ff9500' : '#34c759'
}

onMounted(loadSystems)
</script>

<style scoped>
.toolbar { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; }
.toolbar-title { font-size: 15px; font-weight: 600; }
.sub { color: #c0c4cc; font-size: 12px; font-weight: 400; margin-left: 10px; }
.toolbar-right { display: flex; align-items: center; gap: 8px; }
.metric-col { margin-bottom: 12px; }
.metric-card {
  background: #fff; border: 1px solid #ebeef5; border-radius: 8px; padding: 12px;
  cursor: pointer; transition: box-shadow 0.2s, transform 0.2s;
}
.metric-card:hover { box-shadow: 0 2px 8px rgba(0, 0, 0, 0.12); transform: translateY(-1px); }
.metric-head { display: flex; align-items: center; justify-content: space-between; gap: 8px; }
.metric-name { font-size: 13px; font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.metric-code { color: #c0c4cc; font-size: 11px; margin: 2px 0 8px; }
.metric-value { margin-bottom: 8px; }
.num { font-size: 20px; font-weight: 700; font-variant-numeric: tabular-nums; }
.unit { color: #909399; font-size: 12px; margin-left: 4px; }
.metric-wl { display: flex; justify-content: space-between; color: #909399; font-size: 12px; margin-top: 6px; }
.detail-link { color: var(--cap-primary); }
</style>
