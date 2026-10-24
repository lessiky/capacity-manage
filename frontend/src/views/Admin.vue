<template>
  <div class="page-card">
    <el-tabs v-model="tab">
      <!-- ============ 用户 ============ -->
      <el-tab-pane label="用户管理" name="users">
        <div class="toolbar">
          <el-button type="success" size="small" :icon="Plus" @click="openUser">新建用户</el-button>
        </div>
        <el-table :data="users" v-loading="loading" border stripe>
          <el-table-column prop="id" label="ID" width="60" />
          <el-table-column prop="username" label="用户名" width="130" />
          <el-table-column prop="realName" label="姓名" width="110" />
          <el-table-column prop="dept" label="部门" width="110" />
          <el-table-column label="状态" width="80">
            <template #default="{ row }">
              <el-tag :type="row.status === 'active' ? 'success' : 'info'" size="small">{{ row.status === 'active' ? '启用' : '停用' }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="lastLoginAt" label="最近登录" width="160">
            <template #default="{ row }">{{ fmtTs(row.lastLoginAt) }}</template>
          </el-table-column>
          <el-table-column label="操作" min-width="300">
            <template #default="{ row }">
              <el-button link type="primary" @click="openRoles(row)">分配角色</el-button>
              <el-button link type="warning" @click="resetPwd(row)">重置密码</el-button>
              <el-button link :type="row.status === 'active' ? 'danger' : 'success'"
                @click="toggleUser(row)">{{ row.status === 'active' ? '停用' : '启用' }}</el-button>
              <el-button v-if="row.status === 'disabled'" link type="danger" @click="removeUser(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <!-- ============ 角色 ============ -->
      <el-tab-pane label="角色管理" name="roles">
        <div class="toolbar">
          <el-button type="success" size="small" :icon="Plus" @click="openRole">新建角色</el-button>
        </div>
        <el-table :data="roles" v-loading="loading" border stripe>
          <el-table-column prop="code" label="编码" width="140" />
          <el-table-column prop="name" label="名称" width="140" />
          <el-table-column label="内置" width="70">
            <template #default="{ row }"><el-tag v-if="row.builtin" type="info" size="small">是</el-tag><span v-else>否</span></template>
          </el-table-column>
          <el-table-column prop="remark" label="说明" min-width="220" show-overflow-tooltip />
          <el-table-column label="使用用户" width="100">
            <template #default="{ row }"><el-tag v-if="row.userCount > 0" type="warning" size="small">{{ row.userCount }} 个启用</el-tag><span v-else>0</span></template>
          </el-table-column>
          <el-table-column label="操作" width="140">
            <template #default="{ row }">
              <el-button link type="primary" @click="openRole(row)">编辑</el-button>
              <el-button link type="danger" :disabled="row.builtin || row.userCount > 0" @click="removeRole(row)">删除</el-button>
            </template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <!-- ============ 数据权限 ============ -->
      <el-tab-pane label="数据权限" name="scope">
        <el-alert type="info" :closable="false" show-icon style="margin-bottom: 12px"
          title="为指定用户授权可访问的业务系统（manage=可管理 / view=只读）。平台管理员默认拥有全部数据权限。" />
        <div class="toolbar">
          <el-select v-model="scopeUserId" clearable filterable placeholder="选择用户（留空=按角色）" style="width: 220px">
            <el-option v-for="u in users" :key="u.id" :label="u.username" :value="u.id" />
          </el-select>
          <el-select v-if="!scopeUserId" v-model="scopeRoleId" clearable filterable placeholder="或选择角色" style="width: 220px">
            <el-option v-for="r in roles" :key="r.id" :label="r.name" :value="r.id" />
          </el-select>
          <el-button type="primary" :icon="Plus" :disabled="!scopeUserId && !scopeRoleId" @click="openScope">授权</el-button>
          <el-button :icon="Refresh" circle @click="loadScopes" />
        </div>
        <el-table :data="scopes" v-loading="loading" border stripe>
          <el-table-column label="主体" min-width="160">
            <template #default="{ row }">
              <el-tag v-if="row.userId" size="small">用户</el-tag>
              <el-tag v-else size="small" type="info">角色</el-tag>
              <span style="margin-left: 6px">{{ row.userId ? (row.username || ('用户#' + row.userId)) : (row.roleName || ('角色#' + row.roleId)) }}</span>
            </template>
          </el-table-column>
          <el-table-column prop="systemCode" label="业务系统" min-width="160" />
          <el-table-column label="权限" width="100">
            <template #default="{ row }">
              <el-tag :type="row.scopeType === 'manage' ? 'danger' : 'info'" size="small">{{ row.scopeType === 'manage' ? '管理' : '只读' }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column label="操作" width="90">
            <template #default="{ row }"><el-button link type="danger" @click="removeScope(row)">撤销</el-button></template>
          </el-table-column>
        </el-table>
        <el-empty v-if="!loading && scopes.length === 0" description="暂无授权记录" />

        <el-dialog v-model="scopeVisible" title="授予数据权限" width="480px">
          <el-select v-model="scopePicks" multiple filterable placeholder="选择业务系统" style="width: 100%">
            <el-option v-for="s in allSystems" :key="s.systemCode" :label="s.name + ' (' + s.systemCode + ')'" :value="s.systemCode" />
          </el-select>
          <el-radio-group v-model="scopeType" style="margin-top: 12px">
            <el-radio value="view">只读(view)</el-radio>
            <el-radio value="manage">管理(manage)</el-radio>
          </el-radio-group>
          <template #footer>
            <el-button @click="scopeVisible = false">取消</el-button>
            <el-button type="primary" :loading="saving" :disabled="scopePicks.length === 0" @click="saveScope">保存授权</el-button>
          </template>
        </el-dialog>
      </el-tab-pane>

      <!-- ============ 系统参数 ============ -->
      <el-tab-pane label="系统参数" name="params">
        <el-alert type="info" :closable="false" show-icon style="margin-bottom: 12px"
          title="热参数：修改后免重启生效（平台品牌、阈值调度、登录策略、数据源告警等）。平台图标支持 PNG/JPEG/WebP/GIF/ICO，不超过 1 MiB。" />
        <el-table :data="params" v-loading="loading" border stripe>
          <el-table-column prop="key" label="参数" min-width="200" />
          <el-table-column label="值" min-width="220">
            <template #default="{ row }">
              <div v-if="row.key === 'platform_icon'" class="platform-icon-setting">
                <span class="platform-icon-preview" :class="{ 'has-image': !!row.value }">
                  <img v-if="row.value" :src="row.value" alt="" />
                  <span v-else>未设置</span>
                </span>
                <el-upload
                  accept="image/png,image/jpeg,image/webp,image/gif,image/x-icon"
                  :show-file-list="false"
                  :http-request="uploadPlatformIcon"
                >
                  <el-button size="small" :icon="Upload" :loading="platformIconSaving">上传图标</el-button>
                </el-upload>
                <el-button v-if="row.value" link type="danger" @click="clearPlatformIcon(row)">清除</el-button>
              </div>
              <el-input v-else v-model="row.value" size="small" @change="saveParam(row)" />
            </template>
          </el-table-column>
          <el-table-column prop="description" label="说明" min-width="240" show-overflow-tooltip />
        </el-table>
      </el-tab-pane>

      <!-- ============ 操作日志 ============ -->
      <el-tab-pane label="操作日志" name="logs">
        <div class="pager">
          <el-pagination v-model:current-page="logPage" :page-size="20" :total="logTotal" layout="total, prev, pager, next" @current-change="loadLogs" />
        </div>
        <el-table :data="logs" v-loading="loading" border stripe>
          <el-table-column prop="username" label="用户" width="110" />
          <el-table-column prop="module" label="模块" width="110" />
          <el-table-column prop="action" label="操作" width="150" />
          <el-table-column prop="target" label="对象" min-width="160" show-overflow-tooltip />
          <el-table-column prop="ip" label="IP" width="120" />
          <el-table-column prop="createdAt" label="时间" width="170">
            <template #default="{ row }">{{ fmtTs(row.createdAt) }}</template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <!-- ============ AI 配置 ============ -->
      <el-tab-pane label="AI 配置" name="ai">
        <el-alert type="info" :closable="false" show-icon style="margin-bottom: 12px"
          title="AI 服务采用 OpenAI 兼容协议（/chat/completions）。保存后免重启热生效；字段留空表示跟随环境变量（AI_ENABLED / AI_BASE_URL / AI_MODEL / AI_API_KEY / AI_TIMEOUT_SEC）。" />
        <el-form :model="aiForm" label-width="110px" style="max-width: 660px" v-loading="aiLoading">
          <el-form-item label="启用 AI">
            <el-switch v-model="aiForm.enabled" />
            <span class="dim" v-if="aiOverrides.includes('enabled')">（页面配置覆盖环境变量）</span>
          </el-form-item>
          <el-form-item label="Base URL" required>
            <el-input v-model="aiForm.baseURL" placeholder="https://.../v1" />
          </el-form-item>
          <el-form-item label="模型名称" required>
            <el-input v-model="aiForm.model" placeholder="如 gpt-4o / qwen-max" />
          </el-form-item>
          <el-form-item label="API Key">
            <el-input v-model="aiForm.apiKey" type="password" show-password placeholder="留空 = 不修改" />
          </el-form-item>
          <el-form-item label="超时(秒)">
            <el-input-number v-model="aiForm.timeoutSec" :min="5" :max="600" :step="5" style="width: 160px" />
          </el-form-item>
          <el-form-item>
            <el-button type="primary" :loading="aiSaving" @click="saveAi">保存</el-button>
            <el-button :loading="aiTesting" @click="testAi">测试连接</el-button>
            <span class="dim" v-if="aiForm.apiKey">API Key 已配置（尾号 {{ aiForm.apiKey.slice(-4) }}）</span>
            <span class="dim" v-else>API Key 未配置</span>
          </el-form-item>
        </el-form>
      </el-tab-pane>

      <!-- ============ AI 调用日志 ============ -->
      <el-tab-pane label="AI 调用日志" name="ailogs">
        <el-table :data="aiLogs" v-loading="aiLogsLoading" border stripe size="small">
          <el-table-column prop="feature" label="功能" width="110">
            <template #default="{ row }">{{ aiFeatureLabel(row.feature) }}</template>
          </el-table-column>
          <el-table-column prop="userId" label="用户" width="120">
            <template #default="{ row }">{{ row.userId || '-' }}</template>
          </el-table-column>
          <el-table-column prop="model" label="模型" width="140" show-overflow-tooltip />
          <el-table-column prop="status" label="状态" width="90">
            <template #default="{ row }">
              <el-tag size="small" :type="row.status === 'success' ? 'success' : row.status === 'degraded' ? 'warning' : 'danger'">{{ row.status }}</el-tag>
            </template>
          </el-table-column>
          <el-table-column prop="latencyMs" label="耗时(ms)" width="90">
            <template #default="{ row }">{{ row.latencyMs ?? '-' }}</template>
          </el-table-column>
          <el-table-column prop="inputDigest" label="输入摘要" min-width="180" show-overflow-tooltip />
          <el-table-column prop="outputDigest" label="输出摘要" min-width="180" show-overflow-tooltip />
          <el-table-column prop="errorMsg" label="错误" min-width="160" show-overflow-tooltip />
          <el-table-column prop="createdAt" label="时间" width="170">
            <template #default="{ row }">{{ fmtTs(row.createdAt) }}</template>
          </el-table-column>
        </el-table>
      </el-tab-pane>

      <!-- ============ 平台自检 ============ -->
      <el-tab-pane label="平台自检" name="health">
        <div class="toolbar">
          <el-button :icon="Refresh" :loading="healthLoading" @click="loadHealth">刷新</el-button>
          <span class="dim">实时采集 调度器 / Worker / 存储 / 近5日汇总与报表 / 数据源 状态</span>
        </div>
        <el-descriptions v-if="health" :column="2" border size="small" style="margin-bottom: 12px">
          <el-descriptions-item label="Worker 沙箱">
            <el-tag size="small" :type="health.worker?.ok ? 'success' : 'danger'">{{ health.worker?.ok ? '正常' : '异常' }}</el-tag>
            <span v-if="health.worker?.error" class="dim">{{ health.worker.error }}</span>
          </el-descriptions-item>
          <el-descriptions-item label="报表生成时间">每日 {{ health.reportTime }}（参数 daily_report_time）</el-descriptions-item>
          <el-descriptions-item label="调度器">
            启动于 {{ fmtTs(health.scheduler?.startedAt) }} · 定时流程 {{ health.scheduler?.scheduledFlows ?? 0 }} · 运行中任务 {{ health.scheduler?.runningTasks ?? 0 }} · 今日触发 {{ health.scheduler?.tasksToday ?? 0 }}
          </el-descriptions-item>
          <el-descriptions-item label="存储">DB {{ fmtBytes(health.storage?.dbBytes) }} · 文件 {{ fmtBytes(health.storage?.fileBytes) }}</el-descriptions-item>
          <el-descriptions-item label="数据源">共 {{ health.dataSources?.total ?? 0 }} · 异常 {{ health.dataSources?.abnormal ?? 0 }}</el-descriptions-item>
          <el-descriptions-item label="分区数">{{ health.partitions }}</el-descriptions-item>
          <el-descriptions-item label="最近日汇总">
            <template v-if="health.lastDailySummary">{{ String(health.lastDailySummary.bizDate).slice(0, 10) }} · {{ health.lastDailySummary.status }}</template>
            <template v-else>-</template>
          </el-descriptions-item>
          <el-descriptions-item label="检查时间">{{ fmtTs(health.time) }}</el-descriptions-item>
        </el-descriptions>

        <el-divider content-position="left">近 5 日日汇总</el-divider>
        <el-table :data="health?.dailySummaries ?? []" border stripe size="small" style="margin-bottom: 12px">
          <el-table-column label="业务日" width="120"><template #default="{ row }">{{ String(row.bizDate).slice(0, 10) }}</template></el-table-column>
          <el-table-column label="状态" width="90">
            <template #default="{ row }"><el-tag size="small" :type="row.status === 'success' ? 'success' : row.status === 'running' ? 'info' : 'danger'">{{ row.status }}</el-tag></template>
          </el-table-column>
          <el-table-column prop="metricsTotal" label="指标总数" width="100"><template #default="{ row }">{{ row.metricsTotal ?? '-' }}</template></el-table-column>
          <el-table-column prop="metricsDone" label="已处理" width="90"><template #default="{ row }">{{ row.metricsDone ?? '-' }}</template></el-table-column>
          <el-table-column prop="metricsMissing" label="缺数" width="90" />
          <el-table-column label="完成时间" width="170"><template #default="{ row }">{{ fmtTs(row.finishedAt) }}</template></el-table-column>
          <el-table-column prop="errorMsg" label="错误" min-width="160" show-overflow-tooltip />
        </el-table>

        <el-divider content-position="left">近 5 日日报</el-divider>
        <el-table :data="health?.dailyReports ?? []" border stripe size="small" style="margin-bottom: 12px">
          <el-table-column label="业务日" width="120"><template #default="{ row }">{{ String(row.bizDate).slice(0, 10) }}</template></el-table-column>
          <el-table-column label="状态" width="90">
            <template #default="{ row }"><el-tag size="small" :type="row.status === 'success' ? 'success' : row.status === 'running' ? 'info' : 'danger'">{{ row.status }}</el-tag></template>
          </el-table-column>
          <el-table-column prop="metricsTotal" label="指标总数" width="100"><template #default="{ row }">{{ row.metricsTotal ?? '-' }}</template></el-table-column>
          <el-table-column prop="metricsOver" label="超线数" width="90"><template #default="{ row }">{{ row.metricsOver ?? '-' }}</template></el-table-column>
          <el-table-column label="生成时间" width="170"><template #default="{ row }">{{ fmtTs(row.generatedAt) }}</template></el-table-column>
          <el-table-column prop="errorMsg" label="错误" min-width="160" show-overflow-tooltip />
        </el-table>

        <el-divider content-position="left">数据源健康</el-divider>
        <el-table :data="dsList" v-loading="dsLoading" border stripe size="small">
          <el-table-column prop="name" label="名称" min-width="140" />
          <el-table-column prop="type" label="类型" width="80" />
          <el-table-column label="健康" width="100">
            <template #default="{ row }"><el-tag size="small" :type="healthTag(row.healthStatus)">{{ row.healthStatus }}</el-tag></template>
          </el-table-column>
          <el-table-column label="最近成功" width="170"><template #default="{ row }">{{ fmtTs(row.lastSuccessAt) }}</template></el-table-column>
          <el-table-column prop="lastError" label="最近错误" min-width="180" show-overflow-tooltip />
        </el-table>
      </el-tab-pane>

      <!-- ============ 保留与归档 ============ -->
      <el-tab-pane label="保留与归档" name="retention">
        <RetentionArchive v-if="tab === 'retention'" />
      </el-tab-pane>
    </el-tabs>

    <!-- 用户对话框 -->
    <el-dialog v-model="userVisible" title="新建用户" width="480px">
      <el-form label-width="90px">
        <el-form-item label="用户名" required><el-input v-model="userForm.username" /></el-form-item>
        <el-form-item label="姓名" required><el-input v-model="userForm.realName" /></el-form-item>
        <el-form-item label="初始密码" required><el-input v-model="userForm.password" type="password" show-password placeholder="8-32 位" /></el-form-item>
        <el-form-item label="部门"><el-input v-model="userForm.dept" /></el-form-item>
        <el-form-item label="角色">
          <el-select v-model="userForm.roleCodes" multiple style="width: 100%">
            <el-option v-for="r in roles" :key="r.code" :label="r.name" :value="r.code" />
          </el-select>
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="userVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveUser">创建</el-button>
      </template>
    </el-dialog>

    <!-- 角色对话框 -->
    <el-dialog v-model="roleVisible" :title="roleForm.id ? '编辑角色' : '新建角色'" width="560px">
      <el-form label-width="90px">
        <el-form-item label="编码" required><el-input v-model="roleForm.code" :disabled="!!roleForm.id" /></el-form-item>
        <el-form-item label="名称" required><el-input v-model="roleForm.name" /></el-form-item>
        <el-form-item label="说明"><el-input v-model="roleForm.remark" type="textarea" :rows="2" /></el-form-item>
        <el-form-item label="功能权限">
          <el-transfer v-model="roleForm.permissions" :data="permOptions" :disabled="roleForm.builtin"
            :titles="['未授予', '已授予']" style="--el-transfer-height: 260px" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="roleVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveRole">保存</el-button>
      </template>
    </el-dialog>

    <!-- 分配角色 -->
    <el-dialog v-model="rolesVisible" :title="'分配角色 - ' + (roleUser?.username ?? '')" width="440px">
      <el-checkbox-group v-model="pickedRoles">
        <el-checkbox v-for="r in roles" :key="r.code" :value="r.code" style="display: block; margin: 8px 0">{{ r.name }}</el-checkbox>
      </el-checkbox-group>
      <template #footer>
        <el-button @click="rolesVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveRoles">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, reactive, onMounted } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Plus, Refresh, Upload } from '@element-plus/icons-vue'
import { http } from '@/api/http'
import { fmtTs } from '@/utils/format'
import { useBrandingStore } from '@/stores/branding'
import RetentionArchive from '@/components/RetentionArchive.vue'

const tab = ref('users')
const loading = ref(false)
const saving = ref(false)
const branding = useBrandingStore()

async function fetchAll(fn: () => Promise<unknown>) {
  loading.value = true
  try { return await fn() } finally { loading.value = false }
}

// ---------- 用户 ----------
const users = ref<Record<string, any>[]>([])
async function loadUsers() {
  const data = (await fetchAll(() => http.get('/api/v1/admin/users', { params: { page: 1, pageSize: 200 } }))) as unknown as { items: Record<string, any>[] }
  users.value = data.items ?? []
}
const userVisible = ref(false)
const userForm = ref({ username: '', realName: '', password: '', dept: '', roleCodes: [] as string[] })
function openUser() { userForm.value = { username: '', realName: '', password: '', dept: '', roleCodes: [] }; userVisible.value = true }
async function saveUser() {
  const f = userForm.value
  if (!f.username || !f.realName || f.password.length < 8) { ElMessage.warning('请填写完整，密码至少 8 位'); return }
  saving.value = true
  try {
    await http.post('/api/v1/admin/users', f)
    ElMessage.success('用户已创建')
    userVisible.value = false
    await loadUsers()
  } finally { saving.value = false }
}
async function resetPwd(row: Record<string, any>) {
  const { value } = await ElMessageBox.prompt(`为用户 ${row.username} 设置新密码（8-32 位）`, '重置密码', { inputType: 'password' }).catch(() => ({ value: '' }))
  if (!value) return
  await http.post(`/api/v1/admin/users/${row.id}/reset-password`, { password: value })
  ElMessage.success('密码已重置')
}
async function toggleUser(row: Record<string, any>) {
  const next = row.status === 'active' ? 'disabled' : 'active'
  await http.put(`/api/v1/admin/users/${row.id}`, { status: next })
  ElMessage.success(next === 'active' ? '已启用' : '已停用')
  await loadUsers()
}
async function removeUser(row: Record<string, any>) {
  const ok = await ElMessageBox.confirm(
    `确认删除用户 ${row.username}？其角色分配与数据授权将一并移除，且不可恢复。`,
    '删除用户', { type: 'warning' }).then(() => true).catch(() => false)
  if (!ok) return
  await http.delete(`/api/v1/admin/users/${row.id}`)
  ElMessage.success('用户已删除')
  await loadUsers()
}
const rolesVisible = ref(false)
const roleUser = ref<Record<string, any> | null>(null)
const pickedRoles = ref<string[]>([])
function openRoles(row: Record<string, any>) {
  roleUser.value = row
  pickedRoles.value = [...(row.roleCodes ?? [])]
  rolesVisible.value = true
}
async function saveRoles() {
  saving.value = true
  try {
    await http.put(`/api/v1/admin/users/${roleUser.value!.id}/roles`, { roleCodes: pickedRoles.value })
    ElMessage.success('角色已更新')
    rolesVisible.value = false
    await loadUsers()
  } finally { saving.value = false }
}

// ---------- 角色 ----------
const roles = ref<Record<string, any>[]>([])
async function loadRoles() {
  const data = (await fetchAll(() => http.get('/api/v1/admin/roles'))) as unknown as Record<string, any>[]
  roles.value = Array.isArray(data) ? data : []
}
const permOptions = ref<{ key: string; label: string }[]>([])
async function loadPerms() {
  try {
    const data = (await http.get('/api/v1/admin/permissions')) as unknown as Record<string, any>[]
    permOptions.value = (Array.isArray(data) ? data : []).map(p => ({ key: p.code, label: p.name }))
  } catch { /* 忽略 */ }
}
const roleVisible = ref(false)
const roleForm = ref({ id: null as number | null, code: '', name: '', remark: '', permissions: [] as string[], builtin: false })
function openRole(row?: Record<string, any>) {
  if (row) roleForm.value = { id: row.id, code: row.code, name: row.name, remark: row.remark, permissions: [...(row.permissions ?? [])], builtin: !!row.builtin }
  else roleForm.value = { id: null, code: '', name: '', remark: '', permissions: [], builtin: false }
  roleVisible.value = true
}
async function saveRole() {
  const f = roleForm.value
  if (!f.code || !f.name) { ElMessage.warning('请填写编码与名称'); return }
  saving.value = true
  try {
    if (f.id) {
      await http.put(`/api/v1/admin/roles/${f.id}`, { name: f.name, remark: f.remark, permissions: f.permissions })
    } else {
      await http.post('/api/v1/admin/roles', { code: f.code, name: f.name, remark: f.remark, permissions: f.permissions })
    }
    ElMessage.success('角色已保存')
    roleVisible.value = false
    await loadRoles()
  } finally { saving.value = false }
}

async function removeRole(row: Record<string, any>) {
  const ok = await ElMessageBox.confirm(
    `确认删除角色 ${row.name}（${row.code}）？该角色的功能权限与数据授权将一并移除，且不可恢复。`,
    '删除角色', { type: 'warning' }).then(() => true).catch(() => false)
  if (!ok) return
  await http.delete(`/api/v1/admin/roles/${row.id}`)
  ElMessage.success('角色已删除')
  await loadRoles()
}

// ---------- 数据权限 ----------
const scopes = ref<Record<string, any>[]>([])
const scopeUserId = ref<number | null>(null)
const scopeRoleId = ref<number | null>(null)
const allSystems = ref<Record<string, any>[]>([])
async function loadScopes() {
  const data = (await fetchAll(() => http.get('/api/v1/admin/data-scope'))) as unknown as Record<string, any>[]
  scopes.value = Array.isArray(data) ? data : []
}
async function loadAllSystems() {
  try {
    const data = (await http.get('/api/v1/systems', { params: { page: 1, pageSize: 200 } })) as unknown as { items: Record<string, any>[] }
    allSystems.value = data.items ?? []
  } catch { /* 忽略 */ }
}
const scopeVisible = ref(false)
const scopePicks = ref<string[]>([])
const scopeType = ref('view')
function openScope() { scopePicks.value = []; scopeType.value = 'view'; scopeVisible.value = true }
async function saveScope() {
  saving.value = true
  try {
    const body: Record<string, unknown> = {
      systems: scopePicks.value.map(c => ({ systemCode: c, scope: scopeType.value })),
    }
    if (scopeUserId.value) body.userId = scopeUserId.value
    else body.roleId = scopeRoleId.value
    await http.put('/api/v1/admin/data-scope', body)
    ElMessage.success('授权已保存')
    scopeVisible.value = false
    await loadScopes()
  } finally { saving.value = false }
}

async function removeScope(row: Record<string, any>) {
  const subject = row.userId ? `用户 ${row.username ?? row.userId}` : `角色 ${row.roleName ?? row.roleId}`
  const ok = await ElMessageBox.confirm(
    `确认撤销 ${subject} 对业务系统 ${row.systemCode} 的数据授权？`,
    '撤销授权', { type: 'warning' }).then(() => true).catch(() => false)
  if (!ok) return
  await http.delete(`/api/v1/admin/data-scope/${row.id}`)
  ElMessage.success('授权已撤销')
  await loadScopes()
}

// ---------- 参数 ----------
const params = ref<Record<string, any>[]>([])
const platformIconSaving = ref(false)
async function loadParams() {
  const data = (await fetchAll(() => http.get('/api/v1/admin/params'))) as unknown as Record<string, any>[]
  params.value = Array.isArray(data) ? data : []
}
async function saveParam(row: Record<string, any>) {
  try {
    await http.put('/api/v1/admin/params', { key: row.key, value: row.value })
    if (row.key === 'platform_name') await branding.load()
    ElMessage.success(`参数 ${row.key} 已更新`)
  } catch { /* 后端已提示 */ }
}
async function uploadPlatformIcon(options: { file: File }) {
  if (options.file.size > 1024 * 1024) {
    ElMessage.warning('图标不能超过 1 MiB')
    throw new Error('icon too large')
  }
  const form = new FormData()
  form.append('file', options.file)
  platformIconSaving.value = true
  try {
    await http.post('/api/v1/admin/params/platform-icon', form)
    await Promise.all([loadParams(), branding.load()])
    ElMessage.success('平台图标已更新')
  } finally {
    platformIconSaving.value = false
  }
}
async function clearPlatformIcon(row: Record<string, any>) {
  const ok = await ElMessageBox.confirm('确认清除平台图标？', '清除图标', { type: 'warning' })
    .then(() => true).catch(() => false)
  if (!ok) return
  await http.delete('/api/v1/admin/params/platform-icon')
  row.value = ''
  await branding.load()
  ElMessage.success('平台图标已清除')
}

// ---------- 日志 ----------
const logs = ref<Record<string, any>[]>([])
const logPage = ref(1)
const logTotal = ref(0)
async function loadLogs() {
  const data = (await fetchAll(() => http.get('/api/v1/admin/operate-logs', { params: { page: logPage.value, pageSize: 20 } }))) as unknown as { items: Record<string, any>[]; total: number }
  logs.value = data.items ?? []
  logTotal.value = data.total ?? 0
}

// ---------- AI 调用日志（FR-AI-04） ----------
// ---------- AI 配置（系统参数热生效） ----------
const aiLoading = ref(false)
const aiSaving = ref(false)
const aiTesting = ref(false)
const aiForm = reactive({ enabled: false, baseURL: '', model: '', apiKey: '', timeoutSec: 60 })
const aiOverrides = ref<string[]>([])
async function loadAiConfig() {
  aiLoading.value = true
  try {
    const d = (await http.get('/api/v1/admin/ai/config')) as unknown as Record<string, any>
    aiForm.enabled = !!d.enabled
    aiForm.baseURL = d.baseURL ?? ''
    aiForm.model = d.model ?? ''
    aiForm.apiKey = d.apiKey ?? ''
    aiForm.timeoutSec = d.timeoutSec ?? 60
    aiOverrides.value = d.overrides ?? []
  } catch { /* 后端已提示 */ } finally {
    aiLoading.value = false
  }
}
async function saveAi() {
  if (!aiForm.baseURL.trim()) { ElMessage.warning('请填写 Base URL'); return }
  if (!aiForm.model.trim()) { ElMessage.warning('请填写模型名称'); return }
  aiSaving.value = true
  try {
    const d = (await http.put('/api/v1/admin/ai/config', {
      enabled: aiForm.enabled,
      baseURL: aiForm.baseURL,
      model: aiForm.model,
      apiKey: aiForm.apiKey,
      timeoutSec: aiForm.timeoutSec,
    })) as unknown as Record<string, any>
    aiForm.apiKey = d.apiKey ?? ''
    aiOverrides.value = d.overrides ?? []
    ElMessage.success('AI 配置已保存（免重启热生效）')
  } catch { /* 后端已提示 */ } finally {
    aiSaving.value = false
  }
}
async function testAi() {
  if (!aiForm.baseURL.trim() || !aiForm.model.trim()) { ElMessage.warning('请先填写 Base URL 与模型名称'); return }
  aiTesting.value = true
  try {
    const r = (await http.post('/api/v1/admin/ai/test', {
      baseURL: aiForm.baseURL,
      model: aiForm.model,
      apiKey: aiForm.apiKey,
    })) as unknown as { ok: boolean; message: string; model?: string; latencyMs?: number }
    if (r.ok) ElMessage.success(`连接成功${r.model ? '（' + r.model + '）' : ''}，耗时 ${r.latencyMs}ms`)
    else ElMessage.error(r.message)
  } catch { /* 后端已提示 */ } finally {
    aiTesting.value = false
  }
}

// ---------- AI 调用日志（FR-AI-04） ----------
const aiLogs = ref<Record<string, any>[]>([])
const aiLogsLoading = ref(false)
const AI_FEATURE_LABELS: Record<string, string> = { assistant: '配置助手', flow_gen: '流程生成', clean_gen: '脚本生成', clean_tryrun: '脚本试跑' }
function aiFeatureLabel(f: string): string { return AI_FEATURE_LABELS[f] ?? f }
async function loadAiLogs() {
  aiLogsLoading.value = true
  try {
    aiLogs.value = ((await http.get('/api/v1/ai/logs')) as unknown as Record<string, any>[]) ?? []
  } catch {
    aiLogs.value = []
  } finally {
    aiLogsLoading.value = false
  }
}

// ---------- 平台自检（FR-ADM-08） ----------
const health = ref<Record<string, any> | null>(null)
const healthLoading = ref(false)
const dsList = ref<Record<string, any>[]>([])
const dsLoading = ref(false)
async function loadHealth() {
  healthLoading.value = true
  try {
    health.value = (await http.get('/api/v1/admin/health')) as unknown as Record<string, any>
  } catch {
    health.value = null
  } finally {
    healthLoading.value = false
  }
  dsLoading.value = true
  try {
    dsList.value = ((await http.get('/api/v1/data-sources')) as unknown as Record<string, any>[]) ?? []
  } catch {
    dsList.value = []
  } finally {
    dsLoading.value = false
  }
}
function fmtBytes(n: number | null | undefined): string {
  if (n == null) return '-'
  if (n < 1024) return n + ' B'
  const units = ['KB', 'MB', 'GB', 'TB']
  let v = n / 1024
  let i = 0
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++ }
  return v.toFixed(1) + ' ' + units[i]
}
function healthTag(s: string): string {
  return s === 'ok' ? 'success' : s === 'error' ? 'danger' : 'info'
}

onMounted(async () => {
  await Promise.all([loadUsers(), loadRoles(), loadPerms(), loadScopes(), loadParams(), loadLogs(), loadAllSystems(), loadAiConfig(), loadAiLogs(), loadHealth()])
})
</script>

<style scoped>
.toolbar { display: flex; gap: 8px; align-items: center; margin-bottom: 12px; }
.pager { display: flex; justify-content: flex-end; margin-bottom: 10px; }
.dim { color: #909399; font-size: 12px; margin-left: 4px; }
.platform-icon-setting { display: flex; align-items: center; gap: 8px; min-height: 32px; }
.platform-icon-preview {
  width: 32px; height: 32px; border: 1px solid #dcdfe6; border-radius: 6px;
  display: inline-flex; align-items: center; justify-content: center; flex: 0 0 32px;
  overflow: hidden; color: #c0c4cc; font-size: 11px; background: #fff;
}
.platform-icon-preview img { width: 100%; height: 100%; object-fit: contain; display: block; }
</style>
