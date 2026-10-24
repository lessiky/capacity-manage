<template>
  <div :class="embedded ? 'embedded-page' : 'page-card'">
    <div class="toolbar">
      <el-input v-model="filterFlow" clearable placeholder="流程编码/指标" style="width: 180px" @keyup.enter="applyFilter" />
      <el-select v-model="filterStatus" clearable placeholder="状态" style="width: 120px" @change="applyFilter">
        <el-option label="成功" value="success" />
        <el-option label="失败" value="failed" />
        <el-option label="超时" value="timeout" />
        <el-option label="执行中" value="running" />
      </el-select>
      <el-select v-model="filterSource" clearable placeholder="来源" style="width: 120px" @change="applyFilter">
        <el-option label="自动调度" value="auto" />
        <el-option label="手工触发" value="manual" />
      </el-select>
      <el-date-picker v-model="filterDate" type="date" value-format="YYYY-MM-DD" placeholder="日期" style="width: 150px" @change="applyFilter" />
      <el-button type="primary" :icon="Search" @click="applyFilter">查询</el-button>
      <div style="flex: 1" />
      <el-button :icon="Refresh" circle @click="reload" />
    </div>

    <el-table :data="items" v-loading="loading" border stripe>
      <el-table-column prop="id" label="ID" width="70" />
      <el-table-column prop="flowCode" label="流程" min-width="140" show-overflow-tooltip />
      <el-table-column prop="metricCode" label="指标" min-width="130" show-overflow-tooltip />
      <el-table-column label="触发时间" width="170">
        <template #default="{ row }">{{ fmtTs(row.triggeredAt) }}</template>
      </el-table-column>
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag size="small" :type="statusTag(row.status)">{{ statusLabel(row.status) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="retryCount" label="重试" width="60" />
      <el-table-column prop="durationMs" label="耗时(ms)" width="90" />
      <el-table-column prop="rowsCollected" label="采集行数" width="90" />
      <el-table-column label="解析失败率" width="100">
        <template #default="{ row }">{{ row.parseFailRate != null ? (row.parseFailRate * 100).toFixed(1) + "%" : "-" }}</template>
      </el-table-column>
      <el-table-column prop="anomalyCount" label="异常数" width="80" />
      <el-table-column label="来源" width="90">
        <template #default="{ row }">{{ row.source === "auto" ? "自动" : row.source === "manual" ? "手工" : row.source }}</template>
      </el-table-column>
      <el-table-column prop="errorMsg" label="错误" min-width="180" show-overflow-tooltip />
      <el-table-column label="操作" width="80" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" @click="openDetail(row)">详情</el-button>
        </template>
      </el-table-column>
    </el-table>
    <el-empty v-if="!loading && error" :description="error" />
    <el-empty v-else-if="!loading && items.length === 0" description="暂无执行记录" />
    <el-pagination
      v-model:current-page="page"
      :page-size="pageSize"
      :total="total"
      layout="total, prev, pager, next"
      class="pager"
      @current-change="reload"
    />

    <!-- 任务详情抽屉 -->
    <el-drawer v-model="detailVisible" title="任务详情" size="620px">
      <template v-if="detail">
        <el-descriptions :column="2" border size="small">
          <el-descriptions-item label="流程">{{ detail.task.flowCode }}</el-descriptions-item>
          <el-descriptions-item label="指标">{{ detail.task.metricCode }}</el-descriptions-item>
          <el-descriptions-item label="触发时间">{{ fmtTs(detail.task.triggeredAt) }}</el-descriptions-item>
          <el-descriptions-item label="状态">
            <el-tag size="small" :type="statusTag(detail.task.status)">{{ statusLabel(detail.task.status) }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="重试次数">{{ detail.task.retryCount }}</el-descriptions-item>
          <el-descriptions-item label="耗时">{{ detail.task.durationMs ?? "-" }} ms</el-descriptions-item>
          <el-descriptions-item label="采集行数">{{ detail.task.rowsCollected ?? "-" }}</el-descriptions-item>
          <el-descriptions-item label="解析失败">{{ detail.task.parseFailCount ?? 0 }} / {{ (detail.task.parseFailRate ?? 0) * 100 }}%</el-descriptions-item>
          <el-descriptions-item label="异常值数">{{ detail.task.anomalyCount }}</el-descriptions-item>
          <el-descriptions-item label="来源">{{ detail.task.source === "auto" ? "自动调度" : detail.task.source }}</el-descriptions-item>
          <el-descriptions-item label="操作人">{{ detail.task.operator || "-" }}</el-descriptions-item>
          <el-descriptions-item label="结束时间">{{ fmtTs(detail.task.finishedAt) }}</el-descriptions-item>
        </el-descriptions>

        <template v-if="detail.task.errorMsg">
          <el-divider content-position="left">错误信息</el-divider>
          <el-alert type="error" :title="detail.task.errorMsg" :closable="false" />
        </template>

        <template v-if="detail.task.runLog">
          <el-divider content-position="left">执行日志 / 异常堆栈</el-divider>
          <pre class="runlog-pre">{{ detail.task.runLog }}</pre>
        </template>

        <el-divider content-position="left">清洗留痕（{{ detail.traces.length }} 行）</el-divider>
        <el-table v-if="detail.traces.length" :data="detail.traces" size="small" border max-height="300">
          <el-table-column type="index" label="#" width="50" />
          <el-table-column prop="rawValue" label="原始值" min-width="110" show-overflow-tooltip />
          <el-table-column prop="cleanValue" label="清洗值" width="100" />
          <el-table-column label="应用规则" min-width="150" show-overflow-tooltip>
            <template #default="{ row }">{{ rulesText(row.rulesApplied) }}</template>
          </el-table-column>
          <el-table-column label="异常" width="60">
            <template #default="{ row }">
              <el-tag v-if="row.isAnomaly" size="small" type="danger">是</el-tag>
              <span v-else>-</span>
            </template>
          </el-table-column>
          <el-table-column prop="anomalyReason" label="异常原因" min-width="130" show-overflow-tooltip />
        </el-table>
        <el-empty v-else description="无清洗留痕（预览或未执行）" :image-size="60" />
      </template>
    </el-drawer>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from "vue"
import { Search, Refresh } from "@element-plus/icons-vue"
import { http } from "@/api/http"
import { fmtTs } from "@/utils/format"
import { useList } from "@/composables/useList"

defineProps<{ embedded?: boolean }>()

interface TaskRow {
  id: number
  flowId: number
  flowCode: string
  metricCode: string
  triggeredAt: string
  finishedAt: string | null
  status: string
  durationMs: number | null
  rowsCollected: number | null
  parseFailCount: number
  parseFailRate: number | null
  anomalyCount: number
  errorMsg: string
  retryCount: number
  source: string
  operator: string
  runLog: string
}
interface TraceRow {
  id: number
  taskLogId: number
  metricCode: string
  rawValue: string
  cleanValue: number | null
  rulesApplied: string
  isAnomaly: boolean
  anomalyReason: string
}
interface Detail {
  task: TaskRow
  traces: TraceRow[]
}

const STATUS_LABELS: Record<string, string> = { running: "执行中", success: "成功", failed: "失败", timeout: "超时" }
const statusLabel = (s: string) => STATUS_LABELS[s] ?? s
const STATUS_TAGS: Record<string, string> = { running: "primary", success: "success", failed: "danger", timeout: "danger" }
const statusTag = (s: string) => STATUS_TAGS[s] ?? "info"

const filterFlow = ref("")
const filterStatus = ref("")
const filterSource = ref("")
const filterDate = ref("")
const { items, total, loading, error, page, pageSize, reload, params } = useList<TaskRow>("/api/v1/tasks")

function applyFilter() {
  params.value = {
    ...(filterFlow.value ? { flowCode: filterFlow.value } : {}),
    ...(filterStatus.value ? { status: filterStatus.value } : {}),
    ...(filterSource.value ? { source: filterSource.value } : {}),
    ...(filterDate.value ? { date: filterDate.value } : {}),
  }
  page.value = 1
  reload()
}

const detailVisible = ref(false)
const detail = ref<Detail | null>(null)
async function openDetail(row: TaskRow) {
  try {
    const r = (await http.get(`/api/v1/tasks/${row.id}`)) as unknown as { task: TaskRow; cleaningTraces: TraceRow[] }
    detail.value = { task: r.task, traces: r.cleaningTraces ?? [] }
    detailVisible.value = true
  } catch { /* 拦截器已提示 */ }
}
function rulesText(s: string) {
  try { return JSON.parse(s).join(", ") } catch { return s || "-" }
}

onMounted(reload)
</script>

<style scoped>
.embedded-page { min-width: 0; }
.toolbar { display: flex; gap: 8px; align-items: center; margin-bottom: 14px; }
.pager { margin-top: 12px; display: flex; justify-content: flex-end; }
.runlog-pre {
  max-height: 320px; overflow: auto; background: #1e1e1e; color: #d4d4d4;
  padding: 12px; border-radius: 4px; font-size: 12px; white-space: pre-wrap; word-break: break-all;
}
</style>
