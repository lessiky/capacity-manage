<template>
  <div class="page-card">
    <el-tabs v-model="tab">
      <el-tab-pane label="日报" name="daily">
    <div class="toolbar">
      <div class="toolbar-title">日报中心<span class="sub">当日值 / 水位 / 状态 + 超线清单 + 缺数清单 + 采集异常清单（数据权限按用户授权系统过滤）</span></div>
      <div class="toolbar-right">
        <el-date-picker v-model="date" type="date" value-format="YYYY-MM-DD" placeholder="业务日" style="width: 150px" />
        <el-button type="primary" :loading="loading" @click="load">加载日报</el-button>
        <el-button :icon="Download" :disabled="!date" @click="doExport('xlsx')">导出 Excel</el-button>
        <el-button :icon="Download" :disabled="!date" @click="doExport('pdf')">导出 PDF</el-button>
      </div>
    </div>

    <template v-if="report">
      <div class="stat-row">
        <div class="stat-card" style="border-top-color: var(--cap-primary)">
          <div class="stat-label">指标总数</div>
          <div class="stat-value">{{ report.metricsTotal ?? report.metrics.length }}</div>
        </div>
        <div class="stat-card" style="border-top-color: var(--cap-warn)">
          <div class="stat-label">超预警</div>
          <div class="stat-value" style="color: var(--cap-warn)">{{ report.statusCounts?.warn ?? 0 }}</div>
        </div>
        <div class="stat-card" style="border-top-color: var(--cap-danger)">
          <div class="stat-label">超危险</div>
          <div class="stat-value" style="color: var(--cap-danger)">{{ report.statusCounts?.danger ?? 0 }}</div>
        </div>
        <div class="stat-card" style="border-top-color: #e6455c">
          <div class="stat-label">缺数</div>
          <div class="stat-value">{{ report.missingCount }}</div>
        </div>
        <div class="stat-card" style="border-top-color: #909399">
          <div class="stat-label">采集异常</div>
          <div class="stat-value">{{ report.collectFailCount }}</div>
        </div>
        <div class="stat-card" style="border-top-color: #7c3aed">
          <div class="stat-label">预测覆盖</div>
          <div class="stat-value" style="color: #7c3aed">{{ report.forecastCount ?? forecastRows.length }}</div>
        </div>
      </div>

      <div class="charts-row">
        <div class="chart-box">
          <div class="chart-title">状态分布（{{ report.bizDate }}）</div>
          <div ref="donutRef" class="chart" />
        </div>
        <div class="chart-box">
          <div class="chart-title">TOP 10 水位</div>
          <div ref="barRef" class="chart" />
        </div>
      </div>

      <div class="section-title">预测摘要（{{ forecastRows.length }}）</div>
      <el-table :data="forecastRows" border stripe size="small" max-height="300">
        <el-table-column prop="systemName" label="系统" width="150" show-overflow-tooltip />
        <el-table-column prop="name" label="指标" min-width="160" show-overflow-tooltip />
        <el-table-column prop="metricCode" label="指标编码" width="160" show-overflow-tooltip />
        <el-table-column prop="forecastModel" label="模型" width="90">
          <template #default="{ row }">{{ row.forecastModel || '-' }}</template>
        </el-table-column>
        <el-table-column label="状态" width="120">
          <template #default="{ row }">
            <el-tag size="small" :type="forecastTagType(row.forecastStatus)">{{ forecastStatusLabel(row.forecastStatus) }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="预警线 ETA" width="125">
          <template #default="{ row }">{{ dayOnly(row.etaWarnDate) }}</template>
        </el-table-column>
        <el-table-column label="危险线 ETA" width="125">
          <template #default="{ row }">{{ dayOnly(row.etaDangerDate) }}</template>
        </el-table-column>
        <el-table-column label="风险分" width="90" align="right">
          <template #default="{ row }">{{ row.riskScore == null ? '-' : Number(row.riskScore).toFixed(2) }}</template>
        </el-table-column>
      </el-table>

      <div class="section-title">超线清单（{{ report.overCount }}）</div>
      <el-table :data="overSorted" border stripe size="small" max-height="320">
        <el-table-column prop="systemName" label="系统" width="150" show-overflow-tooltip />
        <el-table-column prop="name" label="指标" min-width="160" show-overflow-tooltip />
        <el-table-column prop="metricCode" label="指标编码" width="160" show-overflow-tooltip />
        <el-table-column prop="unit" label="单位" width="70" />
        <el-table-column label="当前值" width="110">
          <template #default="{ row }">{{ fmtNum(row.statValue) }}</template>
        </el-table-column>
        <el-table-column label="水位" width="90">
          <template #default="{ row }">
            <span :style="{ color: levelColor(row.level), fontWeight: 600 }">{{ fmtPct(row.waterLevel) }}</span>
          </template>
        </el-table-column>
        <el-table-column label="预警线" width="90">
          <template #default="{ row }">{{ fmtNum(row.warnLine) }}</template>
        </el-table-column>
        <el-table-column label="危险线" width="90">
          <template #default="{ row }">{{ fmtNum(row.dangerLine) }}</template>
        </el-table-column>
        <el-table-column label="状态" width="90">
          <template #default="{ row }"><el-tag size="small" :type="row.level === 'danger' ? 'danger' : 'warning'">{{ row.level === 'danger' ? '超危险' : '超预警' }}</el-tag></template>
        </el-table-column>
      </el-table>

      <div class="section-title">缺数清单（{{ report.missingCount }}）<span v-if="report.missingCount > report.missingList.length" class="sub">仅显示前 {{ report.missingList.length }} 条</span></div>
      <el-table :data="report.missingList" border stripe size="small" max-height="240">
        <el-table-column prop="systemName" label="系统" width="150" show-overflow-tooltip />
        <el-table-column prop="name" label="指标" min-width="180" show-overflow-tooltip />
        <el-table-column prop="metricCode" label="指标编码" min-width="160" show-overflow-tooltip />
        <el-table-column label="类型" width="120">
          <template #default="{ row }">
            <el-tag size="small" :type="row.level === 'missing' ? 'danger' : 'info'">{{ row.level === 'missing' ? '缺数（应有数据）' : '无数据（建档后无采集）' }}</el-tag>
          </template>
        </el-table-column>
      </el-table>

      <div class="section-title">采集异常清单（{{ report.collectFailCount }}）</div>
      <el-table :data="report.collectFailures" border stripe size="small" max-height="240">
        <el-table-column prop="flowCode" label="流程" width="180" show-overflow-tooltip />
        <el-table-column prop="metricCode" label="指标" min-width="160" show-overflow-tooltip />
        <el-table-column prop="status" label="任务状态" width="100">
          <template #default="{ row }"><el-tag size="small" type="danger">{{ row.status }}</el-tag></template>
        </el-table-column>
        <el-table-column label="触发时间" width="170">
          <template #default="{ row }">{{ fmtTs(row.triggeredAt) }}</template>
        </el-table-column>
        <el-table-column prop="errorMsg" label="错误信息" min-width="200" show-overflow-tooltip />
      </el-table>

      <div class="section-title">级别变化（{{ report.levelChanges.length }}）</div>
      <el-table :data="report.levelChanges" border stripe size="small" max-height="240">
        <el-table-column prop="alertNo" label="告警号" width="180" show-overflow-tooltip />
        <el-table-column prop="metricCode" label="指标" min-width="140" show-overflow-tooltip />
        <el-table-column prop="systemCode" label="系统" width="120" show-overflow-tooltip />
        <el-table-column prop="alertType" label="告警类型" width="120" />
        <el-table-column label="级别变化" width="110">
          <template #default="{ row }">{{ upperLevel(row.fromLevel) }} → {{ upperLevel(row.toLevel) }}</template>
        </el-table-column>
        <el-table-column prop="operator" label="操作人" width="100" />
        <el-table-column label="时间" width="160">
          <template #default="{ row }">{{ fmtTs(row.changedAt) }}</template>
        </el-table-column>
        <el-table-column prop="reason" label="原因" min-width="220" show-overflow-tooltip />
      </el-table>
      <el-empty v-if="!report.levelChanges.length" description="当日无级别变化，以下为固定迁移映射" :image-size="52" />
      <el-table :data="report.legacyLevelMappings" border stripe size="small">
        <el-table-column prop="alertType" label="告警类型" min-width="220" />
        <el-table-column label="原级别" width="100">
          <template #default="{ row }">{{ upperLevel(row.fromLevel) }}</template>
        </el-table-column>
        <el-table-column label="新级别" width="100">
          <template #default="{ row }">{{ upperLevel(row.toLevel) }}</template>
        </el-table-column>
        <el-table-column prop="reason" label="迁移说明" min-width="320" show-overflow-tooltip />
      </el-table>
    </template>
    <el-empty v-else-if="!loading" description="选择业务日并加载日报" />

    <el-divider content-position="left">已生成日报（调度器每日自动生成 / 可手工重新生成）</el-divider>
    <el-table :data="files" v-loading="filesLoading" border stripe size="small">
      <el-table-column label="业务日" width="120">
        <template #default="{ row }">{{ String(row.bizDate).slice(0, 10) }}</template>
      </el-table-column>
      <el-table-column label="状态" width="90">
        <template #default="{ row }">
          <el-tag size="small" :type="row.status === 'success' ? 'success' : row.status === 'running' ? 'info' : 'danger'">{{ fileStatusLabel(row.status) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="metricsTotal" label="指标总数" width="100">
        <template #default="{ row }">{{ row.metricsTotal ?? '-' }}</template>
      </el-table-column>
      <el-table-column prop="metricsOver" label="超线数" width="100">
        <template #default="{ row }">{{ row.metricsOver ?? '-' }}</template>
      </el-table-column>
      <el-table-column label="生成时间" width="170">
        <template #default="{ row }">{{ fmtTs(row.generatedAt) }}</template>
      </el-table-column>
      <el-table-column prop="errorMsg" label="错误" min-width="160" show-overflow-tooltip />
      <el-table-column label="操作" width="220" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" :disabled="row.status !== 'success'" @click="doDownload(row, 'xlsx')">Excel</el-button>
          <el-button link type="primary" :disabled="row.status !== 'success'" @click="doDownload(row, 'pdf')">PDF</el-button>
          <el-button v-if="isAdmin" link type="warning" :loading="regenerating === row.bizDate" @click="doRegenerate(row)">重新生成</el-button>
        </template>
      </el-table-column>
    </el-table>
      </el-tab-pane>

      <el-tab-pane label="报表中心" name="center">
        <div class="toolbar">
          <div class="toolbar-title">报表模板<span class="sub">周报 / 月报 / 容量使用分析 / 告警汇总 / 容量预测, 生成 Excel 与 PDF（数据权限与页面一致）</span></div>
          <div class="toolbar-right">
            <el-button type="primary" :icon="Plus" @click="openDefDialog">新建模板</el-button>
            <el-button :icon="Refresh" circle @click="loadCenter" />
          </div>
        </div>

        <el-table :data="definitions" v-loading="centerLoading" border stripe size="small">
          <el-table-column prop="name" label="模板名称" min-width="180" show-overflow-tooltip />
          <el-table-column label="报表类型" width="130">
            <template #default="{ row }"><el-tag size="small" type="info">{{ reportTypeLabel(row.reportType) }}</el-tag></template>
          </el-table-column>
          <el-table-column label="时间窗口" width="100">
            <template #default="{ row }">{{ row.periodWindow }} 天</template>
          </el-table-column>
          <el-table-column label="含预测" width="80">
            <template #default="{ row }">{{ row.includeForecast ? '是' : '否' }}</template>
          </el-table-column>
          <el-table-column label="状态" width="90">
            <template #default="{ row }"><el-tag size="small" :type="row.enabled ? 'success' : 'info'">{{ row.enabled ? '启用' : '停用' }}</el-tag></template>
          </el-table-column>
          <el-table-column label="操作" width="280" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" size="small" @click="previewDef(row)">预览</el-button>
              <el-button link type="primary" size="small" :loading="runningDefId === row.id" @click="runDef(row)">生成报表</el-button>
              <el-button link type="primary" size="small" @click="openSubDialog(row)">订阅推送</el-button>
              <el-button link type="danger" size="small" @click="removeDef(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>

        <el-divider content-position="left">运行历史（含订阅自动生成）</el-divider>
        <el-table :data="runs" v-loading="runsLoading" border stripe size="small" max-height="320">
          <el-table-column prop="id" label="编号" width="80" />
          <el-table-column label="报表模板" min-width="170" show-overflow-tooltip>
            <template #default="{ row }">{{ definitionName(row.definitionId) }}</template>
          </el-table-column>
          <el-table-column label="统计周期" width="210">
            <template #default="{ row }">{{ dateOnly(row.periodStart) }} ~ {{ dateOnly(row.periodEnd) }}</template>
          </el-table-column>
          <el-table-column label="状态" width="90">
            <template #default="{ row }">
              <el-tag size="small" :type="row.status === 'success' ? 'success' : row.status === 'running' ? 'info' : 'danger'">{{ fileStatusLabel(row.status) }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="metricsTotal" label="指标数" width="90" align="right" />
          <el-table-column prop="generatedBy" label="生成人" width="110" show-overflow-tooltip />
          <el-table-column label="生成时间" width="160">
            <template #default="{ row }">{{ fmtTs(row.createdAt) }}</template>
          </el-table-column>
          <el-table-column label="操作" width="200" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" size="small" :disabled="row.status !== 'success'" @click="downloadRun(row, 'xlsx')">Excel</el-button>
              <el-button link type="primary" size="small" :disabled="row.status !== 'success'" @click="downloadRun(row, 'pdf')">PDF</el-button>
              <el-button link type="primary" size="small" @click="viewRun(row)">明细</el-button>
            </template>
          </el-table-column>
        </el-table>

        <el-divider content-position="left">订阅推送（每日 / 每周 / 每月, 支持时区, 失败自动重试）</el-divider>
        <el-table :data="subscriptions" v-loading="subsLoading" border stripe size="small">
          <el-table-column prop="name" label="订阅名称" min-width="170" show-overflow-tooltip />
          <el-table-column label="报表模板" min-width="150" show-overflow-tooltip>
            <template #default="{ row }">{{ definitionName(row.definitionId) }}</template>
          </el-table-column>
          <el-table-column label="调度" width="170">
            <template #default="{ row }">{{ scheduleLabel(row) }}</template>
          </el-table-column>
          <el-table-column label="收件人 / 渠道" min-width="200" show-overflow-tooltip>
            <template #default="{ row }">{{ targetLabel(row) }}</template>
          </el-table-column>
          <el-table-column label="下次运行" width="160">
            <template #default="{ row }">{{ row.nextRunAt ? fmtTs(row.nextRunAt) : '-' }}</template>
          </el-table-column>
          <el-table-column label="状态" width="80">
            <template #default="{ row }"><el-tag size="small" :type="row.enabled ? 'success' : 'info'">{{ row.enabled ? '启用' : '停用' }}</el-tag></template>
          </el-table-column>
          <el-table-column label="操作" width="150" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" size="small" :loading="runningSubId === row.id" @click="runSubscription(row)">立即运行</el-button>
              <el-button link type="danger" size="small" @click="removeSub(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="previewVisible" :title="previewData ? previewData.title : '报表预览'" width="960px">
      <template v-if="previewData">
        <div class="preview-meta">
          统计周期 {{ previewData.periodStart }} ~ {{ previewData.periodEnd }} · 指标数 {{ previewData.rows.length }} · 生成时间 {{ fmtTs(previewData.generatedAt) }}
        </div>
        <el-table :data="previewRows" border stripe size="small" max-height="420">
          <el-table-column v-for="(col, i) in previewData.columns" :key="col" :label="col" min-width="120" show-overflow-tooltip>
            <template #default="{ row }">{{ cellText(row[previewData.fields[i]]) }}</template>
          </el-table-column>
        </el-table>
      </template>
    </el-dialog>

    <el-dialog v-model="defDialog" title="新建报表模板" width="620px">
      <el-form :model="defForm" label-width="110px">
        <el-form-item label="模板名称"><el-input v-model="defForm.name" maxlength="128" show-word-limit /></el-form-item>
        <el-form-item label="报表类型">
          <el-select v-model="defForm.reportType" style="width: 220px">
            <el-option v-for="t in reportTypes" :key="t.value" :label="t.label" :value="t.value" />
          </el-select>
        </el-form-item>
        <el-form-item label="时间窗口">
          <el-input-number v-model="defForm.periodWindow" :min="1" :max="365" controls-position="right" />
          <span class="sub" style="margin-left: 8px">天（周报/月报自动按自然周期）</span>
        </el-form-item>
        <el-form-item label="包含预测列"><el-switch v-model="defForm.includeForecast" /></el-form-item>
        <el-form-item label="启用"><el-switch v-model="defForm.enabled" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="defDialog = false">取消</el-button>
        <el-button type="primary" :loading="savingDef" @click="createDef">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="subDialog" title="新建报表订阅" width="640px">
      <el-form :model="subForm" label-width="110px">
        <el-form-item label="订阅名称"><el-input v-model="subForm.name" maxlength="128" show-word-limit /></el-form-item>
        <el-form-item label="报表模板">
          <el-input :model-value="definitionName(subForm.definitionId)" disabled />
        </el-form-item>
        <el-form-item label="调度周期">
          <el-select v-model="subForm.scheduleType" style="width: 160px">
            <el-option label="每日" value="daily" />
            <el-option label="每周" value="weekly" />
            <el-option label="每月" value="monthly" />
          </el-select>
          <el-select v-if="subForm.scheduleType === 'weekly'" v-model="subForm.weekday" style="margin-left: 8px; width: 130px">
            <el-option v-for="d in 7" :key="d" :label="'周' + '一二三四五六日'[d - 1]" :value="d" />
          </el-select>
          <el-input-number v-if="subForm.scheduleType === 'monthly'" v-model="subForm.dayOfMonth" :min="1" :max="28" style="margin-left: 8px" controls-position="right" />
        </el-form-item>
        <el-form-item label="推送时间">
          <el-time-picker v-model="subForm.scheduleTime" format="HH:mm" value-format="HH:mm" placeholder="08:30" />
          <el-input v-model="subForm.timezone" style="margin-left: 8px; width: 200px" placeholder="Asia/Shanghai" />
        </el-form-item>
        <el-form-item label="收件人">
          <el-select v-model="subForm.recipients" multiple filterable allow-create default-first-option placeholder="邮箱 / 用户 / 群组" style="width: 100%">
            <el-option v-for="u in recipients" :key="u" :label="u" :value="u" />
          </el-select>
        </el-form-item>
        <el-form-item label="通知渠道">
          <el-select v-model="subForm.channelIds" multiple placeholder="选择通知渠道（可多选）" style="width: 100%">
            <el-option v-for="c in channels" :key="c.id" :label="`${c.name}（${c.type}）`" :value="c.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="启用"><el-switch v-model="subForm.enabled" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="subDialog = false">取消</el-button>
        <el-button type="primary" :loading="savingSub" @click="createSub">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref } from 'vue'
import * as echarts from 'echarts'
import { Download, Plus, Refresh } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { http } from '@/api/http'
import { fmtTs } from '@/utils/format'
import { useAuthStore } from '@/stores/auth'

interface DailyRow {
  metricCode: string
  name: string
  systemCode: string
  systemName: string
  unit: string
  statValue: number | null
  waterLevel: number | null
  level: string
  baseline: number | null
  warnLine: number | null
  dangerLine: number | null
  forecastModel: string
  forecastStatus: string
  etaWarnDate: string | null
  etaDangerDate: string | null
  riskScore: number | null
}
interface CollectFail {
  flowCode: string
  metricCode: string
  status: string
  triggeredAt: string
  errorMsg: string
}
interface ReportData {
  bizDate: string
  metrics: DailyRow[]
  metricsTotal?: number
  overCount: number
  overList: DailyRow[]
  missingCount: number
  missingList: DailyRow[]
  statusCounts: { normal: number; warn: number; danger: number; missing: number; nodata: number }
  collectFailCount: number
  collectFailures: CollectFail[]
  forecastCount?: number
  levelChanges: LevelChange[]
  legacyLevelMappings: LevelMapping[]
}
interface LevelChange {
  alertId: number
  alertNo: string
  metricCode: string
  systemCode: string
  alertType: string
  fromLevel: string
  toLevel: string
  action: string
  reason: string
  operator: string
  changedAt: string
}
interface LevelMapping {
  alertType: string
  fromLevel: string
  toLevel: string
  reason: string
}
interface FileRow {
  id: number
  reportType: string
  bizDate: string
  status: string
  metricsTotal: number | null
  metricsOver: number | null
  errorMsg: string
  generatedAt: string
}

const auth = useAuthStore()
const isAdmin = computed(() => auth.isAdmin)

// ---------- 报表中心（M7, Task 6） ----------
interface Definition {
  id: number
  name: string
  reportType: string
  periodWindow: number
  includeForecast: boolean
  enabled: boolean
}
interface RunRow {
  id: number
  definitionId: number
  periodStart: string
  periodEnd: string
  status: string
  metricsTotal: number
  generatedBy: string
  errorMsg: string
  createdAt: string
}
interface Subscription {
  id: number
  name: string
  definitionId: number
  recipients: string
  channelIds: string
  scheduleType: string
  scheduleTime: string
  weekday: number
  dayOfMonth: number
  timezone: string
  enabled: boolean
  nextRunAt: string | null
}
interface CenterData {
  title: string
  reportType: string
  periodStart: string
  periodEnd: string
  fields: string[]
  columns: string[]
  rows: Record<string, unknown>[]
  generatedAt: string
}

const tab = ref('daily')
const definitions = ref<Definition[]>([])
const runs = ref<RunRow[]>([])
const subscriptions = ref<Subscription[]>([])
const centerLoading = ref(false)
const runsLoading = ref(false)
const subsLoading = ref(false)
const runningDefId = ref<number | null>(null)
const runningSubId = ref<number | null>(null)
const previewVisible = ref(false)
const previewData = ref<CenterData | null>(null)
const previewRows = computed(() => (previewData.value?.rows ?? []).slice(0, 300))
const defDialog = ref(false)
const savingDef = ref(false)
const defForm = ref({ name: '', reportType: 'weekly', periodWindow: 7, includeForecast: true, enabled: true })
const subDialog = ref(false)
const savingSub = ref(false)
const channels = ref<{ id: number; name: string; type: string }[]>([])
const recipients = ref<string[]>([])
const subForm = ref({
  name: '', definitionId: 0, scheduleType: 'daily', scheduleTime: '08:30',
  weekday: 1, dayOfMonth: 1, timezone: 'Asia/Shanghai',
  recipients: [] as string[], channelIds: [] as number[], enabled: true,
})

const reportTypes = [
  { label: '容量周报', value: 'weekly' },
  { label: '容量月报', value: 'monthly' },
  { label: '容量使用分析', value: 'capacity_analysis' },
  { label: '告警汇总报表', value: 'alert_summary' },
  { label: '容量预测报表', value: 'forecast' },
  { label: '容量日报', value: 'daily' },
]

function reportTypeLabel(t: string): string {
  return reportTypes.find((x) => x.value === t)?.label ?? t
}
function definitionName(id: number): string {
  return definitions.value.find((d) => d.id === id)?.name ?? `#${id}`
}
function dateOnly(v: unknown): string {
  return v ? String(v).slice(0, 10) : '-'
}
function cellText(v: unknown): string {
  if (v == null) return ''
  if (typeof v === 'number') return Number(v).toLocaleString('zh-CN', { maximumFractionDigits: 4 })
  if (typeof v === 'boolean') return v ? '是' : '否'
  return String(v)
}
function scheduleLabel(row: Subscription): string {
  const time = `${row.scheduleTime} ${row.timezone}`
  if (row.scheduleType === 'weekly') return `每周${'一二三四五六日'[(row.weekday || 1) - 1] || '一'} ${time}`
  if (row.scheduleType === 'monthly') return `每月 ${row.dayOfMonth} 日 ${time}`
  return `每日 ${time}`
}
function targetLabel(row: Subscription): string {
  let recips: string[] = []
  let ids: number[] = []
  try { recips = JSON.parse(row.recipients ?? '[]') } catch { recips = [] }
  try { ids = JSON.parse(row.channelIds ?? '[]') } catch { ids = [] }
  const chNames = ids.map((id) => channels.value.find((c) => c.id === id)?.name ?? `#${id}`)
  const parts: string[] = []
  if (recips.length) parts.push(`收件人: ${recips.join(', ')}`)
  if (chNames.length) parts.push(`渠道: ${chNames.join(', ')}`)
  return parts.join(' · ') || '-'
}

async function loadCenter() {
  centerLoading.value = true
  try {
    const data = (await http.get('/api/v1/reports/definitions')) as unknown as { items: Definition[] }
    definitions.value = data.items ?? []
  } catch {
    definitions.value = []
  } finally {
    centerLoading.value = false
  }
  await Promise.all([loadRuns(), loadSubscriptions(), loadChannels()])
}

async function loadRuns() {
  runsLoading.value = true
  try {
    const data = (await http.get('/api/v1/reports/runs', { params: { limit: 50 } })) as unknown as { items: RunRow[] }
    runs.value = data.items ?? []
  } catch {
    runs.value = []
  } finally {
    runsLoading.value = false
  }
}

async function loadSubscriptions() {
  subsLoading.value = true
  try {
    const data = (await http.get('/api/v1/reports/subscriptions')) as unknown as { items: Subscription[] }
    subscriptions.value = data.items ?? []
  } catch {
    subscriptions.value = []
  } finally {
    subsLoading.value = false
  }
}

async function loadChannels() {
  try {
    const data = (await http.get('/api/v1/notification-channels')) as unknown as { items: { id: number; name: string; type: string }[] }
    channels.value = data.items ?? []
  } catch { /* 无查看权限时忽略 */ }
  if (!isAdmin.value) return
  try {
    const data = (await http.get('/api/v1/admin/users', { params: { pageSize: 100 } })) as unknown as {
      items: { username: string; realName: string; email: string }[]
    }
    const list = new Set<string>()
    for (const u of data.items ?? []) {
      if (u.username) list.add(u.username)
      if (u.email) list.add(u.email)
    }
    recipients.value = [...list]
  } catch { /* 无用户管理权限时忽略 */ }
}

async function previewDef(row: Definition) {
  try {
    previewData.value = (await http.get(`/api/v1/reports/definitions/${row.id}/preview`)) as unknown as CenterData
    previewVisible.value = true
  } catch { /* 拦截器已提示 */ }
}

async function runDef(row: Definition) {
  runningDefId.value = row.id
  try {
    const run = (await http.post(`/api/v1/reports/definitions/${row.id}/run`)) as unknown as RunRow
    ElMessage.success(`报表已生成（编号 ${run.id}, 指标 ${run.metricsTotal} 条）`)
    await loadRuns()
  } catch { /* 拦截器已提示 */ } finally {
    runningDefId.value = null
  }
}

async function removeDef(row: Definition) {
  try {
    await ElMessageBox.confirm(`确定删除报表模板「${row.name}」？相关运行历史与订阅会一并删除。`, '提示', { type: 'warning' })
  } catch { return }
  try {
    await http.delete(`/api/v1/reports/definitions/${row.id}`)
    ElMessage.success('模板已删除')
    await loadCenter()
  } catch { /* 拦截器已提示 */ }
}

async function downloadRun(row: RunRow, format: 'xlsx' | 'pdf') {
  try {
    const blob = (await http.get(`/api/v1/reports/runs/${row.id}/download`, {
      params: { format }, responseType: 'blob',
    })) as unknown as Blob
    downloadBlob(blob, `report-${row.id}.${format}`)
  } catch { /* 拦截器已提示 */ }
}

async function viewRun(row: RunRow) {
  try {
    const data = (await http.get(`/api/v1/reports/runs/${row.id}`)) as unknown as { run: RunRow }
    ElMessage.info(`运行 #${data.run.id} ${fileStatusLabel(data.run.status)}${data.run.errorMsg ? ': ' + data.run.errorMsg : ''}`)
  } catch { /* 拦截器已提示 */ }
}

function openDefDialog() {
  defForm.value = { name: '', reportType: 'weekly', periodWindow: 7, includeForecast: true, enabled: true }
  defDialog.value = true
}

async function createDef() {
  if (!defForm.value.name.trim()) {
    ElMessage.warning('请填写模板名称')
    return
  }
  savingDef.value = true
  try {
    await http.post('/api/v1/reports/definitions', defForm.value)
    ElMessage.success('模板已创建')
    defDialog.value = false
    await loadCenter()
  } catch { /* 拦截器已提示 */ } finally {
    savingDef.value = false
  }
}

function openSubDialog(row: Definition) {
  subForm.value = {
    name: row.name + ' 订阅', definitionId: row.id, scheduleType: 'daily', scheduleTime: '08:30',
    weekday: 1, dayOfMonth: 1, timezone: 'Asia/Shanghai',
    recipients: [], channelIds: [], enabled: true,
  }
  subDialog.value = true
}

async function createSub() {
  if (!subForm.value.name.trim()) {
    ElMessage.warning('请填写订阅名称')
    return
  }
  savingSub.value = true
  try {
    await http.post('/api/v1/reports/subscriptions', subForm.value)
    ElMessage.success('订阅已创建, 将按调度时间自动生成并推送')
    subDialog.value = false
    await loadSubscriptions()
  } catch { /* 拦截器已提示 */ } finally {
    savingSub.value = false
  }
}

async function runSubscription(row: Subscription) {
  runningSubId.value = row.id
  try {
    const run = (await http.post(`/api/v1/reports/subscriptions/${row.id}/run`)) as unknown as RunRow
    ElMessage.success(`订阅报表已生成（编号 ${run.id}）`)
    await Promise.all([loadRuns(), loadSubscriptions()])
  } catch { /* 拦截器已提示 */ } finally {
    runningSubId.value = null
  }
}

async function removeSub(row: Subscription) {
  try {
    await ElMessageBox.confirm(`确定删除订阅「${row.name}」？`, '提示', { type: 'warning' })
  } catch { return }
  try {
    await http.delete(`/api/v1/reports/subscriptions/${row.id}`)
    ElMessage.success('订阅已删除')
    await loadSubscriptions()
  } catch { /* 拦截器已提示 */ }
}

const date = ref<string>(new Date(Date.now() - 86400000).toISOString().slice(0, 10))
const loading = ref(false)
const report = ref<ReportData | null>(null)

const files = ref<FileRow[]>([])
const filesLoading = ref(false)
const regenerating = ref<string | null>(null)

const donutRef = ref<HTMLElement | null>(null)
const barRef = ref<HTMLElement | null>(null)
let donutChart: echarts.ECharts | null = null
let barChart: echarts.ECharts | null = null

const LEVEL_ORDER: Record<string, number> = { danger: 0, warn: 1 }
const overSorted = computed(() =>
  [...(report.value?.overList ?? [])].sort(
    (a, b) =>
      (LEVEL_ORDER[a.level] ?? 9) - (LEVEL_ORDER[b.level] ?? 9) ||
      (b.waterLevel ?? 0) - (a.waterLevel ?? 0),
  ),
)
const forecastRows = computed(() =>
  [...(report.value?.metrics ?? [])]
    .filter((row) => row.forecastStatus)
    .sort((a, b) => (b.riskScore ?? -1) - (a.riskScore ?? -1)),
)

async function load() {
  if (!date.value) return
  loading.value = true
  report.value = null
  try {
    report.value = (await http.get('/api/v1/reports/daily', { params: { date: date.value, metricsLimit: 500, missingLimit: 500 } })) as unknown as ReportData
    await nextTick()
    renderCharts()
  } catch {
    report.value = null
  } finally {
    loading.value = false
  }
}

async function loadFiles() {
  filesLoading.value = true
  try {
    files.value = ((await http.get('/api/v1/reports/files')) as unknown as FileRow[]) ?? []
  } catch {
    files.value = []
  } finally {
    filesLoading.value = false
  }
}

// ---------- 导出 / 下载 ----------
async function doExport(format: 'xlsx' | 'pdf') {
  if (!date.value) return
  try {
    const blob = (await http.get(`/api/v1/reports/daily/${date.value}/export`, {
      params: { format },
      responseType: 'blob',
    })) as unknown as Blob
    downloadBlob(blob, `daily-report-${date.value}.${format}`)
  } catch { /* 拦截器已提示 */ }
}

async function doDownload(row: FileRow, format: 'xlsx' | 'pdf') {
  const d = String(row.bizDate).slice(0, 10)
  try {
    const blob = (await http.get(`/api/v1/reports/daily/${d}/file`, {
      params: { format },
      responseType: 'blob',
    })) as unknown as Blob
    downloadBlob(blob, `daily-report-${d}.${format}`)
  } catch { /* 拦截器已提示 */ }
}

function downloadBlob(blob: Blob, name: string) {
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = name
  a.click()
  URL.revokeObjectURL(url)
}

async function doRegenerate(row: FileRow) {
  const d = String(row.bizDate).slice(0, 10)
  regenerating.value = row.bizDate
  try {
    const res = (await http.post(`/api/v1/reports/daily/${d}/generate`)) as unknown as { status: string }
    ElMessage.success(res.status === 'success' ? `日报已重新生成（${d}）` : `生成任务状态: ${res.status}`)
    await loadFiles()
    if (date.value === d) load()
  } catch { /* 拦截器已提示 */ } finally {
    regenerating.value = null
  }
}

// ---------- 图表 ----------
function renderCharts() {
  const d = report.value
  if (!d) return
  if (donutRef.value) {
    donutChart?.dispose()
    donutChart = echarts.init(donutRef.value)
    const sc = d.statusCounts
    donutChart.setOption({
      tooltip: { trigger: 'item', formatter: '{b}: {c} ({d}%)' },
      legend: { bottom: 0, textStyle: { fontSize: 11 } },
      series: [
        {
          type: 'pie',
          radius: ['45%', '70%'],
          center: ['50%', '45%'],
          label: { show: false },
          data: [
            { name: '正常', value: sc.normal, itemStyle: { color: '#34c759' } },
            { name: '超预警', value: sc.warn, itemStyle: { color: '#ff9500' } },
            { name: '超危险', value: sc.danger, itemStyle: { color: '#ff3b30' } },
            { name: '缺数', value: sc.missing, itemStyle: { color: '#e6455c' } },
            { name: '无数据', value: sc.nodata, itemStyle: { color: '#c0c4cc' } },
          ],
        },
      ],
    })
  }
  if (barRef.value) {
    barChart?.dispose()
    barChart = echarts.init(barRef.value)
    const items = d.metrics
      .filter((m) => m.waterLevel != null)
      .sort((a, b) => (b.waterLevel ?? 0) - (a.waterLevel ?? 0))
      .slice(0, 10)
      .reverse()
    barChart.setOption({
      tooltip: {
        trigger: 'axis',
        axisPointer: { type: 'shadow' },
        valueFormatter: (v: number) => `${(Number(v) * 100).toFixed(1)}%`,
      },
      grid: { left: 130, right: 60, top: 10, bottom: 24 },
      xAxis: { type: 'value', max: 1, axisLabel: { formatter: (v: number) => `${Math.round(v * 100)}%`, fontSize: 10 } },
      yAxis: {
        type: 'category',
        data: items.map((m) => `${m.name}（${m.systemName}）`),
        axisLabel: { fontSize: 10 },
      },
      series: [
        {
          type: 'bar',
          data: items.map((m) => ({
            value: m.waterLevel ?? 0,
            itemStyle: { color: levelColor(m.level) },
          })),
          barWidth: 12,
          label: { show: true, position: 'right', formatter: (p: { value: number }) => `${(p.value * 100).toFixed(0)}%`, fontSize: 10 },
        },
      ],
    })
  }
}

function onResize() {
  donutChart?.resize()
  barChart?.resize()
}

// ---------- 展示辅助 ----------
function fmtNum(v: number | null): string {
  if (v == null) return '-'
  return v >= 100 ? v.toFixed(0) : v.toFixed(2)
}
function fmtPct(v: number | null): string {
  return v == null ? '-' : (v * 100).toFixed(1) + '%'
}
function dayOnly(v: string | null): string {
  return v ? String(v).slice(0, 10) : '-'
}
function upperLevel(v: string): string {
  return v ? v.toUpperCase() : '-'
}
function forecastStatusLabel(status: string): string {
  return ({
    success: '成功',
    never: '窗口内未到线',
    insufficient_data: '数据不足',
    insufficient_quality: '质量不足',
  } as Record<string, string>)[status] ?? status ?? '-'
}
function forecastTagType(status: string): 'success' | 'warning' | 'info' | 'danger' {
  if (status === 'success') return 'success'
  if (status === 'insufficient_quality') return 'danger'
  if (status === 'insufficient_data') return 'warning'
  return 'info'
}
function levelColor(l: string): string {
  return l === 'danger' ? '#ff3b30' : l === 'warn' ? '#ff9500' : '#34c759'
}
function fileStatusLabel(s: string): string {
  return ({ running: '生成中', success: '成功', failed: '失败' } as Record<string, string>)[s] ?? s
}

onMounted(() => {
  load()
  loadFiles()
  loadCenter()
  window.addEventListener('resize', onResize)
})
onBeforeUnmount(() => {
  window.removeEventListener('resize', onResize)
  donutChart?.dispose()
  barChart?.dispose()
})
</script>

<style scoped>
.toolbar { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; flex-wrap: wrap; gap: 8px; }
.toolbar-title { font-size: 15px; font-weight: 600; }
.sub { color: #c0c4cc; font-size: 12px; font-weight: 400; margin-left: 10px; }
.toolbar-right { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.stat-row { display: grid; grid-template-columns: repeat(6, 1fr); gap: 12px; margin-bottom: 12px; }
.stat-card { background: #fff; border: 1px solid #ebeef5; border-top: 3px solid #1f6feb; border-radius: 6px; padding: 10px 14px; }
.stat-label { font-size: 12px; color: #909399; }
.stat-value { font-size: 24px; font-weight: 700; font-variant-numeric: tabular-nums; margin-top: 4px; }
.charts-row { display: grid; grid-template-columns: 1fr 1fr; gap: 12px; margin-bottom: 12px; }
.chart-box { background: #fff; border: 1px solid #ebeef5; border-radius: 6px; padding: 8px 12px; }
.chart-title { font-size: 13px; font-weight: 600; margin: 4px 0; }
.chart { width: 100%; height: 260px; }
.section-title { font-size: 13px; font-weight: 600; margin: 14px 0 8px; }
.preview-meta { font-size: 12px; color: #606266; margin-bottom: 8px; }
</style>
