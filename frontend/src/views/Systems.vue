<template>
  <div class="page-card">
    <div class="toolbar">
      <el-input v-model="keyword" clearable placeholder="编码/名称关键字" style="width: 200px" @keyup.enter="reload" />
      <el-select v-model="filterStatus" clearable placeholder="状态" style="width: 120px" @change="reload">
        <el-option label="正常" value="normal" />
        <el-option label="维护中" value="maintenance" />
        <el-option label="已下线" value="offline" />
      </el-select>
      <el-button type="primary" :icon="Search" @click="reload">查询</el-button>
      <el-button type="success" :icon="Plus" @click="openCreate">新建系统</el-button>
      <div style="flex: 1" />
      <el-button :icon="Refresh" circle @click="reload" />
    </div>

    <el-table :data="items" v-loading="loading" border stripe>
      <el-table-column prop="systemCode" label="系统编码" min-width="140" />
      <el-table-column prop="name" label="系统名称" min-width="150" show-overflow-tooltip />
      <el-table-column prop="dept" label="部门" width="120" />
      <el-table-column prop="owner" label="负责人" width="100" />
      <el-table-column label="等级" width="90">
        <template #default="{ row }">
          <el-tag :type="row.level === 'core' ? 'danger' : row.level === 'important' ? 'warning' : 'info'" size="small">
            {{ ({ core: '核心', important: '重要', general: '一般' } as Record<string, string>)[row.level] ?? row.level }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="110">
        <template #default="{ row }">
          <el-dropdown trigger="click" @command="(v: string) => doStatus(row, v)">
            <el-tag :type="statusType(row.status)" style="cursor: pointer">{{ statusLabel(row.status) }} ▾</el-tag>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="normal" :disabled="row.status === 'normal'">正常</el-dropdown-item>
                <el-dropdown-item command="maintenance" :disabled="row.status === 'maintenance'">维护中（静默告警）</el-dropdown-item>
                <el-dropdown-item command="offline" :disabled="row.status === 'offline'">下线（冻结）</el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </template>
      </el-table-column>
      <el-table-column prop="createdAt" label="创建时间" width="160">
        <template #default="{ row }">{{ fmtTs(row.createdAt) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="200" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
          <el-button link type="warning" @click="openResources(row)">资源</el-button>
          <el-button v-if="row.status === 'offline'" link type="danger" @click="doDelete(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
    <div class="pager">
      <el-pagination v-model:current-page="page" :page-size="20" :total="total" layout="total, prev, pager, next" @current-change="load" />
    </div>
    <el-empty v-if="!loading && error" :description="error" />

    <el-dialog v-model="formVisible" :title="editing ? '编辑业务系统' : '新建业务系统'" width="560px">
      <el-form :model="form" label-width="100px">
        <el-form-item label="系统编码" required><el-input v-model="form.systemCode" :disabled="!!editing" placeholder="唯一编码，如 CORE_DB" /></el-form-item>
        <el-form-item label="系统名称" required><el-input v-model="form.name" /></el-form-item>
        <el-form-item label="部门"><el-input v-model="form.dept" /></el-form-item>
        <el-form-item label="负责人" required><el-input v-model="form.owner" /></el-form-item>
        <el-form-item label="等级">
          <el-select v-model="form.level" style="width: 100%">
            <el-option label="核心" value="core" />
            <el-option label="重要" value="important" />
            <el-option label="一般" value="general" />
          </el-select>
        </el-form-item>
        <el-form-item label="上线日期"><el-date-picker v-model="form.onlineDate" type="date" value-format="YYYY-MM-DD" style="width: 100%" /></el-form-item>
        <el-form-item label="备注"><el-input v-model="form.remark" type="textarea" :rows="2" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="formVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveForm">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="resVisible" :title="'资源信息 - ' + (resRow?.systemCode ?? '')" width="720px">
      <el-button type="primary" size="small" :icon="Plus" style="margin-bottom: 10px" @click="addRes">添加资源</el-button>
      <el-table :data="resources" border stripe size="small">
        <el-table-column label="类型" width="130">
          <template #default="{ row }">
            <el-select v-model="row.resType">
              <el-option v-for="t in resTypeOptions" :key="t.value" :label="t.label" :value="t.value" />
            </el-select>
          </template>
        </el-table-column>
        <el-table-column label="名称" min-width="120">
          <template #default="{ row }"><el-input v-model="row.name" /></template>
        </el-table-column>
        <el-table-column label="规格/IP" min-width="130">
          <template #default="{ row }"><el-input v-model="row.address" placeholder="规格或IP" /></template>
        </el-table-column>
        <el-table-column label="备注" min-width="110">
          <template #default="{ row }"><el-input v-model="row.remark" /></template>
        </el-table-column>
        <el-table-column label="" width="60">
          <template #default="{ $index }"><el-button link type="danger" @click="resources.splice($index, 1)">删除</el-button></template>
        </el-table-column>
      </el-table>
      <template #footer>
        <el-button @click="resVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveResources">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Search, Refresh, Plus } from '@element-plus/icons-vue'
import { http } from '@/api/http'
import { fmtTs } from '@/utils/format'

interface SysRow {
  systemCode: string
  name: string
  dept: string
  owner: string
  level: string
  status: string
  onlineDate: string | null
  remark: string
  createdAt: string
}
// 资源类型: 下拉显示中文, 落库为后端约定的英文编码(business_system_resource.res_type)
const resTypes = [
  { value: 'server', label: '应用服务器' },
  { value: 'db', label: '数据库' },
  { value: 'middleware', label: '中间件' },
  { value: 'storage', label: '存储' },
  { value: 'network', label: '网络' },
]

const items = ref<SysRow[]>([])
const loading = ref(false)
const error = ref('')
const page = ref(1)
const total = ref(0)
const keyword = ref('')
const filterStatus = ref('')

function statusLabel(s: string) { return { normal: '正常', maintenance: '维护中', offline: '已下线' }[s] ?? s }
function statusType(s: string) { return { normal: 'success', maintenance: 'warning', offline: 'info' }[s] ?? 'info' }

async function load() {
  loading.value = true
  error.value = ''
  try {
    const data = (await http.get('/api/v1/systems', {
      params: {
        page: page.value, pageSize: 20,
        ...(keyword.value ? { keyword: keyword.value } : {}),
        ...(filterStatus.value ? { status: filterStatus.value } : {}),
      },
    })) as unknown as { items: SysRow[]; total: number }
    items.value = data.items ?? []
    total.value = data.total ?? 0
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

const formVisible = ref(false)
const saving = ref(false)
const editing = ref(false)
const blankForm = () => ({ systemCode: '', name: '', dept: '', owner: '', level: 'general', onlineDate: null as string | null, remark: '' })
const form = ref(blankForm())
function openCreate() { form.value = blankForm(); editing.value = false; formVisible.value = true }
function openEdit(row: SysRow) {
  form.value = { ...row, onlineDate: row.onlineDate ? row.onlineDate.slice(0, 10) : null }
  editing.value = true
  formVisible.value = true
}
async function saveForm() {
  const f = form.value
  if (!f.systemCode || !f.name || !f.owner) { ElMessage.warning('请填写必填项'); return }
  saving.value = true
  try {
    const body = { ...f, onlineDate: f.onlineDate }
    if (editing.value) {
      await http.put(`/api/v1/systems/${f.systemCode}`, body)
      ElMessage.success('已更新')
    } else {
      await http.post('/api/v1/systems', body)
      ElMessage.success('已创建')
    }
    formVisible.value = false
    await load()
  } finally {
    saving.value = false
  }
}
async function doStatus(row: SysRow, status: string) {
  try {
    await http.post(`/api/v1/systems/${row.systemCode}/status`, { status })
    ElMessage.success('状态已更新为「' + statusLabel(status) + '」')
    await load()
  } catch { /* 后端已提示 */ }
}

// 仅「已下线」的系统可删除: 将级联清理该系统自有的流程/指标/数据源/分类/历史数据/告警/资源
async function doDelete(row: SysRow) {
  try {
    await ElMessageBox.confirm(
      `确认删除业务系统「${row.name}」(${row.systemCode})? 将同时清理该系统的采集流程、指标、数据源与分类、历史数据及告警, 删除后不可恢复。`,
      '删除业务系统',
      { type: 'error', confirmButtonText: '删除', confirmButtonClass: 'is-danger' },
    )
  } catch { return }
  try {
    await http.delete(`/api/v1/systems/${row.systemCode}`)
    ElMessage.success('已删除')
    await load()
  } catch { /* 后端已提示 */ }
}

// ---------- 资源 ----------
const resVisible = ref(false)
const resRow = ref<SysRow | null>(null)
interface ResItem { resType: string; name: string; address: string; remark: string }
const resources = ref<ResItem[]>([])
// 历史数据或外部写入可能出现未枚举的取值, 保留在下拉里避免被静默改写
const resTypeOptions = computed(() => {
  const known = new Set(resTypes.map((t) => t.value))
  const extra = [...new Set(resources.value.map((r) => r.resType).filter((v) => v && !known.has(v)))]
  return [...resTypes, ...extra.map((v) => ({ value: v, label: v }))]
})
async function openResources(row: SysRow) {
  resRow.value = row
  resVisible.value = true
  try {
    const data = (await http.get(`/api/v1/systems/${row.systemCode}/resources`)) as unknown as ResItem[]
    resources.value = (data ?? []).map((r) => ({
      resType: r.resType ?? '',
      name: r.name ?? '',
      address: r.address ?? '',
      remark: r.remark ?? '',
    }))
  } catch { resources.value = [] }
}
function addRes() { resources.value.push({ resType: resTypes[0].value, name: '', address: '', remark: '' }) }
async function saveResources() {
  if (resources.value.some((r) => !r.resType || !r.name.trim())) {
    ElMessage.warning('请填写资源类型与名称')
    return
  }
  saving.value = true
  try {
    await http.put(`/api/v1/systems/${resRow.value!.systemCode}/resources`, {
      items: resources.value.map((r) => ({
        resType: r.resType,
        name: r.name.trim(),
        address: r.address ?? '',
        remark: r.remark ?? '',
      })),
    })
    ElMessage.success('资源已保存')
    resVisible.value = false
  } finally {
    saving.value = false
  }
}

const reload = load

onMounted(load)
</script>

<style scoped>
.toolbar { display: flex; gap: 8px; align-items: center; margin-bottom: 14px; }
.pager { margin-top: 12px; display: flex; justify-content: flex-end; }
</style>
