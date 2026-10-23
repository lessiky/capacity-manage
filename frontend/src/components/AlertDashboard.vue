<template>
  <div class="dashboard-panel">
    <div class="dashboard-filters">
      <el-select v-model="pref.levels" multiple collapse-tags clearable placeholder="级别" style="width: 180px">
        <el-option v-for="o in levelOptions" :key="o.value" :label="o.label" :value="o.value" />
      </el-select>
      <el-select v-model="pref.statuses" multiple collapse-tags clearable placeholder="状态" style="width: 210px">
        <el-option v-for="o in statusOptions" :key="o.value" :label="o.label" :value="o.value" />
      </el-select>
      <el-select v-model="pref.alertTypes" multiple collapse-tags clearable placeholder="类型" style="width: 230px">
        <el-option v-for="o in typeOptions" :key="o.value" :label="o.label" :value="o.value" />
      </el-select>
      <el-select v-model="pref.systemCodes" multiple collapse-tags clearable placeholder="系统" style="width: 220px">
        <el-option v-for="s in systems" :key="s.systemCode" :label="`${s.name} (${s.systemCode})`" :value="s.systemCode" />
      </el-select>
      <el-radio-group v-model="pref.dateMode">
        <el-radio-button value="today">当日</el-radio-button>
        <el-radio-button value="recent_days">近 N 日</el-radio-button>
      </el-radio-group>
      <el-input-number
        v-if="pref.dateMode === 'recent_days'"
        v-model="pref.recentDays"
        :min="1"
        :max="365"
        data-test="dashboard-recent-days"
      />
      <el-button type="primary" :loading="saving" data-test="dashboard-save" @click="save">保存配置</el-button>
      <el-button @click="restoreDefaults">恢复默认</el-button>
      <el-button :icon="Refresh" circle title="刷新" @click="reload" />
    </div>

    <div class="sort-builder">
      <div class="sort-title">排序优先级</div>
      <div v-for="(rule, index) in pref.sortRules" :key="`${rule.field}-${index}`" class="sort-row">
        <span class="sort-index">{{ index + 1 }}</span>
        <el-select v-model="rule.field" style="width: 160px">
          <el-option
            v-for="o in sortFieldOptions"
            :key="o.value"
            :label="o.label"
            :value="o.value"
            :disabled="pref.sortRules.some((r, i) => i !== index && r.field === o.value)"
          />
        </el-select>
        <el-select v-model="rule.direction" style="width: 100px">
          <el-option label="升序" value="asc" />
          <el-option label="降序" value="desc" />
        </el-select>
        <el-button :icon="ArrowUp" circle title="上移" :disabled="index === 0" @click="moveRule(index, -1)" />
        <el-button :icon="ArrowDown" circle title="下移" :disabled="index === pref.sortRules.length - 1" @click="moveRule(index, 1)" />
        <el-button :icon="Delete" circle title="删除" @click="removeRule(index)" />
      </div>
      <el-button :icon="Plus" :disabled="pref.sortRules.length >= 5" @click="addRule">添加排序</el-button>
    </div>

    <el-table :data="items" v-loading="loading" border stripe highlight-current-row @row-click="emit('open', $event)">
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
      <el-table-column prop="durationMin" label="持续(min)" width="90" align="right" />
      <el-table-column prop="claimedBy" label="认领人" width="100">
        <template #default="{ row }">{{ row.claimedBy || '-' }}</template>
      </el-table-column>
    </el-table>
    <el-empty v-if="!loading && error" :description="error" />
    <el-empty v-else-if="!loading && items.length === 0" description="暂无告警" />
    <el-pagination
      v-model:current-page="page"
      :page-size="pageSize"
      :total="total"
      layout="total, prev, pager, next"
      class="pager"
      @current-change="loadData"
    />
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ArrowDown, ArrowUp, Delete, Plus, Refresh } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { http } from '@/api/http'
import { levelTag, statusLabel, statusTag, typeLabel } from '@/utils/alert'
import { fmtTs } from '@/utils/format'

interface DashboardSortRule {
  field: string
  direction: 'asc' | 'desc'
}

interface DashboardPreference {
  levels: string[]
  statuses: string[]
  alertTypes: string[]
  systemCodes: string[]
  dateMode: 'today' | 'recent_days'
  recentDays: number
  sortRules: DashboardSortRule[]
}

interface AlertItem {
  id: number
  alertNo: string
  level: string
  alertType: string
  systemCode: string
  metricCode: string
  status: string
  triggerAt: string
  lastValue: number | null
  threshold: number | null
  waterLevel: number | null
  durationMin: number | null
  claimedBy: string
}

interface SystemOption {
  systemCode: string
  name: string
}

const emit = defineEmits<{ open: [row: AlertItem] }>()
const loading = ref(false)
const saving = ref(false)
const error = ref('')
const items = ref<AlertItem[]>([])
const total = ref(0)
const page = ref(1)
const pageSize = 20
const systems = ref<SystemOption[]>([])
const pref = reactive<DashboardPreference>({
  levels: [],
  statuses: [],
  alertTypes: [],
  systemCodes: [],
  dateMode: 'today',
  recentDays: 7,
  sortRules: [{ field: 'triggerAt', direction: 'desc' }],
})

const levelOptions = [
  { label: 'P1', value: 'p1' },
  { label: 'P2', value: 'p2' },
  { label: 'P3', value: 'p3' },
  { label: 'P4', value: 'p4' },
]
const statusOptions = [
  { label: '活动', value: 'active' },
  { label: '处理中', value: 'processing' },
  { label: '已恢复', value: 'recovered' },
  { label: '已关闭', value: 'closed' },
]
const typeOptions = [
  { label: '阈值', value: 'threshold' },
  { label: '缺数', value: 'missing' },
  { label: '采集失败', value: 'collect_fail' },
  { label: '异常值', value: 'anomaly' },
  { label: '数据源异常', value: 'ds_error' },
]
const sortFieldOptions = [
  { label: '级别', value: 'level' },
  { label: '状态', value: 'status' },
  { label: '类型', value: 'alertType' },
  { label: '系统', value: 'systemCode' },
  { label: '触发时间', value: 'triggerAt' },
]

function applyPreference(next: DashboardPreference) {
  pref.levels = [...(next.levels || [])]
  pref.statuses = [...(next.statuses || [])]
  pref.alertTypes = [...(next.alertTypes || [])]
  pref.systemCodes = [...(next.systemCodes || [])]
  pref.dateMode = next.dateMode || 'today'
  pref.recentDays = next.recentDays || 7
  const rules = next.sortRules?.length ? next.sortRules : [{ field: 'triggerAt', direction: 'desc' as const }]
  pref.sortRules = rules.map((rule) => ({ field: rule.field, direction: rule.direction }))
}

function restoreDefaults() {
  applyPreference({
    levels: levelOptions.map((o) => o.value),
    statuses: statusOptions.map((o) => o.value),
    alertTypes: typeOptions.map((o) => o.value),
    systemCodes: [],
    dateMode: 'today',
    recentDays: 7,
    sortRules: [{ field: 'triggerAt', direction: 'desc' }],
  })
}

function addRule() {
  const used = new Set(pref.sortRules.map((rule) => rule.field))
  const next = sortFieldOptions.find((option) => !used.has(option.value))
  if (next) pref.sortRules.push({ field: next.value, direction: 'asc' })
}

function removeRule(index: number) {
  pref.sortRules.splice(index, 1)
  if (pref.sortRules.length === 0) pref.sortRules.push({ field: 'triggerAt', direction: 'desc' })
}

function moveRule(index: number, delta: number) {
  const target = index + delta
  if (target < 0 || target >= pref.sortRules.length) return
  const [rule] = pref.sortRules.splice(index, 1)
  pref.sortRules.splice(target, 0, rule)
}

async function loadData() {
  loading.value = true
  error.value = ''
  try {
    const data = await http.get('/api/v1/alerts/dashboard', {
      params: { page: page.value, pageSize },
    }) as unknown as { items: AlertItem[]; total: number }
    items.value = data.items || []
    total.value = data.total || 0
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

async function reload() {
  await loadData()
}

async function save() {
  saving.value = true
  error.value = ''
  try {
    const saved = await http.put('/api/v1/alerts/dashboard-preference', {
      levels: pref.levels,
      statuses: pref.statuses,
      alertTypes: pref.alertTypes,
      systemCodes: pref.systemCodes,
      dateMode: pref.dateMode,
      recentDays: pref.recentDays,
      sortRules: pref.sortRules.map((rule) => ({ ...rule })),
    }) as unknown as DashboardPreference
    applyPreference(saved)
    page.value = 1
    ElMessage.success('配置已保存')
    await loadData()
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    saving.value = false
  }
}

onMounted(async () => {
  loading.value = true
  error.value = ''
  try {
    const [savedRaw, optionsRaw] = await Promise.all([
      http.get('/api/v1/alerts/dashboard-preference'),
      http.get('/api/v1/alerts/dashboard-options'),
    ])
    const saved = savedRaw as unknown as DashboardPreference
    const options = optionsRaw as unknown as { systems: SystemOption[] }
    applyPreference(saved)
    systems.value = options.systems || []
    await loadData()
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
})

defineExpose({ reload })
</script>

<style scoped>
.dashboard-panel { display: flex; flex-direction: column; gap: 12px; }
.dashboard-filters { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.sort-builder { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.sort-title { font-size: 13px; font-weight: 600; }
.sort-row { display: flex; align-items: center; gap: 6px; }
.sort-index { width: 18px; text-align: center; color: #909399; font-size: 12px; }
.pager { display: flex; justify-content: flex-end; }
</style>
