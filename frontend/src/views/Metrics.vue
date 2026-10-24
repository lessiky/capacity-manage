<template>
  <div class="page-card">
    <el-tabs v-model="tab" @tab-change="onTabChange">
      <el-tab-pane label="指标列表" name="list">
        <div class="toolbar">
          <el-select v-model="filterSystem" clearable filterable placeholder="业务系统" style="width: 170px" @change="reload">
            <el-option v-for="s in systems" :key="s.systemCode" :label="s.name" :value="s.systemCode" />
          </el-select>
          <el-select v-model="filterStatus" clearable placeholder="状态" style="width: 110px" @change="reload">
            <el-option label="启用" value="active" />
            <el-option label="停用" value="disabled" />
          </el-select>
          <el-button type="primary" :icon="Search" @click="reload">查询</el-button>
          <el-button type="success" :icon="Plus" @click="openCreate()">新建指标</el-button>
          <div style="flex: 1" />
          <el-button :icon="Refresh" circle @click="reload" />
        </div>

        <el-table :data="items" v-loading="loading" border stripe>
          <el-table-column prop="metricCode" label="指标编码" min-width="180" show-overflow-tooltip />
          <el-table-column prop="name" label="名称" min-width="130" show-overflow-tooltip />
          <el-table-column label="类型" width="90">
            <template #default="{ row }">
              <el-tag size="small" :type="kindTagType(row.metricKind)">{{ kindLabel(row.metricKind) }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="统计方法" width="90">
            <template #default="{ row }">{{ row.metricKind === 'normal' ? statModeLabel(row.statMode) : '-' }}</template>
          </el-table-column>
          <el-table-column prop="systemCode" label="系统" width="130" />
          <el-table-column prop="unit" label="单位" width="70" />
          <el-table-column label="阈值模式" width="90">
            <template #default="{ row }">{{ row.thresholdMode === 'percent' ? '百分比' : '绝对值' }}</template>
          </el-table-column>
          <el-table-column label="预警线" width="90">
            <template #default="{ row }">{{ row.warnLine ?? '-' }}</template>
          </el-table-column>
          <el-table-column label="危险线" width="90">
            <template #default="{ row }">{{ row.dangerLine ?? '-' }}</template>
          </el-table-column>
          <el-table-column label="判定方向" width="100">
            <template #default="{ row }">{{ row.judgeDirection === 'higher_worse' ? '越高越差' : '越低越差' }}</template>
          </el-table-column>
          <el-table-column label="状态" width="80">
            <template #default="{ row }">
              <el-tag :type="row.status === 'active' ? 'success' : 'info'" size="small">{{ row.status === 'active' ? '启用' : '停用' }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="330" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" @click="openDetail(row)">详情</el-button>
              <el-button v-if="row.metricKind !== 'group'" link type="primary" @click="openThreshold(row)">调阈值</el-button>
              <el-button link type="warning" @click="openHistory(row)">历史</el-button>
              <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
              <el-button v-if="row.status === 'active'" link type="danger" @click="doDisable(row)">停用</el-button>
              <el-button v-else link type="success" @click="doEnable(row)">启用</el-button>
              <el-button v-if="row.status === 'disabled'" link type="danger" @click="doDelete(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
        <div class="pager">
          <el-pagination v-model:current-page="page" :page-size="20" :total="total" layout="total, prev, pager, next" @current-change="load" />
        </div>
        <el-empty v-if="!loading && error" :description="error" />
      </el-tab-pane>

      <el-tab-pane label="指标树" name="tree">
        <div class="toolbar">
          <el-select v-model="filterSystem" clearable filterable placeholder="业务系统" style="width: 170px" @change="loadTree">
            <el-option v-for="s in systems" :key="s.systemCode" :label="s.name" :value="s.systemCode" />
          </el-select>
          <el-input v-model="treeKeyword" clearable placeholder="名称/编码关键字" style="width: 190px" @keyup.enter="loadTree" @clear="loadTree" />
          <el-button type="primary" :icon="Search" @click="loadTree">查询</el-button>
          <el-button type="success" :icon="Plus" @click="openCreate({ metricKind: 'aggregate' })">新建汇总指标</el-button>
          <div style="flex: 1" />
          <span class="tree-tip">汇总指标 → 子指标(分组) → 统计模块; 点击节点查看详情</span>
          <el-button :icon="Refresh" circle @click="loadTree" />
        </div>
        <el-tree
          v-loading="treeLoading"
          :data="treeData"
          node-key="metricCode"
          default-expand-all
          highlight-current
          :expand-on-click-node="false"
          :props="{ label: 'name', children: 'children' }"
          @node-click="onTreeNodeClick"
        >
          <template #default="{ data }">
            <div class="tree-node">
              <el-tag size="small" :type="kindTagType(data.metricKind)" effect="dark" class="tree-kind">{{ kindLabel(data.metricKind) }}</el-tag>
              <span class="tree-name">{{ data.name }}</span>
              <span class="tree-code">{{ data.metricCode }}</span>
              <span v-if="data.metricKind === 'normal'" class="tree-stat" title="统计方法（点击编辑）" @click.stop="openEdit(data)">{{ statModeLabel(data.statMode) }}</span>
              <span class="tree-actions">
                <el-button
                  v-if="data.metricKind === 'aggregate' || data.metricKind === 'group'"
                  link type="primary" size="small"
                  @click.stop="openCreate({ parentCode: data.metricCode, metricKind: data.metricKind === 'aggregate' ? 'group' : 'normal', systemCode: data.systemCode })"
                >{{ data.metricKind === 'aggregate' ? '添加子指标' : '添加统计模块' }}</el-button>
                <el-button link type="primary" size="small" @click.stop="openDetail(data)">详情</el-button>
                <el-button link type="primary" size="small" @click.stop="openEdit(data)">编辑</el-button>
              </span>
            </div>
          </template>
        </el-tree>
        <el-empty v-if="!treeLoading && treeData.length === 0" description="暂无指标, 可新建汇总指标" />
      </el-tab-pane>

      <el-tab-pane label="衍生指标" name="derived">
        <DerivedMetricsPanel v-if="tab === 'derived'" />
      </el-tab-pane>

      <el-tab-pane label="阈值模板" name="templates">
        <ThresholdTemplatePanel v-if="tab === 'templates'" />
      </el-tab-pane>

      <el-tab-pane label="批量导入" name="import">
        <MetricImportPanel v-if="tab === 'import'" />
      </el-tab-pane>
    </el-tabs>

    <!-- 新建/编辑 -->
    <el-dialog v-model="formVisible" :title="editing ? '编辑指标' : createTitle" width="640px">
      <el-form :model="form" label-width="110px">
        <el-form-item label="节点类型">
          <el-select v-model="form.metricKind" :disabled="!!editing" style="width: 100%" @change="onKindChange">
            <el-option label="普通指标(有采集数据)" value="normal" />
            <el-option label="子指标分组(归类节点)" value="group" />
            <el-option label="汇总指标(子指标聚合)" value="aggregate" />
          </el-select>
        </el-form-item>
        <el-form-item label="指标编码" required><el-input v-model="form.metricCode" :disabled="!!editing" placeholder="如 CORE_DB.CAP_USAGE" /></el-form-item>
        <el-form-item label="名称" required><el-input v-model="form.name" /></el-form-item>
        <el-form-item label="归属系统" required>
          <el-select v-model="form.systemCode" filterable style="width: 100%">
            <el-option v-for="s in systems" :key="s.systemCode" :label="s.name + ' (' + s.systemCode + ')'" :value="s.systemCode" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="form.metricKind === 'group' || form.metricKind === 'normal'" :label="form.metricKind === 'group' ? '所属汇总指标' : '上级指标(可选)'">
          <el-select v-model="form.parentCode" :clearable="form.metricKind === 'normal'" :disabled="!!editing" style="width: 100%" @change="onParentChange" placeholder="无">
            <el-option
              v-for="p in parentOptions"
              :key="p.metricCode"
              :label="p.name + ' (' + p.metricCode + ')'"
              :value="p.metricCode"
            />
          </el-select>
        </el-form-item>
        <el-form-item v-if="form.metricKind === 'normal' && form.parentCode" label="统计模块标识" required>
          <el-input v-model="form.statModule" placeholder="如 count / max_latency / avg_latency" />
          <div class="form-tip">同一模块标识的指标将在汇总指标中按统计方式合并(求和/取最大/取均值)</div>
        </el-form-item>
        <el-form-item v-if="form.metricKind === 'aggregate'" label="主统计模块">
          <el-input v-model="form.mainModule" placeholder="如 count(请求量); 留空=首个模块" />
          <div class="form-tip">汇总指标的日值/水位/阈值告警按该模块的跨子指标聚合值计算; 留空取第一个模块</div>
        </el-form-item>
        <el-form-item label="单位" required><el-input v-model="form.unit" placeholder="如 % / 笔 / GB (虚拟节点填 -)" /></el-form-item>
        <template v-if="form.metricKind === 'normal'">
          <el-form-item label="指标类型">
            <el-select v-model="form.metricType" style="width: 100%">
              <el-option label="使用率" value="usage" />
              <el-option label="绝对量" value="absolute" />
              <el-option label="增量" value="increment" />
            </el-select>
          </el-form-item>
          <el-form-item label="统计方式">
            <el-select v-model="form.statMode" style="width: 100%">
              <el-option label="最新值 (latest)" value="latest" />
              <el-option label="最大值 (max)" value="max" />
              <el-option label="最小值 (min)" value="min" />
              <el-option label="平均值 (avg)" value="avg" />
              <el-option label="求和 (sum)" value="sum" />
              <div class="form-tip">决定该指标采集值的合并口径, 列表/树视图可查看, 此处可修改</div>
            </el-select>
          </el-form-item>
          <el-form-item label="权重指标">
            <el-select v-model="form.weightMetricCode" :disabled="!form.parentCode" clearable filterable style="width: 100%" :placeholder="form.parentCode ? '无(按统计方式简单合并)' : '请先选择上级指标'">
              <el-option v-for="w in weightOptions" :key="w.metricCode" :label="w.name + ' (' + w.metricCode + ')'" :value="w.metricCode" />
            </el-select>
            <div class="form-tip">仅可选同一父级下的同级指标; 配置后所属模块的汇总值按加权均值计算: sum(値×权重)/sum(权重), 如平均耗时按请求量加权</div>
          </el-form-item>
          <el-form-item label="判定方向">
            <el-radio-group v-model="form.judgeDirection">
              <el-radio value="higher_worse">越高越差</el-radio>
              <el-radio value="lower_worse">越低越差</el-radio>
            </el-radio-group>
          </el-form-item>
          <el-form-item label="阈值模式">
            <el-radio-group v-model="form.thresholdMode">
              <el-radio value="percent">百分比(0-100)</el-radio>
              <el-radio value="absolute">绝对值</el-radio>
            </el-radio-group>
          </el-form-item>
          <el-form-item label="预警线 / 危险线">
            <el-input-number v-model="form.warnLine" :min="0" :max="form.thresholdMode === 'percent' ? 100 : undefined" :precision="2" style="width: 150px" />
            <span style="margin: 0 8px">/</span>
            <el-input-number v-model="form.dangerLine" :min="0" :max="form.thresholdMode === 'percent' ? 100 : undefined" :precision="2" style="width: 150px" />
          </el-form-item>
          <el-form-item label="基线(可选)"><el-input-number v-model="form.baseline" :min="0" :precision="2" style="width: 150px" /></el-form-item>
        </template>
        <template v-if="form.metricKind === 'aggregate'">
          <el-form-item label="判定方向">
            <el-radio-group v-model="form.judgeDirection">
              <el-radio value="higher_worse">越高越差</el-radio>
              <el-radio value="lower_worse">越低越差</el-radio>
            </el-radio-group>
          </el-form-item>
          <el-form-item label="阈值模式">
            <el-radio-group v-model="form.thresholdMode">
              <el-radio value="percent">百分比(0-100)</el-radio>
              <el-radio value="absolute">绝对值</el-radio>
            </el-radio-group>
          </el-form-item>
          <el-form-item label="预警线 / 危险线">
            <el-input-number v-model="form.warnLine" :min="0" :max="form.thresholdMode === 'percent' ? 100 : undefined" :precision="2" style="width: 150px" />
            <span style="margin: 0 8px">/</span>
            <el-input-number v-model="form.dangerLine" :min="0" :max="form.thresholdMode === 'percent' ? 100 : undefined" :precision="2" style="width: 150px" />
            <div class="form-tip">配置后, 日汇总时主统计模块聚合值超线即生成阈值告警(合并/升级/恢复)</div>
          </el-form-item>
        </template>
        <el-form-item label="备注"><el-input v-model="form.remark" type="textarea" :rows="2" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="formVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveForm">保存</el-button>
      </template>
    </el-dialog>

    <!-- 调整阈值 -->
    <el-dialog v-model="thVisible" :title="'调整阈值 - ' + (thRow?.metricCode ?? '')" width="460px">
      <el-form label-width="100px" v-if="thRow">
        <el-form-item label="当前值">
          预警 {{ thRow.warnLine ?? '-' }} / 危险 {{ thRow.dangerLine ?? '-' }}（{{ thRow.thresholdMode === 'percent' ? '百分比' : '绝对值' }}）
        </el-form-item>
        <el-form-item label="新预警线"><el-input-number v-model="thWarn" :min="0" :max="thRow.thresholdMode === 'percent' ? 100 : undefined" :precision="2" style="width: 160px" /></el-form-item>
        <el-form-item label="新危险线"><el-input-number v-model="thDanger" :min="0" :max="thRow.thresholdMode === 'percent' ? 100 : undefined" :precision="2" style="width: 160px" /></el-form-item>
        <el-form-item label="调整原因" required><el-input v-model="thReason" type="textarea" :rows="2" placeholder="必填（如容量扩容评审）" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="thVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveThreshold">保存</el-button>
      </template>
    </el-dialog>

    <!-- 阈值历史 -->
    <el-dialog v-model="histVisible" :title="'阈值调整历史 - ' + (histCode ?? '')" width="720px">
      <el-table :data="history" border stripe size="small">
        <el-table-column prop="changedAt" label="时间" width="160">
          <template #default="{ row }">{{ fmtTs(row.changedAt) }}</template>
        </el-table-column>
        <el-table-column prop="operator" label="操作人" width="100" />
        <el-table-column label="预警线" width="110">
          <template #default="{ row }">{{ row.warnOld ?? '-' }} → {{ row.warnNew ?? '-' }}</template>
        </el-table-column>
        <el-table-column label="危险线" width="110">
          <template #default="{ row }">{{ row.dangerOld ?? '-' }} → {{ row.dangerNew ?? '-' }}</template>
        </el-table-column>
        <el-table-column prop="reason" label="原因" min-width="180" show-overflow-tooltip />
      </el-table>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, onMounted } from 'vue'
import { useRouter } from 'vue-router'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Search, Refresh, Plus } from '@element-plus/icons-vue'
import { http } from '@/api/http'
import { fmtTs } from '@/utils/format'
import DerivedMetricsPanel from '@/components/DerivedMetricsPanel.vue'
import ThresholdTemplatePanel from '@/components/ThresholdTemplatePanel.vue'
import MetricImportPanel from '@/components/MetricImportPanel.vue'

interface MetRow {
  metricCode: string
  name: string
  systemCode: string
  unit: string
  metricType: string
  metricKind?: string
  parentCode?: string | null
  statModule?: string
  statMode: string
  judgeDirection: string
  thresholdMode: string
  mainModule?: string
  weightMetricCode?: string | null
  warnLine: number | null
  dangerLine: number | null
  baseline: number | null
  status: string
  remark: string
}
interface SysRow { systemCode: string; name: string }
interface TreeRow extends MetRow { children?: TreeRow[] }

const router = useRouter()
function openDetail(row: MetRow) {
  const kind = row.metricKind ?? 'normal'
  if (kind === 'group' || kind === 'aggregate') {
    router.push('/metrics/' + row.metricCode + '/aggregate-detail')
  } else {
    router.push('/metrics/' + row.metricCode + '/detail')
  }
}
function kindLabel(kind?: string): string {
  return ({ aggregate: '汇总', group: '子指标', normal: '指标' } as Record<string, string>)[kind ?? 'normal'] || '指标'
}
function statModeLabel(mode?: string): string {
  return ({ latest: '最新值', max: '最大值', min: '最小值', avg: '平均值', sum: '求和' } as Record<string, string>)[mode ?? ''] || (mode ?? '-')
}

function kindTagType(kind?: string): string {
  return ({ aggregate: 'danger', group: 'warning', normal: 'info' } as Record<string, string>)[kind ?? 'normal'] || 'info'
}

const tab = ref<'list' | 'tree' | 'derived' | 'templates' | 'import'>('list')
const items = ref<MetRow[]>([])
const systems = ref<SysRow[]>([])
const filterSystem = ref('')
const filterStatus = ref('')
const loading = ref(false)
const error = ref('')
const page = ref(1)
const total = ref(0)

// ---------- 指标树 ----------
const treeData = ref<TreeRow[]>([])
const treeLoading = ref(false)
const treeKeyword = ref('')
let treeLoaded = false
async function loadTree() {
  treeLoading.value = true
  try {
    const params: Record<string, string> = {}
    if (filterSystem.value) params.systemCode = filterSystem.value
    if (treeKeyword.value.trim()) params.keyword = treeKeyword.value.trim()
    const data = (await http.get('/api/v1/metrics/tree', { params })) as unknown as { roots: TreeRow[] }
    treeData.value = data.roots ?? []
    treeLoaded = true
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : String(e))
  } finally {
    treeLoading.value = false
  }
}
function onTabChange(name: string | number) {
  if (name === 'tree' && !treeLoaded) loadTree()
}
function onTreeNodeClick(data: TreeRow) {
  openDetail(data)
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    const data = (await http.get('/api/v1/metrics', {
      params: {
        page: page.value,
        pageSize: 20,
        ...(filterSystem.value ? { systemCode: filterSystem.value } : {}),
        ...(filterStatus.value ? { status: filterStatus.value } : {}),
      },
    })) as unknown as { items: MetRow[]; total: number }
    items.value = data.items ?? []
    total.value = data.total ?? 0
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

async function loadSystems() {
  try {
    const data = (await http.get('/api/v1/systems', { params: { page: 1, pageSize: 200 } })) as unknown as { items: SysRow[] }
    systems.value = data.items ?? []
  } catch { /* 无系统时忽略 */ }
}

// ---------- 新建/编辑 ----------
const formVisible = ref(false)
const saving = ref(false)
const editing = ref(false)
const blankForm = () => ({
  metricKind: 'normal' as 'normal' | 'group' | 'aggregate',
  metricCode: '', name: '', systemCode: '', unit: '',
  parentCode: '', statModule: '', mainModule: '', weightMetricCode: '',
  metricType: 'usage', statMode: 'latest',
  judgeDirection: 'higher_worse', thresholdMode: 'percent',
  warnLine: null as number | null, dangerLine: null as number | null,
  baseline: null as number | null, remark: '',
})
const form = ref(blankForm())
const allMetrics = ref<MetRow[]>([])
async function loadAllMetrics() {
  try {
    const first = (await http.get('/api/v1/metrics', { params: { page: 1, pageSize: 200 } })) as unknown as { items: MetRow[]; total: number }
    let all = first.items ?? []
    const pages = Math.ceil((first.total ?? 0) / 200)
    for (let p = 2; p <= pages; p++) {
      const data = (await http.get('/api/v1/metrics', { params: { page: p, pageSize: 200 } })) as unknown as { items: MetRow[] }
      all = all.concat(data.items ?? [])
    }
    allMetrics.value = all
  } catch { allMetrics.value = [] }
}
const parentOptions = computed(() => {
  if (form.value.metricKind === 'group') {
    return allMetrics.value.filter((m) => m.metricKind === 'aggregate' && m.systemCode === form.value.systemCode)
  }
  if (form.value.metricKind === 'normal') {
    return allMetrics.value.filter((m) => (m.metricKind === 'aggregate' || m.metricKind === 'group') && m.systemCode === form.value.systemCode)
  }
  return []
})
const weightOptions = computed(() => {
  if (form.value.metricKind !== 'normal' || !form.value.parentCode) return []
  return allMetrics.value.filter(
    (m) => m.metricKind === 'normal' && m.systemCode === form.value.systemCode
      && (m.parentCode ?? '') === form.value.parentCode && m.metricCode !== form.value.metricCode,
  )
})
const createTitle = computed(() => {
  if (editing.value) return '编辑指标'
  const k = form.value.metricKind
  return k === 'aggregate' ? '新建汇总指标' : k === 'group' ? '新建子指标分组' : '新建指标'
})
function openCreate(opts?: { metricKind?: 'normal' | 'group' | 'aggregate'; parentCode?: string; systemCode?: string }) {
  form.value = blankForm()
  form.value.metricKind = opts?.metricKind ?? 'normal'
  if (opts?.parentCode) form.value.parentCode = opts.parentCode
  if (opts?.systemCode) form.value.systemCode = opts.systemCode
  if (form.value.metricKind !== 'normal') form.value.unit = '-'
  editing.value = false
  loadAllMetrics().then(() => { formVisible.value = true })
}
function onParentChange() {
  form.value.weightMetricCode = ''
}

function onKindChange() {
  form.value.parentCode = ''
  form.value.statModule = ''
  form.value.mainModule = ''
  form.value.weightMetricCode = ''
  if (form.value.metricKind !== 'normal' && !form.value.unit) form.value.unit = '-'
}
function openEdit(row: MetRow) {
  form.value = {
    ...blankForm(),
    ...(row as unknown as Record<string, unknown>),
    metricKind: (row.metricKind ?? 'normal') as 'normal' | 'group' | 'aggregate',
    parentCode: row.parentCode ?? '',
    statModule: row.statModule ?? '',
    mainModule: row.mainModule ?? '',
    weightMetricCode: row.weightMetricCode ?? '',
    warnLine: row.warnLine, dangerLine: row.dangerLine, baseline: row.baseline,
  } as ReturnType<typeof blankForm>
  editing.value = true
  loadAllMetrics().then(() => { formVisible.value = true })
}
async function saveForm() {
  const f = form.value
  if (!f.metricCode || !f.name || !f.systemCode || !f.unit) { ElMessage.warning('请填写必填项'); return }
  if (f.metricKind === 'normal' && f.warnLine == null && f.dangerLine == null) { ElMessage.warning('预警线/危险线至少配置一种'); return }
  if (f.metricKind === 'group' && !f.parentCode) { ElMessage.warning('子指标分组必须选择所属汇总指标'); return }
  if (f.metricKind === 'normal' && f.parentCode && (f.parentCode === '') === false && !f.statModule) {
    const p = allMetrics.value.find((m) => m.metricCode === f.parentCode)
    if (p?.metricKind === 'group') { ElMessage.warning('子指标分组下的统计模块须填写模块标识'); return }
  }
  saving.value = true
  try {
    const body: Record<string, unknown> = {
      metricCode: f.metricCode, name: f.name, systemCode: f.systemCode, unit: f.unit,
      metricKind: f.metricKind,
      remark: f.remark,
    }
    if (f.parentCode) body.parentCode = f.parentCode
    if (f.statModule) body.statModule = f.statModule
    if (f.metricKind === 'aggregate') {
      if (f.mainModule) body.mainModule = f.mainModule
      body.judgeDirection = f.judgeDirection
      body.thresholdMode = f.thresholdMode
      body.warnLine = f.warnLine
      body.dangerLine = f.dangerLine
    }
    if (f.metricKind === 'normal' && f.weightMetricCode) {
      body.weightMetricCode = f.weightMetricCode
    }
    if (f.metricKind === 'normal') {
      body.metricType = f.metricType
      body.statMode = f.statMode
      body.judgeDirection = f.judgeDirection
      body.thresholdMode = f.thresholdMode
      body.warnLine = f.warnLine
      body.dangerLine = f.dangerLine
      body.baseline = f.baseline
    }
    if (editing.value) {
      const { metricCode, ...rest } = body
      await http.put(`/api/v1/metrics/${f.metricCode}`, rest)
      ElMessage.success('已更新')
    } else {
      await http.post('/api/v1/metrics', body)
      ElMessage.success('已创建')
    }
    formVisible.value = false
    await load()
    if (treeLoaded) await loadTree()
  } finally {
    saving.value = false
  }
}

// ---------- 调阈值 / 历史 ----------
const thVisible = ref(false)
const thRow = ref<MetRow | null>(null)
const thWarn = ref<number | null>(null)
const thDanger = ref<number | null>(null)
const thReason = ref('')
function openThreshold(row: MetRow) {
  thRow.value = row
  thWarn.value = row.warnLine
  thDanger.value = row.dangerLine
  thReason.value = ''
  thVisible.value = true
}
async function saveThreshold() {
  if (!thReason.value.trim()) { ElMessage.warning('调整原因必填'); return }
  saving.value = true
  try {
    await http.put(`/api/v1/metrics/${thRow.value!.metricCode}/threshold`, {
      warnLine: thWarn.value, dangerLine: thDanger.value, reason: thReason.value,
    })
    ElMessage.success('阈值已调整（已记录历史）')
    thVisible.value = false
    await load()
  } finally {
    saving.value = false
  }
}
const histVisible = ref(false)
const histCode = ref('')
const history = ref<Record<string, unknown>[]>([])
async function openHistory(row: MetRow) {
  histCode.value = row.metricCode
  histVisible.value = true
  try {
    const data = (await http.get(`/api/v1/metrics/${row.metricCode}/threshold-history`)) as unknown as Record<string, unknown>[]
    history.value = data ?? []
  } catch { history.value = [] }
}

async function doDisable(row: MetRow) {
  try {
    await ElMessageBox.confirm(`确认停用指标「${row.name}」? 停用后不再采集与判定。`, '提示', { type: 'warning' })
    await http.post(`/api/v1/metrics/${row.metricCode}/disable`, { confirm: true })
    ElMessage.success('已停用')
    await load()
  } catch { /* 取消 */ }
}

async function doEnable(row: MetRow) {
  try {
    await ElMessageBox.confirm(`确认启用指标「${row.name}」? 将同时恢复其联动停用的采集流程。`, '提示', { type: 'info' })
    await http.post(`/api/v1/metrics/${row.metricCode}/enable`)
    ElMessage.success('已启用')
    await load()
  } catch { /* 取消 */ }
}

async function doDelete(row: MetRow) {
  try {
    await ElMessageBox.confirm(`确认删除指标「${row.name}」? 删除后不可恢复。`, '警告', { type: 'warning' })
    await http.delete(`/api/v1/metrics/${row.metricCode}`)
    ElMessage.success('已删除')
    await load()
  } catch { /* 取消 */ }
}

const reload = load

onMounted(() => { loadSystems(); load() })
</script>

<style scoped>
.toolbar { display: flex; gap: 8px; align-items: center; margin-bottom: 14px; }
.pager { margin-top: 12px; display: flex; justify-content: flex-end; }
.tree-tip { font-size: 12px; color: #909399; }
.tree-node { display: flex; align-items: center; flex: 1; min-width: 0; gap: 6px; padding-right: 8px; }
.tree-kind { flex: none; }
.tree-name { font-weight: 500; }
.tree-code { font-size: 12px; color: #909399; font-variant-numeric: tabular-nums; }
.tree-stat { margin-left: 6px; padding: 0 6px; font-size: 11px; color: #606266; background: #ecf5ff; border-radius: 3px; cursor: pointer; }
.tree-actions { margin-left: auto; display: flex; gap: 2px; opacity: 0; transition: opacity 0.15s; }
.tree-node:hover .tree-actions { opacity: 1; }
.form-tip { font-size: 12px; color: #909399; line-height: 1.4; width: 100%; }
</style>
