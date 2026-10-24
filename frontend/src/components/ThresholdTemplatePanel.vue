<template>
  <div class="panel">
    <div class="toolbar">
      <el-input v-model="form.name" placeholder="模板名称" style="width: 180px" />
      <el-select v-model="form.judgeDirection" style="width: 150px">
        <el-option label="越高越危险" value="higher_worse" />
        <el-option label="越低越危险" value="lower_worse" />
      </el-select>
      <el-input-number v-model="form.warnLine" :min="0" :controls="false" placeholder="预警线" style="width: 120px" />
      <el-input-number v-model="form.dangerLine" :min="0" :controls="false" placeholder="危险线" style="width: 120px" />
      <el-button type="primary" :icon="Plus" @click="create">新增模板</el-button>
      <div class="spacer" />
      <el-button :icon="Refresh" circle @click="load" />
    </div>

    <el-table :data="rows" v-loading="loading" border stripe empty-text="暂无阈值模板">
      <el-table-column prop="name" label="模板" width="200" />
      <el-table-column label="方向" width="130">
        <template #default="{ row }">{{ row.judgeDirection === 'higher_worse' ? '越高越危险' : '越低越危险' }}</template>
      </el-table-column>
      <el-table-column prop="warnLine" label="预警线" width="110" />
      <el-table-column prop="dangerLine" label="危险线" width="110" />
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag :type="row.status === 'active' ? 'success' : 'info'" size="small">
            {{ row.status === 'active' ? '启用' : '停用' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="套用到指标" min-width="280">
        <template #default="{ row }">
          <div v-if="row.status === 'active'" class="apply-row">
            <el-input v-model="applyTarget[row.id]" placeholder="指标编码, 逗号分隔" size="small" />
            <el-button link type="primary" size="small" @click="apply(row)">套用</el-button>
          </div>
          <span v-else class="disabled-tip">已停用，不可套用</span>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="150" fixed="right">
        <template #default="{ row }">
          <el-button v-if="row.status === 'active'" link type="warning" size="small" @click="disable(row)">停用</el-button>
          <template v-else>
            <el-button link type="success" size="small" @click="enable(row)">启用</el-button>
            <el-button link type="danger" size="small" @click="remove(row)">删除</el-button>
          </template>
        </template>
      </el-table-column>
    </el-table>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh } from '@element-plus/icons-vue'
import { http } from '@/api/http'

const rows = ref<Record<string, any>[]>([])
const loading = ref(false)
const applyTarget = reactive<Record<number, string>>({})
const form = reactive({ name: '', judgeDirection: 'higher_worse', warnLine: 80, dangerLine: 90 })

async function load() {
  loading.value = true
  try {
    const data = (await http.get('/api/v1/open-api/threshold-templates')) as { items?: Record<string, any>[] }
    rows.value = data.items ?? []
  } finally {
    loading.value = false
  }
}

async function create() {
  if (!form.name.trim()) {
    ElMessage.warning('请填写模板名称')
    return
  }
  await http.post('/api/v1/open-api/threshold-templates', { ...form, name: form.name.trim() })
  ElMessage.success('已新增')
  form.name = ''
  await load()
}

async function apply(row: Record<string, any>) {
  const codes = (applyTarget[row.id] || '').split(',').map((s) => s.trim()).filter(Boolean)
  if (!codes.length) {
    ElMessage.warning('请填写指标编码')
    return
  }
  const data = (await http.post(`/api/v1/open-api/threshold-templates/${row.id}/apply`, { metricCodes: codes })) as { applied: number }
  ElMessage.success(`已套用 ${data.applied} 个指标`)
  applyTarget[row.id] = ''
}

async function disable(row: Record<string, any>) {
  try {
    await ElMessageBox.confirm(`确认停用阈值模板「${row.name}」？`, '提示', { type: 'warning' })
  } catch {
    return
  }
  await http.post(`/api/v1/open-api/threshold-templates/${row.id}/disable`)
  ElMessage.success('已停用')
  await load()
}

async function enable(row: Record<string, any>) {
  await http.post(`/api/v1/open-api/threshold-templates/${row.id}/enable`)
  ElMessage.success('已启用')
  await load()
}

async function remove(row: Record<string, any>) {
  try {
    await ElMessageBox.confirm(`删除后不可恢复，确认永久删除阈值模板「${row.name}」？`, '删除确认', {
      type: 'error',
      confirmButtonText: '删除',
      confirmButtonClass: 'is-danger',
    })
  } catch {
    return
  }
  await http.delete(`/api/v1/open-api/threshold-templates/${row.id}`)
  ElMessage.success('已删除')
  await load()
}

onMounted(load)
</script>

<style scoped>
.panel { padding-top: 4px; }
.toolbar { display: flex; gap: 8px; align-items: center; margin-bottom: 12px; flex-wrap: wrap; }
.spacer { flex: 1; }
.apply-row { display: flex; align-items: center; gap: 8px; }
.apply-row :deep(.el-input) { max-width: 220px; }
.disabled-tip { font-size: 12px; color: var(--el-text-color-secondary); }
</style>
