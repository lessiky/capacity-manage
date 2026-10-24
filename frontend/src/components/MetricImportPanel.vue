<template>
  <div class="panel">
    <div class="toolbar">
      <input ref="fileInput" type="file" accept=".csv,.json" />
      <el-button type="primary" :icon="Upload" :loading="importing" @click="importFile">上传导入</el-button>
      <span class="hint">CSV 表头: metricCode,name,systemCode,unit,judgeDirection,warnLine,dangerLine；也支持 JSON 数组</span>
      <div class="spacer" />
      <el-button :icon="Refresh" circle @click="load" />
    </div>

    <el-table :data="rows" v-loading="loading" border stripe empty-text="暂无导入记录">
      <el-table-column prop="filename" label="文件" min-width="220" show-overflow-tooltip />
      <el-table-column prop="totalRows" label="总行数" width="90" align="right" />
      <el-table-column prop="successRows" label="成功" width="90" align="right" />
      <el-table-column prop="failedRows" label="失败" width="90" align="right" />
      <el-table-column label="状态" width="100">
        <template #default="{ row }">
          <el-tag :type="row.status === 'success' ? 'success' : row.status === 'failed' ? 'danger' : 'warning'" size="small">
            {{ statusLabel(row.status) }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="时间" width="180">
        <template #default="{ row }">{{ fmtTs(row.createdAt) }}</template>
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
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { ElMessage } from 'element-plus'
import { Refresh, Upload } from '@element-plus/icons-vue'
import { http } from '@/api/http'
import { fmtTs } from '@/utils/format'

const rows = ref<Record<string, any>[]>([])
const loading = ref(false)
const importing = ref(false)
const fileInput = ref<HTMLInputElement | null>(null)
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)

async function load() {
  loading.value = true
  try {
    const data = (await http.get('/api/v1/open-api/metrics/imports', {
      params: { page: page.value, pageSize: pageSize.value },
    })) as { items?: Record<string, any>[]; total?: number }
    rows.value = data.items ?? []
    total.value = data.total ?? rows.value.length
  } finally {
    loading.value = false
  }
}

function onPageChange(p: number) {
  page.value = p
  load()
}

function onSizeChange(size: number) {
  pageSize.value = size
  page.value = 1
  load()
}

async function importFile() {
  const input = fileInput.value
  const file = input?.files?.[0]
  if (!file) {
    ElMessage.warning('请选择 CSV 或 JSON 文件')
    return
  }
  const formData = new FormData()
  formData.append('file', file)
  importing.value = true
  try {
    const data = (await http.post('/api/v1/open-api/metrics/import', formData)) as Record<string, any>
    ElMessage.success(`导入完成: 成功 ${data.success} / 失败 ${data.failed}`)
    page.value = 1
    await load()
  } finally {
    importing.value = false
    if (input) input.value = ''
  }
}

function statusLabel(status: string) {
  return ({ success: '成功', partial: '部分成功', failed: '失败' } as Record<string, string>)[status] || status
}

onMounted(load)
</script>

<style scoped>
.panel { padding-top: 4px; }
.toolbar { display: flex; gap: 8px; align-items: center; margin-bottom: 12px; flex-wrap: wrap; }
.spacer { flex: 1; }
.hint { font-size: 12px; color: var(--el-text-color-secondary); }
.pager { margin-top: 12px; justify-content: flex-end; }
</style>
