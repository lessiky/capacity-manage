<template>
  <div class="retention-page">
    <div class="toolbar">
      <div class="toolbar-title">
        数据保留
        <span class="sub">日内数据默认保留 90 天，日数据默认 3 年在线；超期按月分区压缩归档成功后再从在线库下线，可按需恢复到在线库</span>
      </div>
      <div class="toolbar-right">
        <el-button type="primary" :loading="archiving" @click="runArchive">立即归档</el-button>
        <el-button :icon="Refresh" circle @click="reload" />
      </div>
    </div>

    <el-form :inline="true" class="retention-form">
      <el-form-item label="日内保留(天)">
        <el-input-number v-model="retention.rtRetainDays" :min="1" :max="3650" />
      </el-form-item>
      <el-form-item label="日数据在线(天)">
        <el-input-number v-model="retention.dayRetainDays" :min="30" :max="36500" />
      </el-form-item>
      <el-form-item>
        <el-button @click="saveRetention">保存</el-button>
      </el-form-item>
    </el-form>

    <div class="summary-row">
      <div class="run-card" v-for="s in retentionStats" :key="s.status">
        <div class="run-label">{{ archiveStatusLabel(s.status) }}</div>
        <div class="run-value">{{ s.count }}</div>
        <div class="run-sub">{{ fmtBytes(s.bytes) }}</div>
      </div>
      <div class="run-card">
        <div class="run-label">最近归档</div>
        <div class="run-value">{{ retention.lastRun ? dateOnly(retention.lastRun.fromDate) : '暂无' }}</div>
        <div class="run-sub">
          {{ retention.lastRun ? `${retention.lastRun.partition} · ${retention.lastRun.rowCount} 行` : '尚未执行归档' }}
        </div>
      </div>
    </div>

    <el-table :data="archives" v-loading="loading" border stripe size="small" empty-text="暂无归档记录">
      <el-table-column prop="partition" label="分区" min-width="200" show-overflow-tooltip />
      <el-table-column label="统计周期" width="200">
        <template #default="{ row }">{{ dateOnly(row.fromDate) }} ~ {{ dateOnly(row.toDate) }}</template>
      </el-table-column>
      <el-table-column prop="rowCount" label="行数" width="110" align="right" />
      <el-table-column label="大小" width="100" align="right">
        <template #default="{ row }">{{ fmtBytes(row.bytes) }}</template>
      </el-table-column>
      <el-table-column label="状态" width="100">
        <template #default="{ row }">
          <el-tag :type="row.status === 'success' ? 'success' : row.status === 'failed' ? 'danger' : 'warning'" size="small">
            {{ archiveStatusLabel(row.status) }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="完成时间" width="170">
        <template #default="{ row }">{{ fmtTs(row.finishedAt) }}</template>
      </el-table-column>
      <el-table-column label="在线状态" width="110">
        <template #default="{ row }">
          <el-tag :type="row.onlineDropped ? 'info' : 'success'" size="small">
            {{ row.onlineDropped ? '已下线' : '在线保留' }}
          </el-tag>
        </template>
      </el-table-column>
      <el-table-column label="操作" width="100">
        <template #default="{ row }">
          <el-button
            link
            type="primary"
            size="small"
            :loading="restoringId === row.id"
            :disabled="!row.onlineDropped"
            @click="restoreArchive(row)"
          >恢复</el-button>
        </template>
      </el-table-column>
    </el-table>
    <el-pagination
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
import { onMounted, reactive, ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Refresh } from '@element-plus/icons-vue'
import { http } from '@/api/http'
import { fmtTs } from '@/utils/format'

const loading = ref(false)
const archiving = ref(false)
const restoringId = ref(0)
const archives = ref<Record<string, any>[]>([])
const retentionStats = ref<{ status: string; count: number; bytes: number }[]>([])
const retention = reactive({ rtRetainDays: 90, dayRetainDays: 1095, lastRun: null as any })
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)

async function loadArchives() {
  const list = (await http.get('/api/v1/open-api/archives', {
    params: { page: page.value, pageSize: pageSize.value },
  })) as unknown as { items: Record<string, any>[]; total: number }
  archives.value = list.items ?? []
  total.value = list.total ?? archives.value.length
}

async function loadRetention() {
  loading.value = true
  try {
    const data = (await http.get('/api/v1/open-api/retention')) as Record<string, any>
    retention.rtRetainDays = data.rtRetainDays
    retention.dayRetainDays = data.dayRetainDays
    retention.lastRun = data.lastRun
    retentionStats.value = data.archiveStats ?? []
    await loadArchives()
  } finally {
    loading.value = false
  }
}

function reload() {
  loadRetention()
}

function onPageChange(p: number) {
  page.value = p
  loadArchives()
}

function onSizeChange(s: number) {
  pageSize.value = s
  page.value = 1
  loadArchives()
}

async function saveRetention() {
  await http.put('/api/v1/open-api/retention', {
    rtRetainDays: retention.rtRetainDays, dayRetainDays: retention.dayRetainDays,
  })
  ElMessage.success('已保存')
  await loadRetention()
}

async function runArchive() {
  archiving.value = true
  try {
    const data = (await http.post('/api/v1/open-api/archives/run')) as { archivedPartitions: number; rtArchivedPartitions: number }
    ElMessage.success(`已归档下线 日内 ${data.rtArchivedPartitions} / 日数据 ${data.archivedPartitions} 个分区`)
    page.value = 1
    await loadRetention()
  } finally {
    archiving.value = false
  }
}

async function restoreArchive(row: Record<string, any>) {
  await ElMessageBox.confirm(`从归档恢复分区「${row.partition}」到在线库？`, '提示', { type: 'warning' })
  restoringId.value = row.id
  try {
    const data = (await http.post(`/api/v1/open-api/archives/${row.id}/restore`)) as { restoredRows: number }
    ElMessage.success(`已恢复 ${data.restoredRows} 行到在线库`)
    await loadRetention()
  } finally {
    restoringId.value = 0
  }
}

function archiveStatusLabel(s: string) {
  return s === 'success' ? '成功' : s === 'failed' ? '失败' : s === 'restored' ? '已恢复' : '进行中'
}

function dateOnly(v?: string | null) {
  return v ? String(v).slice(0, 10) : '-'
}

function fmtBytes(n?: number | null) {
  if (n == null) return '-'
  if (n < 1024) return n + ' B'
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return v.toFixed(1) + ' ' + units[i]
}

onMounted(loadRetention)
</script>

<style scoped>
.toolbar { display: flex; justify-content: space-between; align-items: flex-start; gap: 12px; flex-wrap: wrap; margin-bottom: 12px; }
.toolbar-title { font-size: 15px; font-weight: 600; }
.toolbar-title .sub { display: block; margin-top: 4px; font-size: 12px; font-weight: 400; color: var(--el-text-color-secondary); }
.toolbar-right { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.retention-form { margin-bottom: 8px; }
.summary-row { display: grid; grid-template-columns: repeat(4, minmax(170px, 1fr)); gap: 12px; margin-bottom: 12px; }
.run-card { border: 1px solid var(--el-border-color-light); border-radius: 8px; padding: 12px 14px; }
.run-label { font-size: 12px; color: var(--el-text-color-secondary); }
.run-value { font-size: 22px; font-weight: 600; margin: 4px 0; }
.run-sub { font-size: 12px; color: var(--el-text-color-secondary); }
.pager { margin-top: 12px; justify-content: flex-end; }
</style>
