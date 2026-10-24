<template>
  <div class="page-card">
    <div class="toolbar">
      <el-select v-model="filterSystem" clearable filterable placeholder="系统" style="width: 160px" @change="onFilterSystemChange">
        <el-option v-for="s in systems" :key="s.systemCode" :label="s.name" :value="s.systemCode" />
      </el-select>
      <el-select v-model="filterCategory" clearable placeholder="分类" style="width: 150px" @change="reload">
        <el-option v-for="ct in catsOf(filterSystem)" :key="ct.id" :label="ct.name" :value="ct.id" />
      </el-select>
      <el-select v-model="filterType" clearable placeholder="类型" style="width: 110px" @change="reload">
        <el-option label="数据库" value="db" />
        <el-option label="日志" value="log" />
        <el-option label="执行目标" value="ssh" />
      </el-select>
      <el-button type="primary" :icon="Search" @click="reload">查询</el-button>
      <el-button type="success" :icon="Plus" @click="openCreate">新建数据源</el-button>
      <el-button :icon="Upload" @click="openImport">导入 CSV</el-button>
      <el-button :icon="Collection" @click="openCategoryMgmt">分类管理</el-button>
      <div style="flex: 1" />
      <el-button :icon="Refresh" circle @click="reload" />
    </div>

    <el-table :data="items" v-loading="loading" border stripe>
      <el-table-column prop="name" label="名称" min-width="150" show-overflow-tooltip />
      <el-table-column label="系统" min-width="130" show-overflow-tooltip>
        <template #default="{ row }">{{ row.systemName || row.systemCode || '-' }}</template>
      </el-table-column>
      <el-table-column label="分类" min-width="110">
        <template #default="{ row }">{{ row.categoryName || '-' }}</template>
      </el-table-column>
      <el-table-column label="类型" width="90">
        <template #default="{ row }">
          <el-tag :type="TYPE_TAGS[row.type] ?? 'info'" size="small">{{ TYPE_LABELS[row.type] ?? row.type }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="概要" min-width="220" show-overflow-tooltip>
        <template #default="{ row }">{{ summary(row.config) }}</template>
      </el-table-column>
      <el-table-column label="健康状态" width="110">
        <template #default="{ row }">
          <el-tag v-if="row.healthStatus === 'ok'" type="success" size="small">可用</el-tag>
          <el-tag v-else-if="row.healthStatus === 'error'" type="danger" size="small">异常</el-tag>
          <el-tag v-else size="small">未检测</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag v-if="row.status === 'disabled'" type="info" size="small">已停用</el-tag>
          <el-tag v-else type="success" size="small">正常</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="lastSuccessAt" label="最近成功" width="160">
        <template #default="{ row }">{{ fmtTs(row.lastSuccessAt) }}</template>
      </el-table-column>
      <el-table-column prop="lastError" label="最近错误" min-width="200" show-overflow-tooltip>
        <template #default="{ row }">{{ row.lastError || '-' }}</template>
      </el-table-column>
      <el-table-column label="操作" width="250" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" :loading="testingId === row.id" @click="doTest(row)">测试连接</el-button>
          <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
          <el-button v-if="row.status !== 'disabled'" link type="danger" @click="doDisable(row)">停用</el-button>
          <el-button v-else link type="success" @click="doEnable(row)">启用</el-button>
          <el-button v-if="row.status === 'disabled'" link type="danger" @click="doDelete(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
    <el-empty v-if="!loading && error" :description="error" />
    <el-empty v-else-if="!loading && items.length === 0" description="暂无数据源" />

    <el-dialog v-model="formVisible" :title="editingId ? '编辑数据源' : '新建数据源'" width="620px">
      <el-form :model="form" label-width="110px">
        <el-form-item label="所属系统" required>
          <el-select v-model="form.systemCode" filterable placeholder="选择业务系统" style="width: 100%" @change="onFormSystemChange">
            <el-option v-for="s in systems" :key="s.systemCode" :label="s.name" :value="s.systemCode" />
          </el-select>
        </el-form-item>
        <el-form-item label="所属分类" required>
          <el-select v-model="form.categoryId" :disabled="!form.systemCode" filterable placeholder="选择该系统下的分类" style="width: 100%">
            <el-option v-for="ct in catsOf(form.systemCode)" :key="ct.id" :label="ct.name" :value="ct.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="名称" required>
          <el-input v-model="form.name" placeholder="唯一名称" />
        </el-form-item>
        <el-form-item label="类型" required>
          <el-radio-group v-model="form.type" :disabled="!!editingId">
            <el-radio value="db">数据库</el-radio>
            <el-radio value="log">日志文件</el-radio>
            <el-radio value="ssh">执行目标</el-radio>
          </el-radio-group>
        </el-form-item>

        <template v-if="form.type === 'db'">
          <el-form-item label="驱动" required>
            <el-select v-model="form.db.driver" style="width: 100%">
              <el-option label="Oracle (go-ora)" value="oracle" />
              <el-option label="MySQL (go-sql-driver)" value="mysql" />
            </el-select>
          </el-form-item>
          <el-form-item label="主机" required><el-input v-model="form.db.host" /></el-form-item>
          <el-form-item label="端口" required><el-input-number v-model="form.db.port" :min="1" :max="65535" style="width: 100%" /></el-form-item>
          <el-form-item v-if="form.db.driver === 'oracle'" label="Service" required><el-input v-model="form.db.service" /></el-form-item>
          <el-form-item v-else label="数据库名" required><el-input v-model="form.db.database" /></el-form-item>
          <el-form-item label="账号" required><el-input v-model="form.db.username" /></el-form-item>
          <el-form-item label="密码" :required="!editingId">
            <el-input v-model="form.db.password" type="password" show-password :placeholder="editingId ? '留空 = 不修改' : ''" />
          </el-form-item>
        </template>

        <template v-else-if="form.type === 'log'">
          <el-form-item label="读取方式">
            <el-radio-group v-model="form.log.scheme">
              <el-radio value="local">本地路径</el-radio>
              <el-radio value="sftp">SFTP</el-radio>
            </el-radio-group>
          </el-form-item>
          <template v-if="form.log.scheme === 'sftp'">
            <el-form-item label="主机" required><el-input v-model="form.log.host" /></el-form-item>
            <el-form-item label="端口"><el-input-number v-model="form.log.port" :min="1" :max="65535" style="width: 100%" /></el-form-item>
            <el-form-item label="账号" required><el-input v-model="form.log.username" /></el-form-item>
            <el-form-item label="密码" :required="!editingId">
              <el-input v-model="form.log.password" type="password" show-password :placeholder="editingId ? '留空 = 不修改' : ''" />
            </el-form-item>
          </template>
          <el-form-item label="路径模板" required>
            <el-input v-model="form.log.pathTemplate" placeholder="例: /data/aboss/log/${biz_date:YYYYMMDD}/" />
            <div class="field-hint">目录或单文件路径; 供连接测试/健康巡检列目录</div>
          </el-form-item>
          <el-form-item label="文件匹配 glob">
            <el-input v-model="form.log.globTemplate" placeholder="留空则用路径模板; 例: /data/aboss/log/${biz_date:YYYYMMDD}/*run_*${biz_date:YYYYMMDD}.txt" />
            <div class="field-hint">采集实际读取的文件匹配模式(优先级高于路径模板), 支持 ${biz_date:YYYYMMDD} 等变量; 不配则只按路径模板读, 读取多个文件时必须配置</div>
          </el-form-item>
          <el-form-item label="编码">
            <el-select v-model="form.log.encoding" style="width: 100%">
              <el-option label="UTF-8" value="utf-8" />
              <el-option label="GBK" value="gbk" />
            </el-select>
          </el-form-item>
        </template>

        <template v-else>
          <el-form-item label="目标系统">
            <el-radio-group v-model="form.ssh.os" @change="onSshOsChange">
              <el-radio value="linux">Linux</el-radio>
              <el-radio value="windows">Windows</el-radio>
            </el-radio-group>
            <div class="field-hint">决定脚本运行器(python3/bash/pwsh 或 python/powershell)与临时目录默认值</div>
          </el-form-item>
          <el-form-item label="主机" required><el-input v-model="form.ssh.host" /></el-form-item>
          <el-form-item label="端口"><el-input-number v-model="form.ssh.port" :min="1" :max="65535" style="width: 100%" /></el-form-item>
          <el-form-item label="账号" required><el-input v-model="form.ssh.username" /></el-form-item>
          <el-form-item label="密码" :required="!editingId">
            <el-input v-model="form.ssh.password" type="password" show-password :placeholder="editingId ? '留空 = 不修改' : ''" />
          </el-form-item>
          <el-form-item label="临时目录">
            <el-input v-model="form.ssh.tempDir" :placeholder="form.ssh.os === 'windows' ? 'C:/Windows/Temp' : '/tmp'" />
            <div class="field-hint">脚本与参数下发到此目录下的 capcollect-&lt;任务号&gt; 子目录, 执行结束后整个目录删除; Windows 目标建议填 C:/Windows/Temp</div>
          </el-form-item>
        </template>
      </el-form>
      <template #footer>
        <el-button @click="formVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveForm">保存</el-button>
      </template>
    </el-dialog>

    <!-- 数据源分类管理 -->
    <el-dialog v-model="catVisible" title="数据源分类管理" width="820px">
      <div class="cat-toolbar">
        <el-select v-model="catFilterSystem" clearable filterable placeholder="按系统筛选" style="width: 220px">
          <el-option v-for="s in systems" :key="s.systemCode" :label="s.name" :value="s.systemCode" />
        </el-select>
        <el-button type="primary" size="small" :icon="Plus" @click="openCatCreate">新建分类</el-button>
      </div>
      <el-table :data="filteredCats" border stripe size="small" v-loading="catLoading">
        <el-table-column label="所属系统" min-width="140" show-overflow-tooltip>
          <template #default="{ row }">{{ sysName(row.systemCode) }}</template>
        </el-table-column>
        <el-table-column prop="name" label="分类名称" min-width="130" />
        <el-table-column prop="sort" label="排序" width="70" />
        <el-table-column prop="remark" label="备注" min-width="150" show-overflow-tooltip>
          <template #default="{ row }">{{ row.remark || '-' }}</template>
        </el-table-column>
        <el-table-column label="创建时间" width="160">
          <template #default="{ row }">{{ fmtTs(row.createdAt) }}</template>
        </el-table-column>
        <el-table-column label="操作" width="110" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openCatEdit(row)">编辑</el-button>
            <el-button link type="danger" @click="doCatDelete(row)">删除</el-button>
          </template>
        </el-table-column>
      </el-table>
      <el-empty v-if="!catLoading && filteredCats.length === 0" description="暂无分类, 点击「新建分类」创建" :image-size="60" />

      <el-dialog v-model="catFormVisible" :title="catEditingId ? '编辑分类' : '新建分类'" width="480px" append-to-body>
        <el-form :model="catForm" label-width="100px">
          <el-form-item label="所属系统" required>
            <el-select v-model="catForm.systemCode" :disabled="!!catEditingId" filterable placeholder="选择业务系统" style="width: 100%">
              <el-option v-for="s in systems" :key="s.systemCode" :label="s.name" :value="s.systemCode" />
            </el-select>
          </el-form-item>
          <el-form-item label="分类名称" required>
            <el-input v-model="catForm.name" placeholder="系统内唯一, 如: 核心库 / 日志归档" />
          </el-form-item>
          <el-form-item label="排序">
            <el-input-number v-model="catForm.sort" :min="0" style="width: 100%" />
          </el-form-item>
          <el-form-item label="备注">
            <el-input v-model="catForm.remark" type="textarea" :rows="2" />
          </el-form-item>
        </el-form>
        <template #footer>
          <el-button @click="catFormVisible = false">取消</el-button>
          <el-button type="primary" :loading="catSaving" @click="saveCategory">保存</el-button>
        </template>
      </el-dialog>
    </el-dialog>

    <el-dialog v-model="importVisible" title="批量导入数据源" width="min(680px, calc(100vw - 32px))">
      <div class="import-hint">
        <div>CSV 表头：<code>{{ IMPORT_HEADER }}</code></div>
        <div>分类按「所属系统 + 分类名称」定位；同名数据源更新配置，密码留空则保留原密码，类型不可变更。</div>
        <el-button link type="primary" :icon="Download" @click="downloadTemplate">下载模板</el-button>
      </div>
      <input ref="importInput" class="import-file" type="file" accept=".csv" />
      <div v-if="importResult" class="import-result">
        <el-alert
          :type="importResult.failed === 0 ? 'success' : importResult.success === 0 ? 'error' : 'warning'"
          :title="`共 ${importResult.total} 行，成功 ${importResult.success}，失败 ${importResult.failed}`"
          :closable="false"
          show-icon
        />
        <el-table v-if="importResult.errors.length" :data="importResult.errors" size="small" border max-height="220">
          <el-table-column prop="row" label="行号" width="80" />
          <el-table-column prop="reason" label="失败原因" show-overflow-tooltip />
        </el-table>
      </div>
      <template #footer>
        <el-button @click="importVisible = false">关闭</el-button>
        <el-button type="primary" :icon="Upload" :loading="importing" @click="doImport">开始导入</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Search, Refresh, Plus, Collection, Upload, Download } from '@element-plus/icons-vue'
import { http } from '@/api/http'
import { fmtTs } from '@/utils/format'

// 数据源类型展示: db/log 为取数数据源, ssh 为远程脚本执行目标
const TYPE_LABELS: Record<string, string> = { db: '数据库', log: '日志', ssh: '执行目标' }
const TYPE_TAGS: Record<string, string> = { db: '', log: 'warning', ssh: 'success' }

interface DsRow {
  id: number
  name: string
  type: string
  status: string
  healthStatus: string
  config: string
  systemCode: string
  categoryId: number | null
  systemName?: string
  categoryName?: string
  lastSuccessAt: string | null
  lastError: string
  createdAt?: string
}
interface SysRow {
  systemCode: string
  name: string
}
interface CatRow {
  id: number
  systemCode: string
  name: string
  remark: string
  sort: number
  createdAt: string
}

const items = ref<DsRow[]>([])
const loading = ref(false)
const error = ref('')
const filterType = ref('')
const filterSystem = ref('')
const filterCategory = ref<number | ''>('')
const testingId = ref<number | null>(null)

// ---------- 系统 / 分类 基础数据 ----------
const systems = ref<SysRow[]>([])
const categories = ref<CatRow[]>([])
async function loadSystems() {
  try {
    const data = (await http.get('/api/v1/systems', { params: { page: 1, pageSize: 500 } })) as unknown as { items: SysRow[] }
    systems.value = data.items ?? []
  } catch { systems.value = [] }
}
async function loadCategories() {
  try {
    categories.value = ((await http.get('/api/v1/data-source-categories')) as unknown as CatRow[]) ?? []
  } catch { categories.value = [] }
}
function catsOf(systemCode: string): CatRow[] {
  return systemCode ? categories.value.filter((c) => c.systemCode === systemCode) : categories.value
}
function sysName(code: string) {
  return systems.value.find((s) => s.systemCode === code)?.name ?? code
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const params: Record<string, unknown> = {}
    if (filterType.value) params.type = filterType.value
    if (filterSystem.value) params.systemCode = filterSystem.value
    if (filterCategory.value !== '') params.categoryId = filterCategory.value
    const data = (await http.get('/api/v1/data-sources', { params })) as unknown as DsRow[]
    items.value = data ?? []
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}
function onFilterSystemChange() {
  filterCategory.value = ''
  reload()
}

function summary(config: string) {
  try {
    const c = JSON.parse(config)
    if (c.driver) return `${c.driver} ${c.host ?? ''}:${c.port ?? ''} ${c.service || c.database || ''}`.trim()
    if (c.os) return `${c.os} ssh ${c.host ? c.host + ':' + (c.port || 22) + ' ' : ''}${c.tempDir || ''}`.trim()
    // 采集实际读取的是 globTemplate(其次 pathTemplate), 列表优先展示真正生效的匹配模式
    if (c.scheme) return `${c.scheme} ${c.host ? c.host + ':' + (c.port || 22) + ' ' : ''}${c.globTemplate || c.pathTemplate || ''}`.trim()
    return config
  } catch {
    return config
  }
}
async function doTest(row: DsRow) {
  testingId.value = row.id
  try {
    const r = (await http.post(`/api/v1/data-sources/${row.id}/test`)) as unknown as { ok: boolean; message: string }
    if (r.ok) ElMessage.success('连接成功: ' + r.message)
    else ElMessage.error('连接失败: ' + r.message)
    await load()
  } finally {
    testingId.value = null
  }
}
async function doDisable(row: DsRow) {
  try {
    await ElMessageBox.confirm(`确认停用数据源「${row.name}」?`, '提示', { type: 'warning' })
    await http.post(`/api/v1/data-sources/${row.id}/disable`)
    ElMessage.success('已停用')
    await load()
  } catch { /* 取消 */ }
}

async function doEnable(row: DsRow) {
  try {
    await ElMessageBox.confirm(`确认启用数据源「${row.name}」? 启用后将自动执行一次连接测试。`, '提示', { type: 'warning' })
    await http.post(`/api/v1/data-sources/${row.id}/enable`)
    ElMessage.success('已启用')
    await load()
  } catch { /* 取消 */ }
}

async function doDelete(row: DsRow) {
  try {
    await ElMessageBox.confirm(
      `确认删除数据源「${row.name}」? 删除后不可恢复, 请确认相关采集流程已停用。`,
      '删除数据源',
      { type: 'error', confirmButtonText: '删除', confirmButtonClass: 'is-danger' },
    )
  } catch {
    return // 取消
  }
  try {
    await http.delete(`/api/v1/data-sources/${row.id}`)
    ElMessage.success('已删除')
    await load()
  } catch { /* 后端已提示 */ }
}

// ---------- 批量导入 ----------
const IMPORT_HEADER = 'name,type,systemCode,categoryName,driver,host,port,database,service,username,password,scheme,pathTemplate,globTemplate,os,tempDir'
interface ImportResult {
  total: number
  success: number
  failed: number
  status: string
  errors: { row: number; reason: string }[]
}
const importVisible = ref(false)
const importing = ref(false)
const importInput = ref<HTMLInputElement | null>(null)
const importResult = ref<ImportResult | null>(null)

function openImport() {
  importResult.value = null
  importVisible.value = true
}

function importCsvField(v: string) {
  return /[",\n]/.test(v) ? `"${v.replace(/"/g, '""')}"` : v
}

function downloadTemplate() {
  const lines = [
    IMPORT_HEADER.split(','),
    ['pay-db-01', 'db', 'PAY', '核心库', 'oracle', '10.0.0.1', '1521', '', 'ORCL', 'cap_read', '密码', '', '', '', '', ''],
    ['pay-log-01', 'log', 'PAY', '日志目录', '', '', '', '', '', '', '', 'local', '/data/cap', 'cap_*.log', '', ''],
    ['pay-ssh-01', 'ssh', 'PAY', '核心库', '', '10.0.0.5', '22', '', '', 'cap', '密码', '', '', '', 'linux', '/data/tmp'],
  ].map((row) => row.map(importCsvField).join(','))
  const blob = new Blob(['\ufeff' + lines.join('\n')], { type: 'text/csv;charset=utf-8' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = 'data_source_import_template.csv'
  a.click()
  URL.revokeObjectURL(url)
}

async function doImport() {
  const input = importInput.value
  const file = input?.files?.[0]
  if (!file) { ElMessage.warning('请选择 CSV 文件'); return }
  const formData = new FormData()
  formData.append('file', file)
  importing.value = true
  try {
    const data = (await http.post('/api/v1/data-sources/import', formData)) as unknown as ImportResult
    importResult.value = { ...data, errors: data.errors ?? [] }
    if (data.failed === 0) ElMessage.success(`导入完成：成功 ${data.success} / 失败 0`)
    else ElMessage.warning(`导入完成：成功 ${data.success} / 失败 ${data.failed}`)
    await load()
  } finally {
    importing.value = false
    if (input) input.value = ''
  }
}

// ---------- 数据源表单 ----------
const formVisible = ref(false)
const saving = ref(false)
const editingId = ref<number | null>(null)
function blankForm() {
  return {
    systemCode: '', categoryId: null as number | null,
    name: '', type: 'db',
    db: { driver: 'oracle', host: '', port: 1521, service: '', database: '', username: '', password: '' },
    log: { scheme: 'local', host: '', port: 22, username: '', password: '', pathTemplate: '', globTemplate: '', encoding: 'utf-8' },
    ssh: { os: 'linux', host: '', port: 22, username: '', password: '', tempDir: '/tmp' },
  }
}
const form = ref(blankForm())
function onFormSystemChange() {
  form.value.categoryId = null
}
function onSshOsChange(os: string) {
  form.value.ssh.tempDir = os === 'windows' ? 'C:/Windows/Temp' : '/tmp'
}
function openCreate() { editingId.value = null; form.value = blankForm(); formVisible.value = true }
function openEdit(row: DsRow) {
  editingId.value = row.id
  form.value = blankForm()
  form.value.systemCode = row.systemCode || ''
  form.value.categoryId = row.categoryId
  form.value.name = row.name
  form.value.type = row.type
  try {
    const c = JSON.parse(row.config)
    if (row.type === 'db') {
      Object.assign(form.value.db, {
        driver: c.driver || 'oracle', host: c.host || '', port: c.port || 1521,
        service: c.service || '', database: c.database || '', username: c.username || '', password: '',
      })
    } else if (row.type === 'log') {
      Object.assign(form.value.log, {
        scheme: c.scheme || 'local', host: c.host || '', port: c.port || 22,
        username: c.username || '', password: '', pathTemplate: c.pathTemplate || '',
        globTemplate: c.globTemplate || '', encoding: c.encoding || 'utf-8',
      })
    } else {
      const os = c.os || 'linux'
      Object.assign(form.value.ssh, {
        os, host: c.host || '', port: c.port || 22, username: c.username || '', password: '',
        tempDir: c.tempDir || (os === 'windows' ? 'C:/Windows/Temp' : '/tmp'),
      })
    }
  } catch { /* config 解析失败保持空白 */ }
  formVisible.value = true
}
async function saveForm() {
  if (!form.value.systemCode) { ElMessage.warning('请选择所属系统'); return }
  if (!form.value.categoryId) { ElMessage.warning('请选择所属分类'); return }
  if (!form.value.name.trim()) { ElMessage.warning('请输入名称'); return }
    if (form.value.type === 'ssh' && (!form.value.ssh.host.trim() || !form.value.ssh.username.trim())) {
      ElMessage.warning('执行目标须填写主机与账号'); return
    }
  saving.value = true
  try {
    let config: Record<string, unknown>
    if (form.value.type === 'db') {
      const { password, ...rest } = form.value.db
      config = { ...rest }
      if (password) config.password = password
      } else if (form.value.type === 'log') {
      const { password, ...rest } = form.value.log
      config = { ...rest }
      if (password) config.password = password
      } else {
        const { password, ...rest } = form.value.ssh
        config = { ...rest }
        if (password) config.password = password
    }
    const body = {
      name: form.value.name, type: form.value.type, config,
      systemCode: form.value.systemCode, categoryId: form.value.categoryId,
    }
    if (editingId.value) {
      await http.put(`/api/v1/data-sources/${editingId.value}`, body)
      ElMessage.success('已更新')
    } else {
      await http.post('/api/v1/data-sources', body)
      ElMessage.success('已创建（将自动执行连接测试）')
    }
    formVisible.value = false
    await load()
  } finally {
    saving.value = false
  }
}

// ---------- 分类管理 ----------
const catVisible = ref(false)
const catLoading = ref(false)
const catSaving = ref(false)
const catFilterSystem = ref('')
const catFormVisible = ref(false)
const catEditingId = ref<number | null>(null)
const catForm = ref({ systemCode: '', name: '', sort: 0, remark: '' })
const filteredCats = computed(() =>
  catFilterSystem.value ? categories.value.filter((c) => c.systemCode === catFilterSystem.value) : categories.value,
)

async function openCategoryMgmt() {
  catVisible.value = true
  catLoading.value = true
  try {
    await Promise.all([loadSystems(), loadCategories()])
  } finally {
    catLoading.value = false
  }
}
function openCatCreate() {
  catEditingId.value = null
  catForm.value = { systemCode: catFilterSystem.value, name: '', sort: 0, remark: '' }
  catFormVisible.value = true
}
function openCatEdit(row: CatRow) {
  catEditingId.value = row.id
  catForm.value = { systemCode: row.systemCode, name: row.name, sort: row.sort ?? 0, remark: row.remark ?? '' }
  catFormVisible.value = true
}
async function saveCategory() {
  if (!catForm.value.systemCode) { ElMessage.warning('请选择所属系统'); return }
  if (!catForm.value.name.trim()) { ElMessage.warning('请输入分类名称'); return }
  catSaving.value = true
  try {
    if (catEditingId.value) {
      await http.put(`/api/v1/data-source-categories/${catEditingId.value}`, {
        name: catForm.value.name, remark: catForm.value.remark, sort: catForm.value.sort,
      })
      ElMessage.success('已更新')
    } else {
      await http.post('/api/v1/data-source-categories', {
        systemCode: catForm.value.systemCode, name: catForm.value.name,
        remark: catForm.value.remark, sort: catForm.value.sort,
      })
      ElMessage.success('已创建')
    }
    catFormVisible.value = false
    await loadCategories()
    await load()
  } catch { /* 后端已提示 */ } finally {
    catSaving.value = false
  }
}
async function doCatDelete(row: CatRow) {
  try {
    await ElMessageBox.confirm(`确认删除分类「${sysName(row.systemCode)} / ${row.name}」? 该分类下仍有数据源时无法删除。`, '删除分类', { type: 'warning' })
  } catch {
    return // 取消
  }
  try {
    await http.delete(`/api/v1/data-source-categories/${row.id}`)
    ElMessage.success('已删除')
    await Promise.all([loadCategories(), load()])
  } catch { /* 后端已提示 */ }
}

const reload = load

onMounted(async () => {
  await Promise.all([loadSystems(), loadCategories()])
  await load()
})
</script>

<style scoped>
.toolbar { display: flex; gap: 8px; align-items: center; margin-bottom: 14px; }
.cat-toolbar { display: flex; gap: 8px; align-items: center; margin-bottom: 12px; }
.field-hint { margin-top: 4px; font-size: 12px; line-height: 18px; color: var(--el-text-color-secondary); }
.import-hint { display: flex; flex-direction: column; gap: 6px; font-size: 12px; line-height: 18px; color: var(--el-text-color-secondary); }
.import-hint code { display: block; overflow-x: auto; white-space: nowrap; color: var(--el-text-color-primary); }
.import-file { margin: 12px 0 4px; }
.import-result { display: flex; flex-direction: column; gap: 10px; margin-top: 8px; }
</style>
