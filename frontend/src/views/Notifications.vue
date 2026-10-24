<template>
  <div class="page-card">
    <div class="toolbar">
      <div class="toolbar-title">通知订阅</div>
      <el-button :icon="Refresh" circle @click="reloadActive" />
    </div>

    <el-tabs v-model="tab" @tab-change="loadActive">
      <el-tab-pane label="通知渠道" name="channels">
        <div class="section-head">
          <span>邮件与 IM/Webhook 渠道</span>
          <el-button type="primary" :icon="Plus" @click="openChannel()">新建渠道</el-button>
        </div>
        <el-table :data="channels" v-loading="loading" border stripe>
          <el-table-column prop="name" label="名称" min-width="170" show-overflow-tooltip />
          <el-table-column prop="type" label="类型" width="110" />
          <el-table-column prop="healthStatus" label="健康状态" width="110">
            <template #default="{ row }"><el-tag :type="row.healthStatus === 'ok' ? 'success' : row.healthStatus === 'error' ? 'danger' : 'info'">{{ row.healthStatus }}</el-tag></template>
          </el-table-column>
          <el-table-column prop="lastError" label="最近错误" min-width="200" show-overflow-tooltip />
          <el-table-column label="状态" width="90">
            <template #default="{ row }"><el-tag :type="row.enabled ? 'success' : 'info'">{{ row.enabled ? '启用' : '停用' }}</el-tag></template>
          </el-table-column>
          <el-table-column label="操作" width="230" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" size="small" :loading="testingId === row.id" @click="testChannel(row)">测试</el-button>
              <el-button link type="primary" size="small" @click="openChannel(row)">编辑</el-button>
              <el-button link type="danger" size="small" @click="removeChannel(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="消息模板" name="templates">
        <div class="section-head">
          <span>按事件和渠道类型匹配模板</span>
          <el-button type="primary" :icon="Plus" @click="openTemplate()">新建模板</el-button>
        </div>
        <el-table :data="templates" v-loading="loading" border stripe>
          <el-table-column prop="name" label="名称" min-width="170" />
          <el-table-column prop="event" label="事件" width="110" />
          <el-table-column prop="channelType" label="渠道" width="110" />
          <el-table-column prop="format" label="格式" width="100" />
          <el-table-column prop="subject" label="标题" min-width="220" show-overflow-tooltip />
          <el-table-column prop="version" label="版本" width="70" align="right" />
          <el-table-column label="状态" width="80">
            <template #default="{ row }"><el-tag :type="row.enabled ? 'success' : 'info'">{{ row.enabled ? '启用' : '停用' }}</el-tag></template>
          </el-table-column>
          <el-table-column label="操作" width="140" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" size="small" @click="openTemplate(row)">编辑</el-button>
              <el-button link type="danger" size="small" @click="removeTemplate(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="订阅规则" name="subscriptions">
        <div class="section-head">
          <span>级别、范围、渠道和去重窗口</span>
          <el-button type="primary" :icon="Plus" @click="openSubscription()">新建订阅</el-button>
        </div>
        <el-table :data="subscriptions" v-loading="loading" border stripe>
          <el-table-column prop="name" label="名称" min-width="160" />
          <el-table-column prop="scopeType" label="范围" width="90" />
          <el-table-column prop="scopeValue" label="范围值" min-width="150" show-overflow-tooltip />
          <el-table-column prop="levels" label="级别" width="130" />
          <el-table-column prop="alertTypes" label="告警类型" width="130" />
          <el-table-column prop="dedupWindowMin" label="去重(min)" width="100" align="right" />
          <el-table-column prop="silentStart" label="静默开始" width="100" />
          <el-table-column prop="silentEnd" label="静默结束" width="100" />
          <el-table-column label="状态" width="80">
            <template #default="{ row }"><el-tag :type="row.enabled ? 'success' : 'info'">{{ row.enabled ? '启用' : '停用' }}</el-tag></template>
          </el-table-column>
          <el-table-column label="操作" width="140" fixed="right">
            <template #default="{ row }">
              <el-button link type="primary" size="small" @click="openSubscription(row)">编辑</el-button>
              <el-button link type="danger" size="small" @click="removeSubscription(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="维护静默" name="silences">
        <div class="section-head">
          <span>按全局、系统或指标设置维护窗口</span>
          <el-button type="primary" :icon="Plus" @click="silenceDialog = true">新增静默</el-button>
        </div>
        <el-table :data="silences" v-loading="loading" border stripe>
          <el-table-column prop="scopeType" label="范围" width="100" />
          <el-table-column prop="scopeValue" label="范围值" min-width="150" />
          <el-table-column prop="startAt" label="开始" width="180"><template #default="{ row }">{{ fmtTs(row.startAt) }}</template></el-table-column>
          <el-table-column prop="endAt" label="结束" width="180"><template #default="{ row }">{{ fmtTs(row.endAt) }}</template></el-table-column>
          <el-table-column prop="reason" label="原因" min-width="220" show-overflow-tooltip />
          <el-table-column prop="createdBy" label="创建人" width="110" />
          <el-table-column label="操作" width="90" fixed="right">
            <template #default="{ row }"><el-button link type="danger" size="small" @click="removeSilence(row)">删除</el-button></template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <el-tab-pane label="发送日志" name="logs">
        <div class="filter-row">
          <el-select v-model="logQ.status" clearable placeholder="状态" style="width: 120px">
            <el-option v-for="v in ['queued','sent','retrying','failed','suppressed']" :key="v" :label="v" :value="v" />
          </el-select>
          <el-input v-model="logQ.alertNo" placeholder="告警编号" clearable style="width: 190px" @keyup.enter="loadLogs" />
          <el-button type="primary" :icon="Search" @click="loadLogs">查询</el-button>
        </div>
        <el-table :data="logs" v-loading="loading" border stripe>
          <el-table-column prop="alertNo" label="告警编号" width="170" />
          <el-table-column prop="channelType" label="渠道" width="100" />
          <el-table-column prop="event" label="事件" width="100" />
          <el-table-column prop="level" label="级别" width="80"><template #default="{ row }">{{ String(row.level).toUpperCase() }}</template></el-table-column>
          <el-table-column prop="status" label="状态" width="100">
            <template #default="{ row }"><el-tag :type="logTag(row.status)">{{ row.status }}</el-tag></template>
          </el-table-column>
          <el-table-column prop="attempts" label="尝试" width="70" align="right" />
          <el-table-column prop="error" label="错误" min-width="220" show-overflow-tooltip />
          <el-table-column prop="createdAt" label="创建时间" width="170"><template #default="{ row }">{{ fmtTs(row.createdAt) }}</template></el-table-column>
          <el-table-column label="操作" width="90" fixed="right">
            <template #default="{ row }">
              <el-button v-if="['failed','retrying'].includes(row.status)" link type="primary" size="small" @click="retryLog(row)">重试</el-button>
            </template>
          </el-table-column>
        </el-table>
        <el-pagination v-model:current-page="logQ.page" :page-size="logQ.pageSize" :total="logTotal" layout="total, prev, pager, next" class="pager" @current-change="loadLogs" />
      </el-tab-pane>
    </el-tabs>

    <el-dialog v-model="channelDialog" :title="channelForm.id ? '编辑通知渠道' : '新建通知渠道'" width="620px">
      <el-form :model="channelForm" label-width="110px">
        <el-form-item label="渠道名称"><el-input v-model="channelForm.name" /></el-form-item>
        <el-form-item label="渠道类型">
          <el-select v-model="channelForm.type" style="width: 180px">
            <el-option v-for="t in ['smtp','wecom','dingtalk','feishu','webhook']" :key="t" :label="t" :value="t" />
          </el-select>
        </el-form-item>
        <template v-if="channelForm.type === 'smtp'">
          <el-form-item label="SMTP Host"><el-input v-model="channelForm.config.host" placeholder="smtp.example.com:465" /></el-form-item>
          <el-form-item label="发件人"><el-input v-model="channelForm.config.from" /></el-form-item>
          <el-form-item label="用户名"><el-input v-model="channelForm.config.username" /></el-form-item>
          <el-form-item label="密码"><el-input v-model="channelForm.config.password" type="password" show-password placeholder="编辑时留空或保留 ******" /></el-form-item>
        </template>
        <template v-else>
          <el-form-item label="Webhook URL"><el-input v-model="channelForm.config.url" /></el-form-item>
          <el-form-item label="签名密钥"><el-input v-model="channelForm.config.secret" type="password" show-password placeholder="可选" /></el-form-item>
        </template>
        <el-form-item label="测试收件人"><el-input v-model="channelForm.testRecipients" placeholder="多个地址用逗号分隔" /></el-form-item>
        <el-form-item label="启用"><el-switch v-model="channelForm.enabled" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="channelDialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveChannel">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="templateDialog" :title="templateForm.id ? '编辑消息模板' : '新建消息模板'" width="720px">
      <el-form :model="templateForm" label-width="110px">
        <el-form-item label="模板名称"><el-input v-model="templateForm.name" /></el-form-item>
        <el-form-item label="事件">
          <el-select v-model="templateForm.event" style="width: 160px"><el-option v-for="e in ['alert','escalate','recovered','report']" :key="e" :label="e" :value="e" /></el-select>
        </el-form-item>
        <el-form-item label="渠道类型">
          <el-select v-model="templateForm.channelType" style="width: 180px">
            <el-option label="通用 (*)" value="*" />
            <el-option v-for="t in ['smtp','wecom','dingtalk','feishu','webhook']" :key="t" :label="t" :value="t" />
          </el-select>
        </el-form-item>
        <el-form-item label="格式">
          <el-radio-group v-model="templateForm.format"><el-radio value="text">text</el-radio><el-radio value="markdown">markdown</el-radio><el-radio value="card">card</el-radio></el-radio-group>
        </el-form-item>
        <el-form-item label="标题"><el-input v-model="templateForm.subject" /></el-form-item>
        <el-form-item label="正文"><el-input v-model="templateForm.body" type="textarea" :rows="6" /></el-form-item>
        <el-form-item label="变量">
          <el-select v-model="templateForm.variableList" multiple filterable allow-create default-first-option style="width: 100%">
            <el-option v-for="v in variableOptions" :key="v" :label="v" :value="v" />
          </el-select>
        </el-form-item>
        <el-form-item label="启用"><el-switch v-model="templateForm.enabled" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="templateDialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveTemplate">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="subscriptionDialog" :title="subscriptionForm.id ? '编辑通知订阅' : '新建通知订阅'" width="700px">
      <el-form :model="subscriptionForm" label-width="110px">
        <el-form-item label="订阅名称"><el-input v-model="subscriptionForm.name" /></el-form-item>
        <el-form-item label="作用域">
          <el-select v-model="subscriptionForm.scopeType" style="width: 140px"><el-option label="全局" value="global" /><el-option label="系统" value="system" /><el-option label="指标" value="metric" /></el-select>
          <el-select v-if="subscriptionForm.scopeType === 'system'" v-model="subscriptionForm.scopeValue" filterable style="margin-left: 8px; width: 280px">
            <el-option v-for="s in systems" :key="s.systemCode" :label="s.name" :value="s.systemCode" />
          </el-select>
          <el-select v-if="subscriptionForm.scopeType === 'metric'" v-model="subscriptionForm.scopeValue" filterable style="margin-left: 8px; width: 300px">
            <el-option v-for="m in metrics" :key="m.metricCode" :label="`${m.name} (${m.metricCode})`" :value="m.metricCode" />
          </el-select>
        </el-form-item>
        <el-form-item label="级别">
          <el-checkbox-group v-model="subscriptionForm.levels"><el-checkbox v-for="l in ['p1','p2','p3','p4']" :key="l" :value="l">{{ l.toUpperCase() }}</el-checkbox></el-checkbox-group>
        </el-form-item>
        <el-form-item label="告警类型"><el-input v-model="subscriptionForm.alertTypesText" placeholder="* 或逗号分隔, 如 threshold,missing" /></el-form-item>
        <el-form-item label="通知渠道">
          <el-select v-model="subscriptionForm.channelIds" multiple style="width: 100%">
            <el-option v-for="c in channels" :key="c.id" :label="`${c.name} (${c.type})`" :value="c.id" />
          </el-select>
        </el-form-item>
        <el-form-item label="收件人"><el-input v-model="subscriptionForm.recipientsText" placeholder="SMTP 收件人, 多个逗号分隔" /></el-form-item>
        <el-form-item label="静默时段">
          <el-time-select v-model="subscriptionForm.silentStart" start="00:00" step="00:15" end="23:45" clearable placeholder="开始" />
          <span class="dash">至</span>
          <el-time-select v-model="subscriptionForm.silentEnd" start="00:00" step="00:15" end="23:45" clearable placeholder="结束" />
        </el-form-item>
        <el-form-item label="去重窗口"><el-input-number v-model="subscriptionForm.dedupWindowMin" :min="0" :max="1440" controls-position="right" /><span class="hint">分钟</span></el-form-item>
        <el-form-item label="启用"><el-switch v-model="subscriptionForm.enabled" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="subscriptionDialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveSubscription">保存</el-button>
      </template>
    </el-dialog>

    <el-dialog v-model="silenceDialog" title="新增维护静默" width="560px">
      <el-form :model="silenceForm" label-width="100px">
        <el-form-item label="作用域">
          <el-select v-model="silenceForm.scopeType" style="width: 140px"><el-option label="全局" value="global" /><el-option label="系统" value="system" /><el-option label="指标" value="metric" /></el-select>
        </el-form-item>
        <el-form-item v-if="silenceForm.scopeType !== 'global'" label="范围值">
          <el-input v-model="silenceForm.scopeValue" placeholder="系统编码或指标编码" />
        </el-form-item>
        <el-form-item label="时间范围"><el-date-picker v-model="silenceForm.range" type="datetimerange" value-format="YYYY-MM-DDTHH:mm:ss" start-placeholder="开始" end-placeholder="结束" /></el-form-item>
        <el-form-item label="静默原因"><el-input v-model="silenceForm.reason" type="textarea" :rows="3" /></el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="silenceDialog = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveSilence">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, reactive, ref } from 'vue'
import { Plus, Refresh, Search } from '@element-plus/icons-vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { http } from '@/api/http'
import { fmtTs } from '@/utils/format'

const tab = ref('channels')
const loading = ref(false)
const saving = ref(false)
const testingId = ref<number | null>(null)
const channels = ref<any[]>([])
const templates = ref<any[]>([])
const subscriptions = ref<any[]>([])
const silences = ref<any[]>([])
const logs = ref<any[]>([])
const logTotal = ref(0)
const metrics = ref<any[]>([])
const systems = ref<any[]>([])
const logQ = reactive({ status: '', alertNo: '', page: 1, pageSize: 20 })
const variableOptions = ['alertNo','systemCode','metricCode','level','value','threshold','waterLevel','durationMin','triggerAt','recoveredAt','evidenceWindow','link']

const channelDialog = ref(false)
const channelForm = reactive<any>({ id: null, name: '', type: 'smtp', enabled: true, config: {}, testRecipients: '' })
const templateDialog = ref(false)
const templateForm = reactive<any>({ id: null, name: '', event: 'alert', channelType: '*', format: 'markdown', subject: '', body: '', variableList: [], enabled: true })
const subscriptionDialog = ref(false)
const subscriptionForm = reactive<any>({ id: null, name: '', scopeType: 'global', scopeValue: '', levels: ['p1','p2','p3','p4'], alertTypesText: '*', recipientsText: '', channelIds: [], silentStart: '', silentEnd: '', dedupWindowMin: 60, enabled: true })
const silenceDialog = ref(false)
const silenceForm = reactive<any>({ scopeType: 'global', scopeValue: '', range: null, reason: '' })

async function loadOptions() {
  const [ms, ss] = await Promise.all([
    http.get('/api/v1/metrics', { params: { pageSize: 500 } }) as unknown as Promise<any>,
    http.get('/api/v1/systems', { params: { pageSize: 500 } }) as unknown as Promise<any>,
  ])
  metrics.value = ms.items || []
  systems.value = ss.items || []
}

async function loadChannels() {
  const res = await http.get('/api/v1/notification-channels') as unknown as any
  channels.value = res.items || []
}
async function loadTemplates() {
  const res = await http.get('/api/v1/notification-templates') as unknown as any
  templates.value = res.items || []
}
async function loadSubscriptions() {
  const res = await http.get('/api/v1/notification-subscriptions') as unknown as any
  subscriptions.value = res.items || []
}
async function loadSilences() {
  const res = await http.get('/api/v1/notification-silences') as unknown as any
  silences.value = res.items || []
}
async function loadLogs() {
  const res = await http.get('/api/v1/notification-logs', { params: { ...logQ } }) as unknown as any
  logs.value = res.items || []
  logTotal.value = res.total || 0
}

async function loadActive() {
  loading.value = true
  try {
    if (tab.value === 'channels') await loadChannels()
    else if (tab.value === 'templates') await loadTemplates()
    else if (tab.value === 'subscriptions') await loadSubscriptions()
    else if (tab.value === 'silences') await loadSilences()
    else await loadLogs()
  } catch (e) {
    ElMessage.error(e instanceof Error ? e.message : String(e))
  } finally {
    loading.value = false
  }
}
function reloadActive() { loadActive() }

function openChannel(row?: any) {
  Object.assign(channelForm, {
    id: row?.id || null, name: row?.name || '', type: row?.type || 'smtp',
    enabled: row?.enabled ?? true, config: { ...(row?.config || {}) },
    testRecipients: Array.isArray(row?.config?.testRecipients) ? row.config.testRecipients.join(',') : (row?.config?.testRecipients || ''),
  })
  channelDialog.value = true
}
async function saveChannel() {
  if (!channelForm.name.trim()) { ElMessage.warning('请填写渠道名称'); return }
  const config = { ...channelForm.config, testRecipients: splitCSV(channelForm.testRecipients) }
  saving.value = true
  try {
    const payload = { name: channelForm.name, type: channelForm.type, config, enabled: channelForm.enabled }
    if (channelForm.id) await http.put(`/api/v1/notification-channels/${channelForm.id}`, payload)
    else await http.post('/api/v1/notification-channels', payload)
    channelDialog.value = false
    ElMessage.success('渠道已保存')
    await loadChannels()
  } catch (e) { ElMessage.error(e instanceof Error ? e.message : String(e)) } finally { saving.value = false }
}
async function testChannel(row: any) {
  testingId.value = row.id
  try {
    await http.post(`/api/v1/notification-channels/${row.id}/test`)
    ElMessage.success('渠道测试成功')
    await loadChannels()
  } catch (e) { ElMessage.error(e instanceof Error ? e.message : String(e)) } finally { testingId.value = null }
}
async function removeChannel(row: any) {
  try {
    await ElMessageBox.confirm(`确认删除渠道“${row.name}”？`, '提示', { type: 'warning' })
    await http.delete(`/api/v1/notification-channels/${row.id}`)
    await loadChannels()
  } catch (e) { if (e !== 'cancel') ElMessage.error(e instanceof Error ? e.message : String(e)) }
}

function openTemplate(row?: any) {
  Object.assign(templateForm, {
    id: row?.id || null, name: row?.name || '', event: row?.event || 'alert',
    channelType: row?.channelType || '*', format: row?.format || 'markdown',
    subject: row?.subject || '', body: row?.body || '', enabled: row?.enabled ?? true,
    variableList: parseJSONArray(row?.variables),
  })
  templateDialog.value = true
}
async function saveTemplate() {
  saving.value = true
  try {
    const payload = { ...templateForm, variables: templateForm.variableList }
    delete payload.variableList
    if (templateForm.id) await http.put(`/api/v1/notification-templates/${templateForm.id}`, payload)
    else await http.post('/api/v1/notification-templates', payload)
    templateDialog.value = false
    ElMessage.success('模板已保存')
    await loadTemplates()
  } catch (e) { ElMessage.error(e instanceof Error ? e.message : String(e)) } finally { saving.value = false }
}
async function removeTemplate(row: any) {
  try {
    await ElMessageBox.confirm(`确认删除模板“${row.name}”？`, '提示', { type: 'warning' })
    await http.delete(`/api/v1/notification-templates/${row.id}`)
    await loadTemplates()
  } catch (e) { if (e !== 'cancel') ElMessage.error(e instanceof Error ? e.message : String(e)) }
}

function openSubscription(row?: any) {
  Object.assign(subscriptionForm, {
    id: row?.id || null, name: row?.name || '', scopeType: row?.scopeType || 'global',
    scopeValue: row?.scopeValue || '', levels: String(row?.levels || 'p1,p2,p3,p4').split(',').filter(Boolean),
    alertTypesText: row?.alertTypes || '*', recipientsText: parseJSONArray(row?.recipients).join(','),
    channelIds: parseJSONArray(row?.channelIds).map(Number), silentStart: row?.silentStart || '',
    silentEnd: row?.silentEnd || '', dedupWindowMin: row?.dedupWindowMin ?? 60, enabled: row?.enabled ?? true,
  })
  subscriptionDialog.value = true
}
async function saveSubscription() {
  saving.value = true
  try {
    const payload = {
      name: subscriptionForm.name, scopeType: subscriptionForm.scopeType,
      scopeValue: subscriptionForm.scopeType === 'global' ? '' : subscriptionForm.scopeValue,
      levels: subscriptionForm.levels, alertTypes: splitCSV(subscriptionForm.alertTypesText),
      recipients: splitCSV(subscriptionForm.recipientsText), channelIds: subscriptionForm.channelIds,
      silentStart: subscriptionForm.silentStart, silentEnd: subscriptionForm.silentEnd,
      dedupWindowMin: subscriptionForm.dedupWindowMin, enabled: subscriptionForm.enabled,
    }
    if (subscriptionForm.id) await http.put(`/api/v1/notification-subscriptions/${subscriptionForm.id}`, payload)
    else await http.post('/api/v1/notification-subscriptions', payload)
    subscriptionDialog.value = false
    ElMessage.success('订阅已保存')
    await loadSubscriptions()
  } catch (e) { ElMessage.error(e instanceof Error ? e.message : String(e)) } finally { saving.value = false }
}
async function removeSubscription(row: any) {
  try {
    await ElMessageBox.confirm(`确认删除订阅“${row.name}”？`, '提示', { type: 'warning' })
    await http.delete(`/api/v1/notification-subscriptions/${row.id}`)
    await loadSubscriptions()
  } catch (e) { if (e !== 'cancel') ElMessage.error(e instanceof Error ? e.message : String(e)) }
}

async function saveSilence() {
  if (!silenceForm.range?.[0] || !silenceForm.range?.[1]) { ElMessage.warning('请选择静默时间'); return }
  saving.value = true
  try {
    await http.post('/api/v1/notification-silences', {
      scopeType: silenceForm.scopeType,
      scopeValue: silenceForm.scopeType === 'global' ? '' : silenceForm.scopeValue,
      startAt: silenceForm.range[0], endAt: silenceForm.range[1], reason: silenceForm.reason,
    })
    silenceDialog.value = false
    ElMessage.success('静默窗口已创建')
    await loadSilences()
  } catch (e) { ElMessage.error(e instanceof Error ? e.message : String(e)) } finally { saving.value = false }
}
async function removeSilence(row: any) {
  try {
    await http.delete(`/api/v1/notification-silences/${row.id}`)
    await loadSilences()
  } catch (e) { ElMessage.error(e instanceof Error ? e.message : String(e)) }
}
async function retryLog(row: any) {
  try {
    await http.post(`/api/v1/notification-logs/${row.id}/retry`)
    ElMessage.success('已执行重试')
    await loadLogs()
  } catch (e) { ElMessage.error(e instanceof Error ? e.message : String(e)) }
}

function logTag(status: string) {
  return ({ sent: 'success', failed: 'danger', retrying: 'warning', suppressed: 'info' } as Record<string, string>)[status] || 'info'
}
function splitCSV(v: string) {
  return String(v || '').split(',').map(x => x.trim()).filter(Boolean)
}
function parseJSONArray(v: any): string[] {
  if (Array.isArray(v)) return v.map(String)
  try {
    const parsed = JSON.parse(v || '[]')
    return Array.isArray(parsed) ? parsed.map(String) : []
  } catch {
    return String(v || '').split(',').map(x => x.trim()).filter(Boolean)
  }
}

onMounted(async () => {
  await loadOptions()
  await loadActive()
})
</script>

<style scoped>
.toolbar { display: flex; align-items: center; justify-content: space-between; margin-bottom: 12px; }
.toolbar-title { font-size: 15px; font-weight: 600; }
.section-head { display: flex; align-items: center; justify-content: space-between; margin: 4px 0 12px; color: #606266; font-size: 13px; }
.filter-row { display: flex; align-items: center; gap: 8px; margin-bottom: 12px; flex-wrap: wrap; }
.pager { margin-top: 12px; display: flex; justify-content: flex-end; }
.hint { color: #909399; font-size: 12px; margin-left: 8px; }
.dash { color: #909399; margin: 0 8px; }
</style>
