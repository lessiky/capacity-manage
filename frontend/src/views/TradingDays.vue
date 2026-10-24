<template>
  <div class="page-card">
    <el-tabs v-model="tab">
      <el-tab-pane label="日历维护" name="calendar">
        <div class="toolbar">
      <el-date-picker v-model="range" type="daterange" value-format="YYYY-MM-DD"
        start-placeholder="开始日期" end-placeholder="结束日期" style="width: 270px" />
      <el-button type="primary" :icon="Search" @click="search">查询</el-button>
      <el-button :icon="Document" @click="downloadTemplate">导入模板</el-button>
      <el-button :icon="Upload" @click="importVisible = true">Excel 导入</el-button>
      <el-button :icon="Download" @click="doExport">导出</el-button>
      <div style="flex: 1" />
      <el-button :icon="Refresh" circle @click="load" />
    </div>

    <el-table :data="items" v-loading="loading" border stripe max-height="580">
      <el-table-column prop="date" label="日期" width="130" />
      <el-table-column label="交易日" width="100">
        <template #default="{ row }">
          <el-tag :type="row.isTrading ? 'success' : 'info'" size="small">{{ row.isTrading ? '是' : '否' }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="类型" width="120">
        <template #default="{ row }">{{ dayTypeLabel(row.dayType) }}</template>
      </el-table-column>
      <el-table-column label="来源" width="100">
        <template #default="{ row }">
          <el-tag v-if="row.source === 'manual' || row.source === 'excel'" type="warning" size="small">{{ row.source }}</el-tag>
          <span v-else>{{ row.source }}</span>
        </template>
      </el-table-column>
      <el-table-column label="操作" min-width="150">
        <template #default="{ row }">
          <el-button link type="primary" @click="openMark(row)">手工标记</el-button>
          <el-button link type="warning" @click="deriveDate = row.date; deriveVisible = true">推导</el-button>
        </template>
      </el-table-column>
    </el-table>
    <el-empty v-if="!loading && error" :description="error" />
    <el-empty v-else-if="!loading && items.length === 0" description="当前区间无日历数据" />
    <el-pagination
      v-if="total > 0"
      class="pager"
      background
      layout="total, sizes, prev, pager, next"
      :total="total"
      :current-page="page"
      :page-size="pageSize"
      :page-sizes="[30, 50, 100, 200]"
      @current-change="onPageChange"
      @size-change="onSizeChange"
    />
      </el-tab-pane>

      <el-tab-pane label="完整性检查" name="check">
        <TradingCalendarCheckPanel v-if="tab === 'check'" />
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="markVisible" title="手工标记（source=manual，可覆盖 seed 数据）" width="440px">
      <el-form label-width="90px" v-if="marking">
        <el-form-item label="日期">{{ marking.date }}</el-form-item>
        <el-form-item label="是否交易日">
          <el-switch v-model="marking.isTrading" active-text="交易日" inactive-text="休市" />
        </el-form-item>
        <el-form-item label="日类型">
          <el-select v-model="marking.dayType" style="width: 100%">
            <el-option label="交易日" value="trading" />
            <el-option label="周末" value="weekend" />
            <el-option label="节假日" value="holiday" />
            <el-option label="调休交易日" value="makeup" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="markVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveMark">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="deriveVisible" title="交易日推导" width="460px">
      <el-form label-width="90px" inline>
        <el-form-item label="基准日期"><span>{{ deriveDate }}</span></el-form-item>
        <el-form-item label="方向">
          <el-select v-model="deriveAction" style="width: 150px">
            <el-option label="前一交易日" value="prev" />
            <el-option label="后一交易日" value="next" />
            <el-option label="前/后第 N 个" value="offset" />
          </el-select>
        </el-form-item>
        <el-form-item v-if="deriveAction === 'offset'" label="N（正=后）">
          <el-input-number v-model="deriveN" :min="-30" :max="30" />
        </el-form-item>
      </el-form>
      <el-button type="primary" :loading="deriving" @click="doDerive">推导</el-button>
      <div v-if="deriveResult" style="margin-top: 12px; font-size: 15px">
        结果：<b style="color: var(--el-color-primary)">{{ deriveResult }}</b>
      </div>
    </el-dialog>

    <el-dialog v-model="importVisible" title="Excel 导入（日期 / 是否交易日 / 日类型）" width="460px">
      <el-upload drag :auto-upload="false" :limit="1" accept=".xlsx" :on-change="onFilePick"
        :on-exceed="() => ElMessage.warning('一次仅可上传一个文件')">
        <el-icon style="font-size: 42px; color: #8c93a4"><Upload /></el-icon>
        <div>拖拽文件到此处，或 <em>点击选择 xlsx 文件</em></div>
      </el-upload>
      <div v-if="importResult" style="margin-top: 10px" class="import-result">{{ importResult }}</div>
      <template #footer>
        <el-button @click="importVisible = false; importFile = null">关闭</el-button>
        <el-button type="primary" :loading="importing" :disabled="!importFile" @click="doImport">上传并导入</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { ElMessage } from 'element-plus'
import { Search, Refresh, Upload, Download, Document } from '@element-plus/icons-vue'
import { http } from '@/api/http'
import TradingCalendarCheckPanel from '@/components/TradingCalendarCheckPanel.vue'

interface CalRow {
  date: string
  isTrading: boolean
  dayType: string
  source: string
}

const MARKET = 'SH'
const tab = ref<'calendar' | 'check'>('calendar')
const items = ref<CalRow[]>([])
const loading = ref(false)
const error = ref('')
const page = ref(1)
const pageSize = ref(30)
const total = ref(0)
const now = new Date()
const fmt = (d: Date) => d.toISOString().slice(0, 10)
const minus = (days: number) => { const d = new Date(now); d.setDate(d.getDate() - days); return fmt(d) }
const range = ref<[string, string]>([minus(30), fmt(new Date(now.getTime() + 30 * 86400000))])

async function load() {
  loading.value = true
  error.value = ''
  try {
    const res = (await http.get('/api/v1/trading-days', {
      params: { market: MARKET, from: range.value?.[0], to: range.value?.[1], page: page.value, pageSize: pageSize.value },
    })) as unknown as { items: CalRow[]; total: number }
    items.value = res?.items ?? []
    total.value = res?.total ?? 0
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

function search() { page.value = 1; load() }
function onPageChange(p: number) { page.value = p; load() }
function onSizeChange(s: number) { pageSize.value = s; page.value = 1; load() }

function dayTypeLabel(t: string) {
  return { trading: '交易日', weekend: '周末', holiday: '节假日', makeup: '调休交易日' }[t] ?? t
}

// ---------- 手工标记 ----------
const markVisible = ref(false)
const saving = ref(false)
const marking = ref<CalRow | null>(null)
function openMark(row: CalRow) {
  marking.value = { ...row }
  markVisible.value = true
}
async function saveMark() {
  if (!marking.value) return
  saving.value = true
  try {
    await http.put('/api/v1/trading-days', {
      market: MARKET,
      dates: [{ date: marking.value.date, isTrading: marking.value.isTrading, dayType: marking.value.dayType }],
    })
    ElMessage.success('已保存')
    markVisible.value = false
    await load()
  } finally {
    saving.value = false
  }
}

// ---------- 推导 ----------
const deriveVisible = ref(false)
const deriving = ref(false)
const deriveDate = ref('')
const deriveAction = ref('next')
const deriveN = ref(1)
const deriveResult = ref('')
async function doDerive() {
  deriving.value = true
  deriveResult.value = ''
  try {
    const data = (await http.post('/api/v1/trading-days/derive', {
      date: deriveDate.value, market: MARKET, action: deriveAction.value,
      n: deriveAction.value === 'offset' ? deriveN.value : 1,
    })) as unknown as { date: string }
    deriveResult.value = data.date
  } catch (e) {
    deriveResult.value = e instanceof Error ? e.message : String(e)
  } finally {
    deriving.value = false
  }
}

// ---------- 导入 / 导出 ----------
const importVisible = ref(false)
const importing = ref(false)
const importFile = ref<File | null>(null)
const importResult = ref('')
function onFilePick(f: { raw: File }) { importFile.value = f.raw; importResult.value = '' }
async function doImport() {
  if (!importFile.value) return
  importing.value = true
  importResult.value = ''
  try {
    const fd = new FormData()
    fd.append('file', importFile.value)
    const token = localStorage.getItem('cap_access_token')
    const resp = await fetch('/api/v1/trading-days/import', {
      method: 'POST', headers: token ? { Authorization: `Bearer ${token}` } : {}, body: fd,
    })
    const body = await resp.json().catch(() => null)
    if (body && body.code === 0) {
      importResult.value = `成功导入 ${body.data.saved} 行`
      ElMessage.success(`成功导入 ${body.data.saved} 行`)
      importFile.value = null
      await load()
    } else {
      importResult.value = '导入失败: ' + (body?.message ?? resp.status)
    }
  } catch (e) {
    importResult.value = '导入失败: ' + (e instanceof Error ? e.message : String(e))
  } finally {
    importing.value = false
  }
}
async function downloadBlob(url: string, filename: string) {
  const resp = await http.get(url, { responseType: 'blob' }) as unknown as Blob
  const a = document.createElement('a')
  a.href = URL.createObjectURL(resp)
  a.download = filename
  a.click()
  URL.revokeObjectURL(a.href)
}
async function doExport() {
  try { await downloadBlob('/api/v1/trading-days/export?market=' + MARKET + '&from=' + range.value?.[0] + '&to=' + range.value?.[1], 'trading_days.xlsx') }
  catch (e) { ElMessage.error('导出失败: ' + (e instanceof Error ? e.message : String(e))) }
}
async function downloadTemplate() {
  try { await downloadBlob('/api/v1/trading-days/template', 'trading_day_template.xlsx') }
  catch (e) { ElMessage.error('模板下载失败: ' + (e instanceof Error ? e.message : String(e))) }
}

onMounted(load)
</script>

<style scoped>
.toolbar { display: flex; gap: 8px; align-items: center; margin-bottom: 14px; }
.import-result { color: var(--el-color-primary); }
.pager { margin-top: 12px; justify-content: flex-end; }
</style>
