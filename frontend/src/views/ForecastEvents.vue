<template>
  <div :class="embedded ? 'embedded-page' : 'page-card'">
    <div class="toolbar" :class="{ 'embedded-toolbar': embedded }">
      <div v-if="!embedded" class="toolbar-title">
        事件因子
        <span class="sub">上线 / 迁移 / 业务高峰 / 维护 / 政策变化等事件, 人工确认后进入预测模型（数据权限按用户授权系统过滤）</span>
      </div>
      <div class="toolbar-right">
        <el-select v-model="statusFilter" style="width: 130px" @change="load">
          <el-option label="全部状态" value="" />
          <el-option label="草稿" value="draft" />
          <el-option label="已确认" value="confirmed" />
          <el-option label="已过期" value="expired" />
        </el-select>
        <el-input v-model="keyword" clearable placeholder="名称 / 作用域" style="width: 180px" />
        <el-button type="primary" :icon="Plus" @click="openCreate">新建事件</el-button>
        <el-button :icon="Refresh" circle @click="load" />
      </div>
    </div>

    <div class="summary-row">
      <div class="run-card">
        <div class="run-label">已确认事件</div>
        <div class="run-value">{{ confirmedCount }}</div>
        <div class="run-sub">确认后才会参与预测修正</div>
      </div>
      <div class="run-card">
        <div class="run-label">草稿</div>
        <div class="run-value">{{ draftCount }}</div>
        <div class="run-sub">可继续编辑或让 AI 给出影响建议</div>
      </div>
      <div class="run-card">
        <div class="run-label">已过期</div>
        <div class="run-value">{{ expiredCount }}</div>
        <div class="run-sub">到期后自动置过期, 不再进入模型</div>
      </div>
      <div class="run-card">
        <div class="run-label">AI 状态</div>
        <div class="run-value">
          <el-tag :type="aiHint ? 'success' : 'info'" size="small">{{ aiHint ? '可用' : '未启用' }}</el-tag>
        </div>
        <div class="run-sub">{{ aiHint ? '可获取影响建议' : 'AI 关闭时仍可人工录入' }}</div>
      </div>
    </div>

    <el-table :data="filtered" v-loading="loading" border stripe max-height="520" empty-text="暂无事件因子">
      <el-table-column label="事件" min-width="190" show-overflow-tooltip>
        <template #default="{ row }">
          <div class="name">{{ row.name }}</div>
          <div class="code">v{{ row.version }} · {{ typeLabel(row.eventType) }}</div>
        </template>
      </el-table-column>
      <el-table-column label="作用域" min-width="160" show-overflow-tooltip>
        <template #default="{ row }">
          <el-tag size="small" type="info">{{ scopeLabel(row.scopeType) }}</el-tag>
          <span class="scope">{{ row.scopeValue || '全部系统' }}</span>
        </template>
      </el-table-column>
      <el-table-column label="影响区间" width="200">
        <template #default="{ row }">{{ dateOnly(row.startDate) }} ~ {{ row.endDate ? dateOnly(row.endDate) : '长期' }}</template>
      </el-table-column>
      <el-table-column label="影响" width="150">
        <template #default="{ row }">
          <span :style="{ color: row.impactDirection === 'increase' ? 'var(--cap-danger)' : 'var(--cap-success)' }">
            {{ row.impactDirection === 'increase' ? '推高' : '拉低' }}
            {{ row.impactPct != null ? Number(row.impactPct).toFixed(1) + '%' : '未填' }}
          </span>
        </template>
      </el-table-column>
      <el-table-column label="置信度" width="100" align="right">
        <template #default="{ row }">{{ row.confidence != null ? Number(row.confidence).toFixed(2) : '-' }}</template>
      </el-table-column>
      <el-table-column label="状态" width="110">
        <template #default="{ row }">
          <el-tag :type="statusTag(row.status)" size="small">{{ statusLabel(row.status) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="250" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" size="small" @click="openDetail(row)">详情</el-button>
          <el-button link type="primary" size="small" @click="openEdit(row)">编辑</el-button>
          <el-button v-if="row.status !== 'confirmed'" link type="success" size="small" @click="confirm(row)">确认</el-button>
          <el-button v-if="row.status !== 'expired'" link type="warning" size="small" @click="expire(row)">过期</el-button>
          <el-button link type="info" size="small" :loading="suggesting === row.id" @click="suggest(row)">AI 建议</el-button>
        </template>
      </el-table-column>
    </el-table>

    <el-dialog v-model="dialog" :title="form.id ? '编辑事件（生成新版本）' : '新建事件'" width="620px">
      <el-form :model="form" label-width="110px">
        <el-form-item label="事件名称">
          <el-input v-model="form.name" maxlength="128" placeholder="如: 双十一业务高峰" />
        </el-form-item>
        <el-form-item label="事件类型">
          <el-select v-model="form.eventType" style="width: 100%">
            <el-option v-for="t in eventTypes" :key="t.value" :label="t.label" :value="t.value" />
          </el-select>
        </el-form-item>
        <el-form-item label="作用域">
          <el-select v-model="form.scopeType" style="width: 140px">
            <el-option label="全局" value="global" />
            <el-option label="业务系统" value="system" />
            <el-option label="指标" value="metric" />
          </el-select>
          <el-input
            v-if="form.scopeType !== 'global'"
            v-model="form.scopeValue"
            style="width: 260px; margin-left: 8px"
            :placeholder="form.scopeType === 'system' ? '系统编码' : '指标编码，多个用逗号分隔'"
          />
        </el-form-item>
        <el-form-item label="影响区间">
          <el-date-picker v-model="form.range" type="daterange" value-format="YYYY-MM-DD" style="width: 100%" />
        </el-form-item>
        <el-form-item label="影响方向">
          <el-radio-group v-model="form.impactDirection">
            <el-radio value="increase">推高容量</el-radio>
            <el-radio value="decrease">拉低容量</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="预计影响 %">
          <el-input-number v-model="form.impactPct" :min="-100" :max="100" :precision="1" />
          <span class="hint-text">正数表示按方向预估的幅度</span>
        </el-form-item>
        <el-form-item label="置信度">
          <el-input-number v-model="form.confidence" :min="0" :max="1" :step="0.05" :precision="2" />
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="form.description" type="textarea" :rows="3" maxlength="1024" show-word-limit />
        </el-form-item>
        <el-alert
          v-if="form.longTerm"
          type="info"
          :closable="false"
          show-icon
          title="未选择结束日表示长期事件, 会持续影响预测直到手工置为过期。"
        />
      </el-form>
      <template #footer>
        <el-button @click="dialog = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submit">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="detailDialog" title="事件详情与版本" width="620px">
      <template v-if="detail">
        <el-descriptions :column="2" border size="small">
          <el-descriptions-item label="名称">{{ detail.name }}</el-descriptions-item>
          <el-descriptions-item label="类型">{{ typeLabel(detail.eventType) }}</el-descriptions-item>
          <el-descriptions-item label="作用域">{{ scopeLabel(detail.scopeType) }} {{ detail.scopeValue || '全部系统' }}</el-descriptions-item>
          <el-descriptions-item label="影响">{{ detail.impactDirection === 'increase' ? '推高' : '拉低' }} {{ detail.impactPct ?? '-' }}%</el-descriptions-item>
          <el-descriptions-item label="创建人">{{ detail.createdBy }}</el-descriptions-item>
          <el-descriptions-item label="确认人">{{ detail.confirmedBy || '-' }}</el-descriptions-item>
        </el-descriptions>
        <div class="section-title">AI 建议</div>
        <el-alert
          v-if="parsedSuggestion"
          type="success"
          :closable="false"
          show-icon
          :title="`${parsedSuggestion.impactDirection === 'increase' ? '推高' : '拉低'} ${parsedSuggestion.impactPct}% · 置信度 ${parsedSuggestion.confidence}`"
          :description="parsedSuggestion.rationale"
        />
        <el-empty v-else description="暂无 AI 建议" :image-size="60" />
        <div class="section-title">版本历史</div>
        <el-table :data="versions" size="small" border>
          <el-table-column prop="version" label="版本" width="80" />
          <el-table-column prop="status" label="状态" width="100">
            <template #default="{ row }">{{ statusLabel(row.status) }}</template>
          </el-table-column>
          <el-table-column label="影响" width="140">
            <template #default="{ row }">{{ row.impactDirection === 'increase' ? '推高' : '拉低' }} {{ row.impactPct ?? '-' }}%</template>
          </el-table-column>
          <el-table-column label="更新时间">
            <template #default="{ row }">{{ fmtTs(row.updatedAt) }}</template>
          </el-table-column>
        </el-table>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh } from '@element-plus/icons-vue'
import { http } from '@/api/http'

defineProps<{ embedded?: boolean }>()

interface EventRow {
  id: number
  name: string
  eventType: string
  scopeType: string
  scopeValue: string
  startDate: string
  endDate: string | null
  impactDirection: string
  impactPct: number | null
  confidence: number | null
  status: string
  version: number
  description: string
  aiSuggestion: string | null
  createdBy: string
  confirmedBy: string
  updatedAt: string
}

const eventTypes = [
  { value: 'launch', label: '上线' },
  { value: 'migrate', label: '迁移' },
  { value: 'peak', label: '业务高峰' },
  { value: 'maint', label: '维护' },
  { value: 'policy', label: '政策变化' },
  { value: 'other', label: '其他' },
]

const rows = ref<EventRow[]>([])
const versions = ref<EventRow[]>([])
const loading = ref(false)
const submitting = ref(false)
const suggesting = ref<number | null>(null)
const statusFilter = ref('')
const keyword = ref('')
const dialog = ref(false)
const detailDialog = ref(false)
const detail = ref<EventRow | null>(null)
const aiHint = ref(true)

const form = reactive({
  id: 0,
  name: '',
  eventType: 'peak',
  scopeType: 'global',
  scopeValue: '',
  range: [] as string[],
  impactDirection: 'increase',
  impactPct: 10,
  confidence: 0.7,
  description: '',
  longTerm: true,
})

const filtered = computed(() => {
  const kw = keyword.value.trim().toLowerCase()
  if (!kw) return rows.value
  return rows.value.filter((r) => r.name.toLowerCase().includes(kw) || (r.scopeValue || '').toLowerCase().includes(kw))
})
const confirmedCount = computed(() => rows.value.filter((r) => r.status === 'confirmed').length)
const draftCount = computed(() => rows.value.filter((r) => r.status === 'draft').length)
const expiredCount = computed(() => rows.value.filter((r) => r.status === 'expired').length)
const parsedSuggestion = computed(() => {
  if (!detail.value?.aiSuggestion) return null
  try {
    return JSON.parse(detail.value.aiSuggestion)
  } catch {
    return null
  }
})

onMounted(load)

async function load() {
  loading.value = true
  try {
    const query = statusFilter.value ? `?status=${statusFilter.value}` : ''
    const data = await http.get(`/api/v1/forecast-events${query}`)
    rows.value = (data as unknown as { items: EventRow[] }).items ?? []
  } finally {
    loading.value = false
  }
}

function openCreate() {
  Object.assign(form, {
    id: 0, name: '', eventType: 'peak', scopeType: 'global', scopeValue: '',
    range: [], impactDirection: 'increase', impactPct: 10, confidence: 0.7,
    description: '', longTerm: true,
  })
  dialog.value = true
}

function openEdit(row: EventRow) {
  Object.assign(form, {
    id: row.id, name: row.name, eventType: row.eventType, scopeType: row.scopeType,
    scopeValue: row.scopeValue, range: row.endDate ? [dateOnly(row.startDate), dateOnly(row.endDate)] : [dateOnly(row.startDate)],
    impactDirection: row.impactDirection, impactPct: row.impactPct ?? 0,
    confidence: row.confidence ?? 0.7, description: row.description,
    longTerm: !row.endDate,
  })
  dialog.value = true
}

async function submit() {
  if (!form.name.trim()) {
    ElMessage.warning('请填写事件名称')
    return
  }
  if (!form.range?.length) {
    ElMessage.warning('请选择影响区间')
    return
  }
  const body = {
    name: form.name,
    eventType: form.eventType,
    scopeType: form.scopeType,
    scopeValue: form.scopeType === 'global' ? '' : form.scopeValue,
    startDate: form.range[0],
    endDate: form.longTerm || form.range.length < 2 ? null : form.range[1],
    impactDirection: form.impactDirection,
    impactPct: form.impactPct,
    confidence: form.confidence,
    description: form.description,
  }
  submitting.value = true
  try {
    if (form.id) {
      await http.put(`/api/v1/forecast-events/${form.id}`, body)
    } else {
      await http.post('/api/v1/forecast-events', body)
    }
    ElMessage.success('已保存')
    dialog.value = false
    await load()
  } finally {
    submitting.value = false
  }
}

async function confirm(row: EventRow) {
  await ElMessageBox.confirm(`确认事件「${row.name}」进入预测模型？`, '提示', { type: 'warning' })
  await http.post(`/api/v1/forecast-events/${row.id}/confirm`)
  ElMessage.success('已确认')
  await load()
}

async function expire(row: EventRow) {
  await ElMessageBox.confirm(`将事件「${row.name}」置为过期？`, '提示', { type: 'warning' })
  await http.post(`/api/v1/forecast-events/${row.id}/expire`)
  ElMessage.success('已过期')
  await load()
}

async function suggest(row: EventRow) {
  suggesting.value = row.id
  try {
    const data = (await http.post(`/api/v1/forecast-events/${row.id}/ai-suggest`)) as {
      suggestion: { impactDirection: string; impactPct: number; confidence: number; rationale: string }
    }
    aiHint.value = true
    ElMessage.success(`AI 建议: ${data.suggestion.impactDirection === 'increase' ? '推高' : '拉低'} ${data.suggestion.impactPct}%`)
    await load()
  } catch {
    aiHint.value = false
  } finally {
    suggesting.value = null
  }
}

async function openDetail(row: EventRow) {
  const data = (await http.get(`/api/v1/forecast-events/${row.id}`)) as { event: EventRow; versions: EventRow[] }
  detail.value = data.event
  versions.value = data.versions ?? []
  detailDialog.value = true
}

function typeLabel(v: string) {
  return eventTypes.find((t) => t.value === v)?.label ?? v
}
function scopeLabel(v: string) {
  return v === 'global' ? '全局' : v === 'system' ? '业务系统' : '指标'
}
function statusLabel(v?: string) {
  return v === 'confirmed' ? '已确认' : v === 'expired' ? '已过期' : '草稿'
}
function statusTag(v?: string) {
  return v === 'confirmed' ? 'success' : v === 'expired' ? 'info' : 'warning'
}
function dateOnly(v?: string | null) {
  return v ? String(v).slice(0, 10) : '-'
}
function fmtTs(v?: string | null) {
  if (!v) return '-'
  const d = new Date(v)
  return Number.isNaN(d.getTime()) ? '-' : d.toLocaleString('zh-CN', { hour12: false })
}
</script>

<style scoped>
.page-card { background: #fff; padding: 16px; border-radius: 8px; }
.embedded-page { min-width: 0; }
.toolbar { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; flex-wrap: wrap; margin-bottom: 12px; }
.embedded-toolbar { justify-content: flex-end; }
.toolbar-title { font-size: 16px; font-weight: 600; }
.toolbar-title .sub { display: block; margin-top: 4px; font-size: 12px; font-weight: 400; color: var(--el-text-color-secondary); }
.toolbar-right { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.summary-row { display: grid; grid-template-columns: repeat(4, minmax(180px, 1fr)); gap: 12px; margin-bottom: 12px; }
.run-card { border: 1px solid var(--el-border-color-light); border-radius: 8px; padding: 12px 14px; }
.run-label { font-size: 12px; color: var(--el-text-color-secondary); }
.run-value { font-size: 22px; font-weight: 600; margin: 4px 0; }
.run-sub { font-size: 12px; color: var(--el-text-color-secondary); }
.name { font-weight: 600; }
.code { font-size: 12px; color: var(--el-text-color-secondary); }
.scope { margin-left: 6px; font-size: 12px; color: var(--el-text-color-secondary); }
.hint-text { margin-left: 8px; font-size: 12px; color: var(--el-text-color-secondary); }
.section-title { margin: 14px 0 6px; font-weight: 600; }
</style>
