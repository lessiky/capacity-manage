<template>
  <div class="panel">
    <div class="toolbar">
      <el-input v-model="form.metricCode" placeholder="指标编码" style="width: 160px" />
      <el-input v-model="form.name" placeholder="名称" style="width: 160px" />
      <el-input v-model="form.expression" placeholder="表达式 如 (A5_CPU + A5_MEM) / 2" style="width: 300px" />
      <el-input v-model="form.sourceMetrics" placeholder="源指标, 逗号分隔" style="width: 220px" />
      <el-button type="primary" :icon="Plus" @click="create">新增</el-button>
      <div class="spacer" />
      <el-button :icon="Refresh" circle @click="load" />
    </div>

    <el-table :data="rows" v-loading="loading" border stripe empty-text="暂无衍生指标">
      <el-table-column prop="metricCode" label="编码" width="180" />
      <el-table-column prop="name" label="名称" width="180" />
      <el-table-column prop="expression" label="表达式" min-width="260" show-overflow-tooltip />
      <el-table-column label="源指标" min-width="200" show-overflow-tooltip>
        <template #default="{ row }">{{ parseList(row.sourceMetrics).join(', ') }}</template>
      </el-table-column>
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag :type="row.status === 'active' ? 'success' : 'info'" size="small">
            {{ row.status === 'active' ? '启用' : '停用' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="220" fixed="right">
        <template #default="{ row }">
          <template v-if="row.status === 'active'">
            <el-button link type="primary" size="small" @click="evaluate(row)">试算</el-button>
            <el-button link type="warning" size="small" @click="disable(row)">停用</el-button>
          </template>
          <template v-else>
            <el-button link type="success" size="small" @click="enable(row)">启用</el-button>
            <el-button link type="danger" size="small" @click="remove(row)">删除</el-button>
          </template>
        </template>
      </el-table-column>
    </el-table>
    <el-pagination
      v-if="total > 0"
      class="pager"
      :total="total"
      :current-page="page"
      :page-size="pageSize"
      :page-sizes="[20, 50, 100]"
      layout="total, sizes, prev, pager, next"
      @current-change="onPageChange"
      @size-change="onSizeChange"
    />

    <el-dialog v-model="evalVisible" title="衍生指标试算" width="520px">
      <el-descriptions v-if="evalResult" :column="1" border size="small">
        <el-descriptions-item label="表达式">{{ evalResult.expression }}</el-descriptions-item>
        <el-descriptions-item label="源值">{{ formatValues(evalResult.values) }}</el-descriptions-item>
        <el-descriptions-item label="结果">{{ evalResult.result }}</el-descriptions-item>
      </el-descriptions>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh } from '@element-plus/icons-vue'
import { http } from '@/api/http'

const rows = ref<Record<string, any>[]>([])
const loading = ref(false)
const evalVisible = ref(false)
const evalResult = ref<Record<string, any> | null>(null)
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const form = reactive({ metricCode: '', name: '', expression: '', sourceMetrics: '' })

async function load() {
  loading.value = true
  try {
    const data = (await http.get('/api/v1/open-api/derived-metrics', {
      params: { page: page.value, pageSize: pageSize.value },
    })) as { items?: Record<string, any>[]; total?: number }
    rows.value = data.items ?? []
    total.value = data.total ?? rows.value.length
  } finally {
    loading.value = false
  }
}

function onPageChange(value: number) {
  page.value = value
  load()
}

function onSizeChange(value: number) {
  pageSize.value = value
  page.value = 1
  load()
}

async function create() {
  if (!form.metricCode.trim() || !form.expression.trim()) {
    ElMessage.warning('请填写指标编码与表达式')
    return
  }
  await http.post('/api/v1/open-api/derived-metrics', {
    metricCode: form.metricCode.trim(),
    name: form.name.trim() || form.metricCode.trim(),
    expression: form.expression.trim(),
    sourceMetrics: form.sourceMetrics.split(',').map((s) => s.trim()).filter(Boolean),
  })
  ElMessage.success('已新增')
  Object.assign(form, { metricCode: '', name: '', expression: '', sourceMetrics: '' })
  page.value = 1
  await load()
}

async function evaluate(row: Record<string, any>) {
  evalResult.value = (await http.post(`/api/v1/open-api/derived-metrics/${row.id}/evaluate`)) as Record<string, any>
  evalVisible.value = true
}

async function disable(row: Record<string, any>) {
  try {
    await ElMessageBox.confirm(`确认停用衍生指标「${row.name || row.metricCode}」？`, '提示', { type: 'warning' })
  } catch {
    return
  }
  await http.post(`/api/v1/open-api/derived-metrics/${row.id}/disable`)
  ElMessage.success('已停用')
  await load()
}

async function enable(row: Record<string, any>) {
  await http.post(`/api/v1/open-api/derived-metrics/${row.id}/enable`)
  ElMessage.success('已启用')
  await load()
}

async function remove(row: Record<string, any>) {
  try {
    await ElMessageBox.confirm(`删除后不可恢复，确认永久删除衍生指标「${row.name || row.metricCode}」？`, '删除确认', {
      type: 'error',
      confirmButtonText: '删除',
      confirmButtonClass: 'is-danger',
    })
  } catch {
    return
  }
  await http.delete(`/api/v1/open-api/derived-metrics/${row.id}`)
  ElMessage.success('已删除')
  if (rows.value.length === 1 && page.value > 1) page.value -= 1
  await load()
}

function parseList(raw: unknown): string[] {
  try {
    const parsed = typeof raw === 'string' ? JSON.parse(raw) : raw
    return Array.isArray(parsed) ? parsed.map(String) : []
  } catch {
    return []
  }
}

function formatValues(values: unknown): string {
  if (values == null) return '-'
  if (typeof values === 'string') return values
  return JSON.stringify(values)
}

onMounted(load)
</script>

<style scoped>
.panel { padding-top: 4px; }
.toolbar { display: flex; gap: 8px; align-items: center; margin-bottom: 12px; flex-wrap: wrap; }
.spacer { flex: 1; }
.pager { margin-top: 12px; justify-content: flex-end; }
</style>
