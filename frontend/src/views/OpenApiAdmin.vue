<template>
  <div class="page-card">
    <el-tabs v-model="tab" @tab-change="onTabChange">
      <el-tab-pane label="API Key" name="keys">
        <div class="toolbar">
          <div class="toolbar-title">
            API Key
            <span class="sub">对外数据服务凭证。secret 只在创建/轮换时返回一次，平台只保存摘要</span>
          </div>
          <div class="toolbar-right">
            <el-button type="primary" :icon="Plus" @click="openCreate">新建 Key</el-button>
            <el-button :icon="Refresh" circle @click="loadKeys" />
          </div>
        </div>
        <el-table :data="keys" v-loading="loading" border stripe empty-text="暂无 API Key">
          <el-table-column prop="name" label="名称" min-width="150" show-overflow-tooltip />
          <el-table-column prop="keyId" label="Key ID" width="180" show-overflow-tooltip />
          <el-table-column label="Scope" min-width="220">
            <template #default="{ row }">
              <el-tag v-for="s in row.scopes" :key="s" size="small" style="margin-right: 4px">{{ scopeLabel(s) }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="系统范围" min-width="140">
            <template #default="{ row }">{{ row.systemScope?.length ? row.systemScope.join(', ') : '不限' }}</template>
          </el-table-column>
          <el-table-column label="限流(/分)" width="100" align="right">
            <template #default="{ row }">{{ row.rateLimit }}</template>
          </el-table-column>
          <el-table-column label="请求签名" width="95" align="center">
            <template #default="{ row }">
              <el-tag :type="row.signatureRequired ? 'success' : 'info'" size="small">{{ row.signatureRequired ? '必须' : '关闭' }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="调用量" width="100" align="right">
            <template #default="{ row }">{{ row.callCount }}</template>
          </el-table-column>
          <el-table-column label="状态" width="90">
            <template #default="{ row }">
              <el-tag :type="row.status === 'active' ? 'success' : 'info'" size="small">{{ row.status === 'active' ? '启用' : '停用' }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="最近调用" width="170">
            <template #default="{ row }">{{ fmtTs(row.lastUsedAt) }}</template>
          </el-table-column>
          <el-table-column label="操作" width="300" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" size="small" @click="openEdit(row)">编辑</el-button>
              <el-button link type="warning" size="small" @click="rotate(row)">轮换</el-button>
              <template v-if="row.status === 'active'">
                <el-button link type="danger" size="small" @click="setStatus(row, false)">停用</el-button>
              </template>
              <template v-else>
                <el-button link type="success" size="small" @click="setStatus(row, true)">启用</el-button>
                <el-button link type="danger" size="small" @click="removeKey(row)">删除</el-button>
              </template>
            </template>
          </el-table-column>
        </el-table>
        <el-pagination
          class="pager"
          :total="keyTotal"
          :current-page="keyPage"
          :page-size="keyPageSize"
          :page-sizes="[20, 50, 100]"
          layout="total, sizes, prev, pager, next"
          @current-change="onKeyPageChange"
          @size-change="onKeySizeChange"
        />
      </el-tab-pane>

      <el-tab-pane label="调用审计" name="logs">
        <div class="toolbar">
          <div class="toolbar-title">API 调用审计 <span class="sub">含被拒绝的越权与超限请求</span></div>
          <div class="toolbar-right">
            <el-select v-model="logAllowed" style="width: 130px" @change="onLogAllowedChange">
              <el-option label="全部" value="" />
              <el-option label="仅成功" value="true" />
              <el-option label="仅拒绝" value="false" />
            </el-select>
            <el-button :icon="Refresh" circle @click="loadLogs" />
          </div>
        </div>
        <el-table :data="logs" v-loading="loading" border stripe size="small" empty-text="暂无调用记录">
          <el-table-column prop="keyName" label="Key" width="150" show-overflow-tooltip />
          <el-table-column prop="method" label="方法" width="80" />
          <el-table-column prop="path" label="路径" min-width="220" show-overflow-tooltip />
          <el-table-column prop="scope" label="Scope" width="150" />
          <el-table-column label="结果" width="90">
            <template #default="{ row }">
              <el-tag :type="row.allowed ? 'success' : 'danger'" size="small">{{ row.allowed ? '允许' : '拒绝' }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="reason" label="原因" min-width="140" show-overflow-tooltip />
          <el-table-column prop="ip" label="IP" width="130" />
          <el-table-column label="耗时(ms)" width="100" align="right">
            <template #default="{ row }">{{ row.latencyMs ?? '-' }}</template>
          </el-table-column>
          <el-table-column label="时间" width="170">
            <template #default="{ row }">{{ fmtTs(row.createdAt) }}</template>
          </el-table-column>
        </el-table>
        <el-pagination
          class="pager"
          :total="logTotal"
          :current-page="logPage"
          :page-size="logPageSize"
          :page-sizes="[20, 50, 100]"
          layout="total, sizes, prev, pager, next"
          @current-change="onLogPageChange"
          @size-change="onLogSizeChange"
        />
      </el-tab-pane>

    </el-tabs>

    <el-dialog v-model="keyDialog" :title="keyForm.id ? '编辑 API Key' : '新建 API Key'" width="560px">
      <el-form :model="keyForm" label-width="110px">
        <el-form-item label="名称">
          <el-input v-model="keyForm.name" maxlength="128" />
        </el-form-item>
        <el-form-item label="Scope">
          <el-checkbox-group v-model="keyForm.scopes">
            <el-checkbox value="data:read">数据读取</el-checkbox>
            <el-checkbox value="forecast:read">预测读取</el-checkbox>
            <el-checkbox value="alert:webhook">告警 Webhook</el-checkbox>
          </el-checkbox-group>
        </el-form-item>
        <el-form-item label="系统范围">
          <el-select v-model="keyForm.systemScope" multiple filterable allow-create default-first-option style="width: 100%" placeholder="留空表示不限系统">
            <el-option v-for="s in systems" :key="s.systemCode" :label="`${s.name} (${s.systemCode})`" :value="s.systemCode" />
          </el-select>
        </el-form-item>
        <el-form-item label="限流(/分)">
          <el-input-number v-model="keyForm.rateLimit" :min="1" :max="100000" />
        </el-form-item>
        <el-form-item label="请求签名">
          <el-switch v-model="keyForm.signatureRequired" />
          <span class="dim" style="margin-left: 8px">启用后调用方必须携带时间戳、nonce 与 HMAC-SHA256 签名</span>
        </el-form-item>
        <el-form-item label="备注">
          <el-input v-model="keyForm.remark" type="textarea" :rows="2" maxlength="512" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="keyDialog = false">取消</el-button>
        <el-button type="primary" :loading="submitting" @click="submitKey">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="secretDialog" title="API Key 已生成" width="560px">
      <el-alert type="warning" :closable="false" show-icon title="secret 只显示一次，请立即交付给调用方；平台不再保存明文。" />
      <el-input v-model="generatedKey" readonly style="margin-top: 12px">
        <template #append>
          <el-button @click="copyKey">复制</el-button>
        </template>
      </el-input>
    </el-dialog>

  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh } from '@element-plus/icons-vue'
import { http } from '@/api/http'

interface KeyRow {
  id: number
  name: string
  keyId: string
  scopes: string[]
  systemScope: string[]
  rateLimit: number
  signatureRequired: boolean
  status: string
  remark: string
  callCount: number
  lastUsedAt: string | null
}

const tab = ref('keys')
const loading = ref(false)
const submitting = ref(false)
const keys = ref<KeyRow[]>([])
const logs = ref<Record<string, any>[]>([])
const keyPage = ref(1)
const keyPageSize = ref(20)
const keyTotal = ref(0)
const logPage = ref(1)
const logPageSize = ref(20)
const logTotal = ref(0)
const logAllowed = ref('')
const keyDialog = ref(false)
const secretDialog = ref(false)
const generatedKey = ref('')
const systems = ref<{ systemCode: string; name: string }[]>([])
const keyForm = reactive({ id: 0, name: '', scopes: ['data:read'] as string[], systemScope: [] as string[], rateLimit: 300, signatureRequired: false, remark: '' })

onMounted(async () => {
  await Promise.all([loadKeys(), loadSystems()])
})

function onTabChange(name: string) {
  if (name === 'logs') loadLogs()
}

async function loadKeys() {
  loading.value = true
  try {
    const data = (await http.get('/api/v1/open-api/keys', {
      params: { page: keyPage.value, pageSize: keyPageSize.value },
    })) as unknown as { items: KeyRow[]; total: number }
    keys.value = data.items ?? []
    keyTotal.value = data.total ?? keys.value.length
  } finally {
    loading.value = false
  }
}

function onKeyPageChange(p: number) {
  keyPage.value = p
  loadKeys()
}

function onKeySizeChange(s: number) {
  keyPageSize.value = s
  keyPage.value = 1
  loadKeys()
}

async function loadSystems() {
  try {
    systems.value = ((await http.get('/api/v1/systems', { params: { page: 1, pageSize: 500 } })) as unknown as { items: any[] }).items ?? []
  } catch {
    systems.value = []
  }
}

async function loadLogs() {
  loading.value = true
  try {
    const params: Record<string, unknown> = { page: logPage.value, pageSize: logPageSize.value }
    if (logAllowed.value) params.allowed = logAllowed.value
    const data = (await http.get('/api/v1/open-api/logs', { params })) as unknown as { items: Record<string, any>[]; total: number }
    logs.value = data.items ?? []
    logTotal.value = data.total ?? logs.value.length
  } finally {
    loading.value = false
  }
}

function onLogAllowedChange() {
  logPage.value = 1
  loadLogs()
}

function onLogPageChange(p: number) {
  logPage.value = p
  loadLogs()
}

function onLogSizeChange(s: number) {
  logPageSize.value = s
  logPage.value = 1
  loadLogs()
}

function openCreate() {
  Object.assign(keyForm, { id: 0, name: '', scopes: ['data:read'], systemScope: [], rateLimit: 300, signatureRequired: false, remark: '' })
  keyDialog.value = true
}

function openEdit(row: KeyRow) {
  Object.assign(keyForm, {
    id: row.id, name: row.name, scopes: [...row.scopes], systemScope: [...(row.systemScope ?? [])],
    rateLimit: row.rateLimit, signatureRequired: row.signatureRequired, remark: row.remark,
  })
  keyDialog.value = true
}

async function submitKey() {
  if (!keyForm.name.trim()) {
    ElMessage.warning('请填写名称')
    return
  }
  if (!keyForm.scopes.length) {
    ElMessage.warning('至少选择一个 scope')
    return
  }
  const body = {
    name: keyForm.name, scopes: keyForm.scopes, systemScope: keyForm.systemScope,
    rateLimit: keyForm.rateLimit, signatureRequired: keyForm.signatureRequired, remark: keyForm.remark,
  }
  submitting.value = true
  try {
    if (keyForm.id) {
      await http.put(`/api/v1/open-api/keys/${keyForm.id}`, body)
      ElMessage.success('已保存')
    } else {
      const data = (await http.post('/api/v1/open-api/keys', body)) as { apiKey: string }
      generatedKey.value = data.apiKey
      secretDialog.value = true
    }
    keyDialog.value = false
    await loadKeys()
  } finally {
    submitting.value = false
  }
}

async function rotate(row: KeyRow) {
  await ElMessageBox.confirm(`轮换「${row.name}」的 secret？旧 secret 将立即失效。`, '提示', { type: 'warning' })
  const data = (await http.post(`/api/v1/open-api/keys/${row.id}/rotate`)) as { apiKey: string }
  generatedKey.value = data.apiKey
  secretDialog.value = true
  await loadKeys()
}

async function setStatus(row: KeyRow, enable: boolean) {
  const action = enable ? 'enable' : 'disable'
  await http.post(`/api/v1/open-api/keys/${row.id}/${action}`)
  ElMessage.success(enable ? '已启用' : '已停用')
  await loadKeys()
}

async function removeKey(row: KeyRow) {
  try {
    await ElMessageBox.confirm(`删除后不可恢复，确认永久删除 API Key「${row.name}」？`, '删除确认', {
      type: 'error',
      confirmButtonText: '删除',
      confirmButtonClass: 'is-danger',
    })
  } catch {
    return
  }
  await http.delete(`/api/v1/open-api/keys/${row.id}`)
  ElMessage.success('已删除')
  if (keys.value.length === 1 && keyPage.value > 1) keyPage.value -= 1
  await loadKeys()
}

async function copyKey() {
  try {
    await navigator.clipboard.writeText(generatedKey.value)
    ElMessage.success('已复制')
  } catch {
    ElMessage.warning('复制失败，请手工选择文本')
  }
}

function scopeLabel(s: string) {
  return s === 'data:read' ? '数据读取' : s === 'forecast:read' ? '预测读取' : s === 'alert:webhook' ? '告警 Webhook' : s
}
function fmtTs(v?: string | null) {
  if (!v) return '-'
  const d = new Date(v)
  return Number.isNaN(d.getTime()) ? '-' : d.toLocaleString('zh-CN', { hour12: false })
}
</script>

<style scoped>
.page-card { background: #fff; padding: 16px; border-radius: 8px; }
.toolbar { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; flex-wrap: wrap; margin-bottom: 12px; }
.toolbar-title { font-size: 15px; font-weight: 600; }
.toolbar-title .sub { display: block; margin-top: 4px; font-size: 12px; font-weight: 400; color: var(--el-text-color-secondary); }
.toolbar-right { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.dim { font-size: 12px; color: var(--el-text-color-secondary); }
.pager { margin-top: 12px; justify-content: flex-end; }
</style>
