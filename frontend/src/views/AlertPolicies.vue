<template>
  <div :class="embedded ? 'embedded-page' : 'page-card'">
    <div class="toolbar" :class="{ 'embedded-toolbar': embedded }">
      <div v-if="!embedded" class="toolbar-title">告警升级策略</div>
      <div>
        <el-button type="primary" :icon="Plus" @click="openCreate">新建策略</el-button>
        <el-button :icon="Refresh" circle @click="load" />
      </div>
    </div>

    <div class="filter-row">
      <el-select v-model="filters.scopeType" clearable placeholder="作用域" style="width: 130px">
        <el-option label="全局" value="global" />
        <el-option label="业务系统" value="system" />
        <el-option label="指标" value="metric" />
      </el-select>
      <el-select v-model="filters.enabled" clearable placeholder="状态" style="width: 110px">
        <el-option label="启用" value="true" />
        <el-option label="停用" value="false" />
      </el-select>
      <el-button type="primary" :icon="Search" @click="load">查询</el-button>
    </div>

    <el-table :data="items" v-loading="loading" border stripe>
      <el-table-column prop="name" label="策略名称" min-width="210" show-overflow-tooltip />
      <el-table-column label="触发线" width="100">
        <template #default="{ row }">
          <el-tag :type="row.sourceLine === 'danger' ? 'danger' : 'warning'" size="small">
            {{ row.sourceLine === 'danger' ? '危险线' : '预警线' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="targetLevel" label="升级级别" width="100">
        <template #default="{ row }"><el-tag :type="levelTag(row.targetLevel)" size="small">{{ row.targetLevel.toUpperCase() }}</el-tag></template>
      </el-table-column>
      <el-table-column label="判定规则" min-width="170">
        <template #default="{ row }">{{ ruleText(row) }}</template>
      </el-table-column>
      <el-table-column label="作用域" min-width="180">
        <template #default="{ row }">
          <span v-if="row.scopeType === 'global'" class="hint">全局</span>
          <template v-else>
            <el-tag size="small" type="info" class="scope-tag">{{ row.scopeType === 'system' ? '系统' : '指标' }}</el-tag>
            <el-tag v-for="v in (row.scopeValue || [])" :key="v" size="small" class="scope-tag">{{ v }}</el-tag>
          </template>
        </template>
      </el-table-column>
      <el-table-column prop="priority" label="优先级" width="90" align="right" />
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-switch :model-value="row.enabled" @change="(v: boolean) => toggle(row, v)" />
        </template>
      </el-table-column>
      <el-table-column label="操作" width="210" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" size="small" @click="openPreview(row)">历史命中</el-button>
          <el-button link type="primary" size="small" @click="openEdit(row)">编辑</el-button>
          <el-button link type="danger" size="small" @click="remove(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
    <el-empty v-if="!loading && items.length === 0" description="暂无升级策略" />

    <el-dialog v-model="editDialog" :title="form.id ? '编辑升级策略' : '新建升级策略'" width="720px">
      <el-form :model="form" label-width="112px">
        <el-form-item label="策略名称">
          <el-input v-model="form.name" maxlength="128" show-word-limit />
        </el-form-item>
        <el-form-item label="触发线">
          <el-radio-group v-model="form.sourceLine" @change="onSourceChange">
            <el-radio value="warn">预警线</el-radio>
            <el-radio value="danger">危险线</el-radio>
          </el-radio-group>
          <span class="hint">预警线升级 P2，危险线升级 P1</span>
        </el-form-item>
        <el-form-item label="判定模式">
          <el-radio-group v-model="form.mode">
            <el-radio value="consecutive">连续命中</el-radio>
            <el-radio value="window_count">窗口累计</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item v-if="form.mode === 'consecutive'" label="连续天数 N">
          <el-input-number v-model="form.consecutiveDays" :min="1" :max="90" controls-position="right" />
        </el-form-item>
        <template v-else>
          <el-form-item label="窗口天数 N">
            <el-input-number v-model="form.windowDays" :min="1" :max="90" controls-position="right" />
          </el-form-item>
          <el-form-item label="命中天数 M">
            <el-input-number v-model="form.hitDays" :min="1" :max="form.windowDays" controls-position="right" />
          </el-form-item>
        </template>
        <el-form-item label="作用域">
          <el-select v-model="form.scopeType" style="width: 160px" @change="onScopeTypeChange">
            <el-option label="全局" value="global" />
            <el-option label="业务系统" value="system" />
            <el-option label="指标" value="metric" />
          </el-select>
          <el-select
            v-if="form.scopeType === 'system'"
            v-model="form.scopeValue"
            multiple
            collapse-tags
            collapse-tags-tooltip
            filterable
            placeholder="可多选系统"
            style="margin-left: 10px; width: 320px"
          >
            <el-option v-for="s in systems" :key="s.systemCode" :label="`${s.name} (${s.systemCode})`" :value="s.systemCode" />
          </el-select>
          <el-select
            v-if="form.scopeType === 'metric'"
            v-model="form.scopeValue"
            multiple
            collapse-tags
            collapse-tags-tooltip
            filterable
            placeholder="可多选指标"
            style="margin-left: 10px; width: 360px"
          >
            <el-option v-for="m in metrics" :key="m.metricCode" :label="`${m.name} (${m.metricCode})`" :value="m.metricCode" />
          </el-select>
        </el-form-item>
        <el-form-item label="优先级">
          <el-input-number v-model="form.priority" :min="1" :max="9999" controls-position="right" />
          <span class="hint">数值越大越优先</span>
        </el-form-item>
        <el-form-item label="启用">
          <el-switch v-model="form.enabled" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="editDialog = false">取消</el-button>
        <el-button :loading="simulating" @click="simulateDraft">模拟重放</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="previewDialog" title="历史命中证据" width="760px">
      <div class="filter-row">
        <el-select v-model="previewMetric" filterable placeholder="选择指标" style="width: 300px">
          <el-option v-for="m in metrics" :key="m.metricCode" :label="`${m.name} (${m.metricCode})`" :value="m.metricCode" />
        </el-select>
        <el-input-number v-model="previewDays" :min="1" :max="366" controls-position="right" />
        <span class="hint">天</span>
        <el-button type="primary" :loading="previewing" @click="runPreview">重放</el-button>
      </div>
      <SimulationView :result="previewResult" />
    </el-dialog>

    <el-dialog v-model="simulationDialog" title="策略模拟重放" width="760px">
      <div class="filter-row">
        <el-select v-model="simMetric" filterable placeholder="选择指标" style="width: 300px">
          <el-option v-for="m in metrics" :key="m.metricCode" :label="`${m.name} (${m.metricCode})`" :value="m.metricCode" />
        </el-select>
        <el-date-picker v-model="simRange" type="daterange" value-format="YYYY-MM-DD" start-placeholder="开始日期" end-placeholder="结束日期" style="width: 240px" />
        <el-button type="primary" :loading="simulating" @click="runDraftSimulation">执行</el-button>
      </div>
      <SimulationView :result="simulationResult" />
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { h, onMounted, reactive, ref } from 'vue'
import { Plus, Refresh, Search } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { http } from '@/api/http'

defineProps<{ embedded?: boolean }>()

interface Policy {
  id?: number
  name: string
  sourceLine: 'warn' | 'danger'
  targetLevel: 'p1' | 'p2'
  mode: 'consecutive' | 'window_count'
  consecutiveDays: number
  windowDays: number
  hitDays: number
  dayBasis: string
  scopeType: 'global' | 'system' | 'metric'
  scopeValue: string[]
  enabled: boolean
  priority: number
}

interface SimulationResult {
  matched: boolean
  level?: string
  reason?: string
  evidence?: Record<string, any>
  evaluatedOn?: string
  days?: { bizDate: string; warnHit: boolean; dangerHit: boolean; dataState: string }[]
}

const SimulationView = {
  props: { result: { type: Object as () => SimulationResult | null, default: null } },
  setup(props: { result: SimulationResult | null }) {
    return () => {
      const r = props.result
      if (!r) return h('el-empty', { description: '尚未执行模拟' })
      const evidence = r.evidence || {}
      return h('div', { class: 'sim-result' }, [
        h('el-alert', {
          type: r.matched ? 'success' : 'info',
          showIcon: true,
          title: r.matched ? `命中规则，升级为 ${String(r.level || '').toUpperCase()}` : '未命中升级规则',
          description: r.reason || `评估截止 ${r.evaluatedOn || '-'}`,
        }),
        h('div', { class: 'evidence' },
          Object.keys(evidence).length
            ? Object.entries(evidence).map(([k, v]) => h('span', { class: 'evidence-item' }, `${k}: ${Array.isArray(v) ? v.join(', ') : String(v)}`))
            : []),
        h('el-table', { data: r.days || [], border: true, size: 'small', maxHeight: 320, style: 'margin-top:12px' }, [
          h('el-table-column', { prop: 'bizDate', label: '业务日', width: 120 }),
          h('el-table-column', { label: '数据状态', width: 100, formatter: (_r: any, _c: any, v: string) => ({ valid: '有效', missing: '缺数', anomaly: '异常' } as Record<string, string>)[v] || v }),
          h('el-table-column', { label: '预警命中', width: 100, formatter: (_r: any, _c: any, v: boolean) => v ? '是' : '-' }),
          h('el-table-column', { label: '危险命中', width: 100, formatter: (_r: any, _c: any, v: boolean) => v ? '是' : '-' }),
        ]),
      ])
    }
  },
}

const items = ref<Policy[]>([])
const loading = ref(false)
const filters = reactive<{ scopeType: string; enabled: string }>({ scopeType: '', enabled: '' })
const metrics = ref<Record<string, any>[]>([])
const systems = ref<Record<string, any>[]>([])

const defaultForm = (): Policy => ({
  name: '', sourceLine: 'warn', targetLevel: 'p2', mode: 'consecutive',
  consecutiveDays: 3, windowDays: 7, hitDays: 3, dayBasis: 'natural',
  scopeType: 'global', scopeValue: [], enabled: true, priority: 100,
})
const form = reactive<Policy>(defaultForm())
const editDialog = ref(false)
const saving = ref(false)
const simulating = ref(false)
const simulationDialog = ref(false)
const simulationResult = ref<SimulationResult | null>(null)
const simMetric = ref('')
const simRange = ref<string[] | null>(null)
const previewDialog = ref(false)
const previewing = ref(false)
const previewResult = ref<SimulationResult | null>(null)
const previewMetric = ref('')
const previewDays = ref(30)
const previewPolicyId = ref<number | null>(null)

async function load() {
  loading.value = true
  try {
    const params: Record<string, any> = {}
    if (filters.scopeType) params.scopeType = filters.scopeType
    if (filters.enabled) params.enabled = filters.enabled
    const res = await http.get('/api/v1/alert-escalation-policies', { params }) as unknown as { items: Policy[] }
    items.value = res.items || []
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : String(e))
  } finally {
    loading.value = false
  }
}

async function loadOptions() {
  const [ms, ss] = await Promise.all([
    http.get('/api/v1/metrics', { params: { pageSize: 500 } }) as unknown as Promise<{ items?: Record<string, any>[] }>,
    http.get('/api/v1/systems', { params: { pageSize: 500 } }) as unknown as Promise<{ items?: Record<string, any>[] }>,
  ])
  metrics.value = ms.items || []
  systems.value = ss.items || []
}

function resetForm(row?: Policy) {
  Object.assign(form, row ? { ...row } : defaultForm())
  // 深拷贝作用域数组: el-select(multiple) 会就地改写绑定数组, 否则取消编辑也会污染表格行
  form.scopeValue = [...(row?.scopeValue || [])]
  if (row) form.enabled = row.enabled
}

function openCreate() {
  resetForm()
  editDialog.value = true
}
function openEdit(row: Policy) {
  resetForm(row)
  editDialog.value = true
}

function onSourceChange(v: string | number | boolean | undefined) {
  form.targetLevel = v === 'danger' ? 'p1' : 'p2'
}

function onScopeTypeChange() {
  form.scopeValue = []
}

function ruleText(row: Policy) {
  return row.mode === 'consecutive' ? `连续 ${row.consecutiveDays} 天` : `最近 ${row.windowDays} 天中 ${row.hitDays} 天`
}
function levelTag(level: string) {
  return level === 'p1' ? 'danger' : level === 'p2' ? 'warning' : 'info'
}

async function save() {
  if (!form.name.trim()) {
    ElMessage.warning('请填写策略名称')
    return
  }
  if (form.scopeType !== 'global' && form.scopeValue.length === 0) {
    ElMessage.warning('请至少选择一个作用域目标')
    return
  }
  saving.value = true
  try {
    const payload = {
      ...form,
      targetLevel: form.sourceLine === 'danger' ? 'p1' : 'p2',
      consecutiveDays: form.mode === 'consecutive' ? form.consecutiveDays : 0,
      windowDays: form.mode === 'window_count' ? form.windowDays : 0,
      hitDays: form.mode === 'window_count' ? form.hitDays : 0,
    }
    if (form.id) await http.put(`/api/v1/alert-escalation-policies/${form.id}`, payload)
    else await http.post('/api/v1/alert-escalation-policies', payload)
    editDialog.value = false
    ElMessage.success('策略已保存并热生效')
    load()
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : String(e))
  } finally {
    saving.value = false
  }
}

async function toggle(row: Policy, enabled: boolean) {
  try {
    await http.put(`/api/v1/alert-escalation-policies/${row.id}`, { ...row, enabled })
    row.enabled = enabled
    ElMessage.success(enabled ? '策略已启用' : '策略已停用')
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : String(e))
    load()
  }
}

async function remove(row: Policy) {
  try {
    await ElMessageBox.confirm(`确认删除策略“${row.name}”？`, '提示', { type: 'warning' })
    await http.delete(`/api/v1/alert-escalation-policies/${row.id}`)
    ElMessage.success('策略已删除')
    load()
  } catch (e) {
    if (e !== 'cancel') ElMessage.error(e instanceof Error ? e.message : String(e))
  }
}

function draftPayload() {
  return {
    ...form,
    targetLevel: form.sourceLine === 'danger' ? 'p1' : 'p2',
    consecutiveDays: form.mode === 'consecutive' ? form.consecutiveDays : 0,
    windowDays: form.mode === 'window_count' ? form.windowDays : 0,
    hitDays: form.mode === 'window_count' ? form.hitDays : 0,
    id: undefined,
  }
}

async function simulateDraft() {
  simulationDialog.value = true
  simulationResult.value = null
  if (!simRange.value) {
    const to = new Date()
    const from = new Date()
    from.setDate(from.getDate() - 29)
    simRange.value = [from.toISOString().slice(0, 10), to.toISOString().slice(0, 10)]
  }
}

async function runDraftSimulation() {
  if (!simMetric.value || !simRange.value) {
    ElMessage.warning('请选择指标和模拟区间')
    return
  }
  simulating.value = true
  try {
    simulationResult.value = await http.post('/api/v1/alert-escalation-policies/simulate', {
      metricCode: simMetric.value,
      from: simRange.value[0],
      to: simRange.value[1],
      policy: draftPayload(),
    }) as unknown as SimulationResult
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : String(e))
  } finally {
    simulating.value = false
  }
}

function openPreview(row: Policy) {
  previewPolicyId.value = row.id || null
  previewResult.value = null
  previewDialog.value = true
}

async function runPreview() {
  if (!previewPolicyId.value || !previewMetric.value) {
    ElMessage.warning('请选择指标')
    return
  }
  previewing.value = true
  try {
    previewResult.value = await http.get(`/api/v1/alert-escalation-policies/${previewPolicyId.value}/preview`, {
      params: { metricCode: previewMetric.value, days: previewDays.value },
    }) as unknown as SimulationResult
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : String(e))
  } finally {
    previewing.value = false
  }
}

onMounted(async () => {
  await Promise.all([load(), loadOptions()])
})
</script>

<style scoped>
.embedded-page { min-width: 0; }
.toolbar { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; }
.embedded-toolbar { justify-content: flex-end; }
.toolbar-title { font-size: 15px; font-weight: 600; }
.filter-row { display: flex; align-items: center; gap: 8px; margin-bottom: 12px; flex-wrap: wrap; }
.hint { color: #909399; font-size: 12px; margin-left: 8px; }
.sim-result { min-height: 80px; }
.evidence { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 10px; }
.evidence-item { background: #f4f6f8; border-radius: 4px; padding: 3px 7px; color: #606266; font-size: 12px; }
.scope-tag { margin-right: 4px; }
</style>
