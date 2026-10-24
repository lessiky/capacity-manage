<template>
  <div class="page-card">
    <div class="toolbar">
      <div class="toolbar-title">数据管理</div>
      <div>
        <el-button :icon="Refresh" circle @click="loadActiveTab" />
      </div>
    </div>
    <el-tabs v-model="tab" @tab-change="loadActiveTab">
      <!-- 日数据 -->
      <el-tab-pane label="日数据" name="day">
        <div class="filter-row">
          <el-select v-model="dayQ.metricCode" placeholder="选择指标" filterable clearable style="width: 260px">
            <el-option v-for="m in metrics" :key="m.metricCode" :label="m.name + ' (' + m.metricCode + ')'" :value="m.metricCode" />
          </el-select>
          <el-date-picker v-model="dayQ.range" type="daterange" value-format="YYYY-MM-DD"
            start-placeholder="开始日期" end-placeholder="结束日期" style="width: 240px" />
          <el-button type="primary" :icon="Search" @click="searchDay">查询</el-button>
        </div>
        <el-table :data="dayRows" v-loading="dayLoading" border stripe size="default">
          <el-table-column prop="metricCode" label="指标" width="170" show-overflow-tooltip />
          <el-table-column prop="bizDate" label="业务日期" width="110">
            <template #default="{ row }">{{ String(row.bizDate).slice(0, 10) }}</template>
          </el-table-column>
          <el-table-column prop="dayMax" label="最大" width="100" align="right"><template #default="{ row }">{{ row.dayMax ?? '-' }}</template></el-table-column>
          <el-table-column prop="dayMin" label="最小" width="100" align="right"><template #default="{ row }">{{ row.dayMin ?? '-' }}</template></el-table-column>
          <el-table-column prop="dayAvg" label="平均" width="100" align="right"><template #default="{ row }">{{ row.dayAvg != null ? Number(row.dayAvg).toFixed(2) : '-' }}</template></el-table-column>
          <el-table-column prop="dayLast" label="最新" width="100" align="right"><template #default="{ row }">{{ row.dayLast ?? '-' }}</template></el-table-column>
          <el-table-column prop="statValue" label="统计值" width="100" align="right">
            <template #default="{ row }">
              {{ row.statValue ?? '-' }}
              <el-tag v-if="row.corrected" type="warning" size="small" style="margin-left: 4px" @click.stop="showCorrection(row)">已修正</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="水位" width="90" align="right">
            <template #default="{ row }">{{ row.waterLevel != null ? (row.waterLevel * 100).toFixed(1) + '%' : '-' }}</template>
          </el-table-column>
          <el-table-column prop="level" label="级别" width="90">
            <template #default="{ row }"><el-tag :type="levelTag(row.level)" size="small">{{ levelLabel(row.level) }}</el-tag></template>
          </el-table-column>
          <el-table-column prop="pointCount" label="点数" width="70" align="right" />
          <el-table-column label="操作" width="90" fixed="right">
            <template #default="{ row }">
              <el-button v-if="row.level !== 'missing'" link type="primary" size="small" @click="openCorrect(row)">修正</el-button>
            </template>
          </el-table-column>
        </el-table>
        <el-empty v-if="!dayLoading && dayRows.length === 0" description="暂无日数据（每日 00:10 定时汇总, 或经运维手工触发）" />
        <el-pagination
          v-if="dayTotal > 0"
          class="pager"
          background
          layout="total, sizes, prev, pager, next"
          :total="dayTotal"
          :current-page="dayQ.page"
          :page-size="dayQ.pageSize"
          :page-sizes="[50, 100, 200, 500]"
          @current-change="onDayPageChange"
          @size-change="onDaySizeChange"
        />
      </el-tab-pane>

      <!-- 日内数据 -->
      <el-tab-pane label="日内数据" name="rt">
        <div class="filter-row">
          <el-select v-model="rtQ.metricCode" placeholder="选择指标" filterable clearable style="width: 260px">
            <el-option v-for="m in metrics" :key="m.metricCode" :label="m.name + ' (' + m.metricCode + ')'" :value="m.metricCode" />
          </el-select>
          <el-date-picker v-model="rtQ.range" type="daterange" value-format="YYYY-MM-DD"
            start-placeholder="开始日期" end-placeholder="结束日期" style="width: 240px" />
          <el-button type="primary" :icon="Search" @click="searchRt">查询</el-button>
        </div>
        <el-table :data="rtRows" v-loading="rtLoading" border stripe size="default">
          <el-table-column prop="metricCode" label="指标" width="170" show-overflow-tooltip />
          <el-table-column prop="ts" label="时间" width="170"><template #default="{ row }">{{ fmtTs(row.ts) }}</template></el-table-column>
          <el-table-column prop="rawValue" label="原始值" width="120" align="right"><template #default="{ row }">{{ row.rawValue ?? '-' }}</template></el-table-column>
          <el-table-column prop="cleanValue" label="清洗值" width="120" align="right"><template #default="{ row }">{{ row.cleanValue ?? '-' }}</template></el-table-column>
          <el-table-column prop="judgeLevel" label="判定" width="90">
            <template #default="{ row }"><el-tag :type="levelTag(row.judgeLevel)" size="small">{{ levelLabel(row.judgeLevel) }}</el-tag></template>
          </el-table-column>
          <el-table-column prop="isAnomaly" label="异常" width="70" align="center">
            <template #default="{ row }">{{ row.isAnomaly ? '是' : '-' }}</template>
          </el-table-column>
          <el-table-column prop="anomalyReason" label="异常原因" min-width="160" show-overflow-tooltip />
          <el-table-column prop="source" label="来源" width="90" />
        </el-table>
        <el-empty v-if="!rtLoading && rtRows.length === 0" description="暂无日内数据" />
        <el-pagination
          v-if="rtTotal > 0"
          class="pager"
          background
          layout="total, sizes, prev, pager, next"
          :total="rtTotal"
          :current-page="rtQ.page"
          :page-size="rtQ.pageSize"
          :page-sizes="[50, 100, 200, 500]"
          @current-change="onRtPageChange"
          @size-change="onRtSizeChange"
        />
      </el-tab-pane>

      <!-- 修正记录 -->
      <el-tab-pane label="修正记录" name="corr">
        <div class="filter-row">
          <el-select v-model="corrQ.metricCode" placeholder="选择指标" filterable clearable style="width: 260px">
            <el-option v-for="m in metrics" :key="m.metricCode" :label="m.name + ' (' + m.metricCode + ')'" :value="m.metricCode" />
          </el-select>
          <el-button type="primary" :icon="Search" @click="searchCorr">查询</el-button>
        </div>
        <el-table :data="corrRows" v-loading="corrLoading" border stripe size="default">
          <el-table-column prop="createdAt" label="修正时间" width="170"><template #default="{ row }">{{ fmtTs(row.createdAt) }}</template></el-table-column>
          <el-table-column prop="metricCode" label="指标" width="180" show-overflow-tooltip />
          <el-table-column prop="bizDate" label="业务日期" width="110"><template #default="{ row }">{{ String(row.bizDate).slice(0, 10) }}</template></el-table-column>
          <el-table-column prop="field" label="字段" width="120">
            <template #default="{ row }">{{ row.field === 'stat_value' ? '统计值' : row.field === 'water_level' ? '水位' : row.field }}</template>
          </el-table-column>
          <el-table-column prop="oldValue" label="原值" width="120" align="right"><template #default="{ row }">{{ row.oldValue ?? '-' }}</template></el-table-column>
          <el-table-column prop="newValue" label="新值" width="120" align="right"><template #default="{ row }">{{ row.newValue ?? '-' }}</template></el-table-column>
          <el-table-column prop="reason" label="修正原因" min-width="220" show-overflow-tooltip />
          <el-table-column prop="operator" label="操作人" width="120" />
        </el-table>
        <el-empty v-if="!corrLoading && corrRows.length === 0" description="暂无修正记录" />
        <el-pagination
          v-if="corrTotal > 0"
          class="pager"
          background
          layout="total, sizes, prev, pager, next"
          :total="corrTotal"
          :current-page="corrQ.page"
          :page-size="corrQ.pageSize"
          :page-sizes="[20, 50, 100]"
          @current-change="onCorrPageChange"
          @size-change="onCorrSizeChange"
        />
      </el-tab-pane>

      <!-- 数据质量 -->
      <el-tab-pane label="质量评分" name="quality">
        <div class="filter-row">
          <el-select v-model="qualityQ.metricCode" placeholder="选择指标" filterable clearable style="width: 260px">
            <el-option v-for="m in metrics" :key="m.metricCode" :label="m.name + ' (' + m.metricCode + ')'" :value="m.metricCode" />
          </el-select>
          <el-date-picker v-model="qualityQ.range" type="daterange" value-format="YYYY-MM-DD"
            start-placeholder="开始日期" end-placeholder="结束日期" style="width: 240px" />
          <el-button type="primary" :icon="Search" @click="searchQuality">查询</el-button>
          <el-button :loading="qualityEvaluating" @click="evaluateQuality">重新评分</el-button>
        </div>
        <el-table :data="qualityRows" v-loading="qualityLoading" border stripe size="default">
          <el-table-column prop="metricCode" label="指标" width="180" show-overflow-tooltip />
          <el-table-column prop="bizDate" label="业务日期" width="110"><template #default="{ row }">{{ String(row.bizDate).slice(0, 10) }}</template></el-table-column>
          <el-table-column label="质量分" width="90" align="right">
            <template #default="{ row }"><span :class="{ bad: row.score < 0.8 }">{{ Number(row.score).toFixed(4) }}</span></template>
          </el-table-column>
          <el-table-column label="完整性" width="90" align="right"><template #default="{ row }">{{ pct4(row.completeness) }}</template></el-table-column>
          <el-table-column label="及时性" width="90" align="right"><template #default="{ row }">{{ pct4(row.timeliness) }}</template></el-table-column>
          <el-table-column label="异常率" width="90" align="right"><template #default="{ row }">{{ pct4(row.anomalyRate) }}</template></el-table-column>
          <el-table-column label="重复率" width="90" align="right"><template #default="{ row }">{{ pct4(row.duplicateRate) }}</template></el-table-column>
          <el-table-column label="有效/总点" width="100" align="right"><template #default="{ row }">{{ row.validPoints }}/{{ row.totalPoints }}</template></el-table-column>
          <el-table-column label="质量标记" min-width="220">
            <template #default="{ row }">
              <el-tag v-for="f in parseFlags(row.flags)" :key="f" type="warning" size="small" class="flag">{{ flagLabel(f) }}</el-tag>
              <span v-if="parseFlags(row.flags).length === 0" class="dim">正常</span>
            </template>
          </el-table-column>
        </el-table>
        <el-pagination
          class="pager"
          :total="qualityTotal"
          :current-page="qualityQ.page"
          :page-size="qualityQ.pageSize"
          :page-sizes="[50, 100, 200, 500]"
          layout="total, sizes, prev, pager, next"
          @current-change="onQualityPageChange"
          @size-change="onQualitySizeChange"
        />
        <el-empty v-if="!qualityLoading && qualityRows.length === 0" description="暂无质量评分，可选择指标后重新评分或等待日调度" />
      </el-tab-pane>

      <!-- 历史补采 -->
      <el-tab-pane label="历史补采" name="backfill">
        <div class="filter-row">
          <el-select v-model="backfillQ.flowId" placeholder="选择采集流程" filterable clearable style="width: 300px">
            <el-option v-for="f in flows" :key="f.id" :label="`${f.name || f.flowCode} (${f.metricCode})`" :value="f.id" />
          </el-select>
          <el-date-picker v-model="backfillQ.range" type="daterange" value-format="YYYY-MM-DD"
            start-placeholder="开始日期" end-placeholder="结束日期" style="width: 240px" />
          <el-input v-model="backfillQ.reason" placeholder="补采原因（审计必填）" style="width: 240px" />
          <el-button type="primary" :loading="backfillRunning" @click="runBackfill">开始补采</el-button>
          <el-button :icon="Refresh" circle @click="loadBackfills" />
        </div>
        <el-table :data="backfills" v-loading="backfillLoading" border stripe size="default" @row-click="openBackfillDetail">
          <el-table-column prop="id" label="任务" width="80" />
          <el-table-column prop="metricCode" label="指标" width="180" show-overflow-tooltip />
          <el-table-column prop="flowId" label="流程" width="80" align="right" />
          <el-table-column label="日期范围" width="210"><template #default="{ row }">{{ date10(row.fromDate) }} ~ {{ date10(row.toDate) }}</template></el-table-column>
          <el-table-column prop="status" label="状态" width="100">
            <template #default="{ row }"><el-tag :type="row.status === 'success' ? 'success' : row.status === 'partial' ? 'warning' : row.status === 'failed' ? 'danger' : 'info'">{{ row.status }}</el-tag></template>
          </el-table-column>
          <el-table-column label="成功/失败" width="110" align="right"><template #default="{ row }">{{ row.successDays }}/{{ row.failedDays }}</template></el-table-column>
          <el-table-column prop="createdBy" label="操作人" width="110" />
          <el-table-column prop="error" label="错误" min-width="180" show-overflow-tooltip />
          <el-table-column label="开始时间" width="170"><template #default="{ row }">{{ fmtTs(row.createdAt) }}</template></el-table-column>
        </el-table>
        <el-pagination
          v-if="backfillTotal > 0"
          class="pager"
          background
          layout="total, prev, pager, next"
          :total="backfillTotal"
          :current-page="backfillQ.page"
          :page-size="backfillQ.pageSize"
          @current-change="onBackfillPageChange"
        />
      </el-tab-pane>
    </el-tabs>

    <!-- 修正对话框 -->
    <el-dialog v-model="corrDialog" title="手工修正日数据" width="480px">
      <div v-if="corrTarget" class="corr-meta">
        指标 <b>{{ corrTarget.metricCode }}</b> · 业务日期 <b>{{ String(corrTarget.bizDate).slice(0, 10) }}</b>
        <span v-if="corrTarget.corrected" class="dim">（此前已修正, 原值保留于审计）</span>
      </div>
      <el-form label-width="84px">
        <el-form-item label="修正字段">
          <el-radio-group v-model="corrForm.field">
            <el-radio value="stat_value">统计值</el-radio>
            <el-radio value="water_level">水位(0~1)</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="新值">
          <el-input-number v-model="corrForm.newValue" :precision="6" :step="1" controls-position="right" style="width: 200px" />
        </el-form-item>
        <el-form-item label="修正原因">
          <el-input v-model="corrForm.reason" type="textarea" :rows="3" placeholder="必填, 至少 5 个字符（如: 采集脚本误算, 人工核对后修正）" maxlength="500" show-word-limit />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="corrDialog = false">取消</el-button>
        <el-button type="primary" :loading="corrActing" @click="doCorrect">提交修正</el-button>
      </template>
    </el-dialog>

    <!-- 原值快照查看 -->
    <el-dialog v-model="snapDialog" title="修正前原值快照" width="520px">
      <pre class="snap">{{ snapText }}</pre>
    </el-dialog>

    <el-dialog v-model="backfillDetailDialog" title="补采明细" width="760px">
      <el-descriptions v-if="backfillDetail" :column="3" border size="small" class="desc">
        <el-descriptions-item label="任务">{{ backfillDetail.run.id }}</el-descriptions-item>
        <el-descriptions-item label="指标">{{ backfillDetail.run.metricCode }}</el-descriptions-item>
        <el-descriptions-item label="状态">{{ backfillDetail.run.status }}</el-descriptions-item>
      </el-descriptions>
      <el-table :data="backfillDetail?.items || []" border stripe size="small">
        <el-table-column prop="bizDate" label="业务日" width="120"><template #default="{ row }">{{ date10(row.bizDate) }}</template></el-table-column>
        <el-table-column prop="status" label="状态" width="100" />
        <el-table-column label="日汇总" width="90"><template #default="{ row }">{{ row.summaryRefreshed ? '已刷新' : '-' }}</template></el-table-column>
        <el-table-column label="质量分" width="90"><template #default="{ row }">{{ row.qualityRefreshed ? '已重算' : '-' }}</template></el-table-column>
        <el-table-column label="告警重放" width="100"><template #default="{ row }">{{ row.alertReplayed ? '已重放' : '-' }}</template></el-table-column>
        <el-table-column label="报表失效" width="100"><template #default="{ row }">{{ row.reportInvalidated ? '已标记' : '-' }}</template></el-table-column>
        <el-table-column prop="error" label="错误" min-width="180" show-overflow-tooltip />
      </el-table>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { Refresh, Search } from '@element-plus/icons-vue'
import { ElMessage } from 'element-plus'
import { http } from '@/api/http'
import { fmtTs } from '@/utils/format'

const tab = ref('day')
const metrics = ref<Record<string, any>[]>([])
const flows = ref<Record<string, any>[]>([])

async function loadMetrics() {
  const d = (await http.get('/api/v1/metrics', { params: { pageSize: 500 } })) as any
  metrics.value = d.items ?? []
}

async function loadFlows() {
  const d = (await http.get('/api/v1/flows', { params: { pageSize: 500, status: 'active' } })) as any
  flows.value = d.items ?? []
}

// ---------- 日数据 ----------
const dayRows = ref<Record<string, any>[]>([])
const dayTotal = ref(0)
const dayLoading = ref(false)
const dayQ = reactive<{ metricCode: string; range: string[] | null; page: number; pageSize: number }>({ metricCode: '', range: null, page: 1, pageSize: 100 })

function defaultRange(): string[] {
  const to = new Date(), from = new Date()
  from.setDate(from.getDate() - 13)
  const f = (d: Date) => d.toISOString().slice(0, 10)
  return [f(from), f(to)]
}

async function loadDay() {
  dayLoading.value = true
  try {
    const params: Record<string, unknown> = { page: dayQ.page, pageSize: dayQ.pageSize }
    if (dayQ.metricCode) params.metricCodes = dayQ.metricCode
    if (dayQ.range?.length === 2) { params.from = dayQ.range[0]; params.to = dayQ.range[1] }
    const res = (await http.get('/api/v1/data/day', { params })) as unknown as { items: Record<string, any>[]; total: number }
    dayRows.value = res?.items ?? []
    dayTotal.value = res?.total ?? 0
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : String(e))
  } finally { dayLoading.value = false }
}

function searchDay() { dayQ.page = 1; loadDay() }
function onDayPageChange(p: number) { dayQ.page = p; loadDay() }
function onDaySizeChange(s: number) { dayQ.pageSize = s; dayQ.page = 1; loadDay() }

// ---------- 修正 ----------
const corrDialog = ref(false)
const corrActing = ref(false)
const corrTarget = ref<Record<string, any> | null>(null)
const corrForm = reactive<{ field: string; newValue: number | null; reason: string }>({ field: 'stat_value', newValue: null, reason: '' })

function openCorrect(row: Record<string, any>) {
  corrTarget.value = row
  corrForm.field = 'stat_value'
  corrForm.newValue = row.statValue ?? null
  corrForm.reason = ''
  corrDialog.value = true
}

async function doCorrect() {
  if (!corrTarget.value) return
  if (corrForm.newValue == null) { ElMessage.warning('请填写新值'); return }
  if (corrForm.reason.trim().length < 5) { ElMessage.warning('修正原因必填且不少于 5 个字符'); return }
  corrActing.value = true
  try {
    const res = (await http.post('/api/v1/data/corrections', {
      metricCode: corrTarget.value.metricCode,
      bizDate: String(corrTarget.value.bizDate).slice(0, 10),
      field: corrForm.field,
      newValue: corrForm.newValue,
      reason: corrForm.reason.trim(),
    })) as any
    corrDialog.value = false
    ElMessage.success(res?.refreshHint || '修正成功（原值已保留, 操作已审计）')
    loadDay()
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : String(e))
  } finally { corrActing.value = false }
}

const snapDialog = ref(false)
const snapText = ref('')
function showCorrection(row: Record<string, any>) {
  try {
    snapText.value = typeof row.originalSnapshot === 'string'
      ? JSON.stringify(JSON.parse(row.originalSnapshot), null, 2)
      : JSON.stringify(row.originalSnapshot ?? row, null, 2)
  } catch { snapText.value = String(row.originalSnapshot) }
  snapDialog.value = true
}

// ---------- 日内数据 ----------
const rtRows = ref<Record<string, any>[]>([])
const rtLoading = ref(false)
const rtTotal = ref(0)
const rtQ = reactive<{ metricCode: string; range: string[] | null; page: number; pageSize: number }>({ metricCode: '', range: null, page: 1, pageSize: 100 })

async function loadRt() {
  rtLoading.value = true
  try {
    const params: Record<string, unknown> = { page: rtQ.page, pageSize: rtQ.pageSize }
    if (rtQ.metricCode) params.metricCodes = rtQ.metricCode
    if (rtQ.range?.length === 2) { params.from = rtQ.range[0]; params.to = rtQ.range[1] }
    const res = (await http.get('/api/v1/data/rt', { params })) as unknown as { items: Record<string, any>[]; total: number }
    rtRows.value = res?.items ?? []
    rtTotal.value = res?.total ?? 0
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : String(e))
  } finally { rtLoading.value = false }
}

function searchRt() { rtQ.page = 1; loadRt() }
function onRtPageChange(p: number) { rtQ.page = p; loadRt() }
function onRtSizeChange(s: number) { rtQ.pageSize = s; rtQ.page = 1; loadRt() }

// ---------- 修正记录 ----------
const corrRows = ref<Record<string, any>[]>([])
const corrLoading = ref(false)
const corrTotal = ref(0)
const corrQ = reactive<{ metricCode: string; page: number; pageSize: number }>({ metricCode: '', page: 1, pageSize: 20 })
async function loadCorr() {
  corrLoading.value = true
  try {
    const params: Record<string, unknown> = { page: corrQ.page, pageSize: corrQ.pageSize }
    if (corrQ.metricCode) params.metricCode = corrQ.metricCode
    const res = (await http.get('/api/v1/data/corrections', { params })) as unknown as { items: Record<string, any>[]; total: number }
    corrRows.value = res?.items ?? []
    corrTotal.value = res?.total ?? 0
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : String(e))
  } finally { corrLoading.value = false }
}

function searchCorr() { corrQ.page = 1; loadCorr() }
function onCorrPageChange(p: number) { corrQ.page = p; loadCorr() }
function onCorrSizeChange(s: number) { corrQ.pageSize = s; corrQ.page = 1; loadCorr() }

// ---------- 质量评分 ----------
const qualityRows = ref<Record<string, any>[]>([])
const qualityLoading = ref(false)
const qualityEvaluating = ref(false)
const qualityTotal = ref(0)
const qualityQ = reactive<{ metricCode: string; range: string[] | null; page: number; pageSize: number }>({
  metricCode: '', range: null, page: 1, pageSize: 100,
})

async function loadQuality() {
  qualityLoading.value = true
  try {
    const params: Record<string, unknown> = { page: qualityQ.page, pageSize: qualityQ.pageSize }
    if (qualityQ.metricCode) params.metricCode = qualityQ.metricCode
    if (qualityQ.range?.length === 2) { params.from = qualityQ.range[0]; params.to = qualityQ.range[1] }
    const data = (await http.get('/api/v1/data/quality', { params })) as unknown as
      | { items: Record<string, any>[]; total: number }
      | Record<string, any>[]
    if (Array.isArray(data)) {
      // 兼容旧响应(不带分页时后端返回数组)
      qualityRows.value = data
      qualityTotal.value = data.length
    } else {
      qualityRows.value = data.items ?? []
      qualityTotal.value = data.total ?? qualityRows.value.length
    }
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : String(e))
  } finally { qualityLoading.value = false }
}

function searchQuality() {
  qualityQ.page = 1
  loadQuality()
}

function onQualityPageChange(p: number) {
  qualityQ.page = p
  loadQuality()
}

function onQualitySizeChange(s: number) {
  qualityQ.pageSize = s
  qualityQ.page = 1
  loadQuality()
}

async function evaluateQuality() {
  if (!qualityQ.metricCode || qualityQ.range?.length !== 2) {
    ElMessage.warning('请选择指标和日期范围')
    return
  }
  qualityEvaluating.value = true
  try {
    const res = (await http.post('/api/v1/data/quality/evaluate', {
      metricCode: qualityQ.metricCode, from: qualityQ.range[0], to: qualityQ.range[1],
    })) as any
    ElMessage.success(`质量分已更新: ${Number(res?.score ?? 0).toFixed(4)}`)
    await loadQuality()
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : String(e))
  } finally { qualityEvaluating.value = false }
}

// ---------- 历史补采 ----------
const backfills = ref<Record<string, any>[]>([])
const backfillTotal = ref(0)
const backfillLoading = ref(false)
const backfillRunning = ref(false)
const backfillDetailDialog = ref(false)
const backfillDetail = ref<{ run: Record<string, any>; items: Record<string, any>[] } | null>(null)
const backfillQ = reactive<{ flowId: number | null; range: string[] | null; reason: string; page: number; pageSize: number }>({
  flowId: null, range: null, reason: '', page: 1, pageSize: 20,
})

async function loadBackfills() {
  backfillLoading.value = true
  try {
    const res = (await http.get('/api/v1/data/backfill', {
      params: { page: backfillQ.page, pageSize: backfillQ.pageSize },
    })) as unknown as { items: Record<string, any>[]; total: number }
    backfills.value = res?.items ?? []
    backfillTotal.value = res?.total ?? 0
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : String(e))
  } finally { backfillLoading.value = false }
}

async function runBackfill() {
  if (!backfillQ.flowId || backfillQ.range?.length !== 2) {
    ElMessage.warning('请选择流程和补采日期范围')
    return
  }
  if (backfillQ.reason.trim().length < 2) {
    ElMessage.warning('请填写补采原因')
    return
  }
  backfillRunning.value = true
  try {
    const res = (await http.post('/api/v1/data/backfill', {
      flowId: backfillQ.flowId, from: backfillQ.range[0], to: backfillQ.range[1],
      source: 'backfill', reason: backfillQ.reason.trim(),
    })) as any
    ElMessage.success(`补采完成: 成功 ${res?.successDays ?? 0} 天，失败 ${res?.failedDays ?? 0} 天`)
    await Promise.all([loadBackfills(), loadDay(), loadQuality()])
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : String(e))
  } finally { backfillRunning.value = false }
}

async function openBackfillDetail(row: Record<string, any>) {
  try {
    backfillDetail.value = (await http.get(`/api/v1/data/backfill/${row.id}`)) as unknown as { run: Record<string, any>; items: Record<string, any>[] }
    backfillDetailDialog.value = true
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : String(e))
  }
}

function onBackfillPageChange(p: number) { backfillQ.page = p; loadBackfills() }

function loadActiveTab() {
  dayQ.page = 1
  rtQ.page = 1
  corrQ.page = 1
  if (tab.value === 'day') loadDay()
  else if (tab.value === 'rt') loadRt()
  else if (tab.value === 'corr') loadCorr()
  else if (tab.value === 'quality') loadQuality()
  else loadBackfills()
}

function levelTag(l: string): string {
  return ({ normal: 'success', warn: 'warning', danger: 'danger', missing: 'info' } as Record<string, string>)[l] || 'info'
}
function levelLabel(l: string): string {
  return ({ normal: '正常', warn: '预警', danger: '危险', missing: '缺数' } as Record<string, string>)[l] || l
}
function pct4(v: number | null | undefined): string {
  return v == null ? '-' : (Number(v) * 100).toFixed(2) + '%'
}
function parseFlags(v: unknown): string[] {
  try {
    const parsed = typeof v === 'string' ? JSON.parse(v) : v
    return Array.isArray(parsed) ? parsed.map(String) : []
  } catch { return [] }
}
function flagLabel(v: string): string {
  return ({
    no_data: '无数据', missing_days: '缺天', late_days: '不及时',
    anomaly_ratio: '异常偏高', duplicates: '重复值', stale: '数据陈旧',
  } as Record<string, string>)[v] || v
}
function date10(v: unknown): string {
  return String(v ?? '').slice(0, 10)
}

onMounted(() => {
  dayQ.range = defaultRange()
  rtQ.range = defaultRange()
  qualityQ.range = defaultRange()
  backfillQ.range = defaultRange()
  Promise.all([loadMetrics(), loadFlows()]).then(() => loadDay())
})
</script>

<style scoped>
.toolbar { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; }
.toolbar-title { font-size: 15px; font-weight: 600; }
.filter-row { display: flex; align-items: center; gap: 8px; margin-bottom: 12px; flex-wrap: wrap; }
.pager { margin-top: 12px; justify-content: flex-end; }
.dim { color: #909399; font-size: 12px; margin-left: 8px; }
.corr-meta { font-size: 13px; margin-bottom: 12px; }
.snap { background: #fafbfc; border: 1px solid #ebeef5; border-radius: 6px; padding: 12px; font-size: 12px; overflow: auto; max-height: 400px; }
.bad { color: #f56c6c; font-weight: 700; }
.flag { margin-right: 4px; }
.desc { margin-bottom: 12px; }
</style>

