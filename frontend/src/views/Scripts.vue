<template>
  <div :class="embedded ? 'embedded-page' : 'page-card'">
    <div class="toolbar">
      <div v-if="!embedded" class="toolbar-title">脚本管理</div>
      <el-alert type="info" :closable="false" style="flex: 1; margin: 0"
        title="Python 由独立沙箱 Worker 执行: 无网络 / 只读文件系统 / 超时与内存限制 / 模块白名单, 版本保存前自动语法校验; Bash/PowerShell 随脚本采集下发到执行目标, 由目标机解释器执行" />
      <el-button type="primary" plain :icon="MagicStick" @click="openAi">AI 生成清洗脚本</el-button>
      <el-button type="success" :icon="Plus" @click="openCreate">登记脚本</el-button>
      <div style="flex: 0" />
      <el-button :icon="Refresh" circle @click="load" />
    </div>

    <el-table :data="items" v-loading="loading" border stripe>
      <el-table-column prop="id" label="ID" width="70" />
      <el-table-column prop="name" label="脚本名" min-width="180" show-overflow-tooltip />
      <el-table-column label="用途" width="100">
        <template #default="{ row }">
          <el-tag size="small" :type="row.scriptType === 'collect' ? '' : 'success'">{{ row.scriptType === "collect" ? "采集" : "清洗" }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column label="语言" width="110">
        <template #default="{ row }">
          <el-tag size="small" type="info">{{ languageLabel(row.language) }}</el-tag>
        </template>
      </el-table-column>
      <el-table-column prop="description" label="描述" min-width="200" show-overflow-tooltip />
      <el-table-column prop="createdBy" label="创建人" width="100" />
      <el-table-column label="创建时间" width="170">
        <template #default="{ row }">{{ fmtTs(row.createdAt) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="170" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" @click="openVersions(row)">版本管理</el-button>
          <el-button link type="danger" @click="doDelete(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
    <el-empty v-if="!loading && error" :description="error" />
    <el-empty v-else-if="!loading && items.length === 0" description="暂无脚本, 请先登记" />

    <!-- 登记脚本 -->
    <el-dialog v-model="formVisible" title="登记脚本" width="520px">
      <el-form :model="form" label-width="100px">
        <el-form-item label="脚本名" required>
          <el-input v-model="form.name" placeholder="唯一名称, 如 core_db_collect" />
        </el-form-item>
        <el-form-item label="用途" required>
          <el-radio-group v-model="form.scriptType" @change="onScriptTypeChange">
            <el-radio value="collect">采集</el-radio>
            <el-radio value="clean">清洗</el-radio>
          </el-radio-group>
        </el-form-item>
        <el-form-item label="语言" required>
          <el-radio-group v-model="form.language">
            <el-radio value="python">Python</el-radio>
            <el-radio value="bash" :disabled="form.scriptType === 'clean'">Bash</el-radio>
            <el-radio value="powershell" :disabled="form.scriptType === 'clean'">PowerShell</el-radio>
          </el-radio-group>
          <div class="form-tip">
            清洗脚本在本地沙箱执行, 仅支持 Python; 采集脚本可选 Python / Bash / PowerShell, 非 Python 仅随"脚本采集"下发到执行目标执行。
          </div>
        </el-form-item>
        <el-form-item label="描述">
          <el-input v-model="form.description" type="textarea" :rows="2" />
        </el-form-item>
      </el-form>
      <template #footer>
        <el-button @click="formVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="saveScript">登记</el-button>
      </template>
    </el-dialog>

    <!-- 版本管理 -->
    <el-drawer v-model="verVisible" :title="`脚本版本: ${currentScript?.name ?? ''}`" size="760px">
      <div v-if="versionsLoading" v-loading="versionsLoading" style="min-height: 100px" />
      <el-table v-else :data="versions" size="small" border>
        <el-table-column prop="version" label="版本" width="70" />
        <el-table-column label="状态" width="90">
          <template #default="{ row }">
            <el-tag size="small" :type="row.active ? 'success' : 'info'">{{ row.active ? "生效中" : "未生效" }}</el-tag>
          </template>
        </el-table-column>
        <el-table-column label="校验" width="90">
          <template #default="{ row }">{{ row.checksum ? "通过" : "-" }}</template>
        </el-table-column>
        <el-table-column prop="publishedBy" label="发布人" width="110" />
        <el-table-column label="发布时间" width="160">
          <template #default="{ row }">{{ fmtTs(row.publishedAt) }}</template>
        </el-table-column>
        <el-table-column prop="remark" label="备注" min-width="140" show-overflow-tooltip />
        <el-table-column label="操作" width="210" fixed="right">
          <template #default="{ row }">
            <el-button link type="primary" @click="openView(row)">查看</el-button>
            <el-button link type="warning" @click="openEdit(row)">编辑</el-button>
            <el-button v-if="!row.active" link type="primary" :loading="activatingV === row.version" @click="doActivate(row)">设为生效</el-button>
            <span v-else class="active-mark">当前生效</span>
          </template>
        </el-table-column>
      </el-table>

      <el-divider content-position="left">新增版本（{{ pythonScript ? "Python 保存前自动语法校验" : "由目标机解释器执行" }}）</el-divider>
      <el-input v-model="newContent" type="textarea" :rows="14" class="code-input"
        :placeholder="editorPlaceholder" />
      <el-input v-model="newRemark" placeholder="版本备注" style="margin-top: 8px" />
      <div style="margin-top: 12px; text-align: right">
        <el-button type="primary" :loading="savingVersion" @click="saveVersion">{{ pythonScript ? "校验并保存版本" : "保存版本" }}</el-button>
      </div>
    </el-drawer>

    <!-- 查看脚本内容 -->
    <el-dialog v-model="viewVisible" :title="`脚本内容: ${currentScript?.name ?? ''} v${viewVersion}`" width="760px" top="6vh">
      <el-descriptions :column="3" border size="small" style="margin-bottom: 10px">
        <el-descriptions-item label="状态">
          <el-tag size="small" :type="viewActive ? 'success' : 'info'">{{ viewActive ? "生效中" : "未生效" }}</el-tag>
        </el-descriptions-item>
        <el-descriptions-item label="发布人">{{ viewPublishedBy || "-" }}</el-descriptions-item>
        <el-descriptions-item label="发布时间">{{ viewPublishedAt ? fmtTs(viewPublishedAt) : "-" }}</el-descriptions-item>
        <el-descriptions-item label="备注" :span="3">{{ viewRemark || "-" }}</el-descriptions-item>
      </el-descriptions>
      <pre class="code-view">{{ viewContent || "(无内容)" }}</pre>
      <template #footer>
        <el-button @click="viewVisible = false">关闭</el-button>
        <el-button type="primary" @click="editFromView">编辑</el-button>
      </template>
    </el-dialog>

    <!-- 编辑脚本内容(保存为新版本) -->
    <el-dialog v-model="editVisible" :title="`编辑脚本: ${currentScript?.name ?? ''} (基于 v${editSourceVersion})`" width="760px" top="5vh" :close-on-click-modal="false">
      <el-alert type="info" :closable="false" style="margin-bottom: 10px"
        title="版本内容不可原地修改; 编辑后保存为新版本, 请试跑确认再设为生效" />
      <el-input v-model="editContent" type="textarea" :rows="16" class="code-input" :placeholder="editorPlaceholder" />
      <el-input v-model="editRemark" placeholder="版本备注" style="margin-top: 8px" />
      <template #footer>
        <el-button @click="editVisible = false">取消</el-button>
        <el-button type="primary" :loading="savingVersion" @click="saveEdit">{{ pythonScript ? "校验并保存为新版本" : "保存为新版本" }}</el-button>
      </template>
    </el-dialog>

    <!-- AI 生成清洗脚本（FR-AI-02） -->
    <el-dialog v-model="aiVisible" title="AI 生成清洗脚本（生成 → 试跑对比 → 保存为版本）" width="860px" top="4vh" :close-on-click-modal="false">
      <el-form label-width="100px">
        <el-form-item label="目标脚本" required>
          <el-radio-group v-model="aiTargetMode">
            <el-radio value="existing">已有清洗脚本</el-radio>
            <el-radio value="new">新建脚本</el-radio>
          </el-radio-group>
          <el-select v-if="aiTargetMode === 'existing'" v-model="aiScriptId" clearable filterable placeholder="选择清洗脚本" style="width: 260px">
            <el-option v-for="s in cleanScripts" :key="s.id" :label="s.name" :value="s.id" />
          </el-select>
          <el-input v-else v-model="aiNewName" placeholder="新脚本名, 如 core_db_clean" style="width: 260px" />
        </el-form-item>
        <el-form-item label="样例数据" required>
          <el-input v-model="aiSample" type="textarea" :rows="6" class="code-input"
            placeholder='非空 JSON 数组, 如 [{"value":1024,"unit":"MB"},{"value":2048,"unit":"MB"}]' />
        </el-form-item>
        <el-form-item label="清洗需求" required>
          <el-input v-model="aiRequirement" type="textarea" :rows="3" placeholder="如：单位换算为 GB、保留 2 位小数、剔除 value<=0 的行" />
        </el-form-item>
      </el-form>
      <div class="ai-actions">
        <el-button type="primary" :icon="MagicStick" :loading="aiGenLoading" @click="doAiGen">生成脚本</el-button>
        <el-button :disabled="!aiScript.trim()" :loading="aiTryLoading" @click="doAiTryRun">试跑对比</el-button>
        <el-button type="success" :disabled="!aiScript.trim()" :loading="aiSaveLoading" @click="doAiSave">保存为版本</el-button>
      </div>
      <el-alert v-if="aiNotice" type="warning" :closable="false" :title="aiNotice" style="margin: 8px 0" />
      <el-form-item v-if="aiScript" label="生成脚本" class="ai-script-item">
        <el-input v-model="aiScript" type="textarea" :rows="12" class="code-input" placeholder="AI 生成的 Python 脚本（可编辑）" />
      </el-form-item>
      <template v-if="tryRes">
        <el-divider content-position="left">试跑对比（沙箱内执行，不落库/不判定）</el-divider>
        <el-descriptions :column="4" border size="small" style="margin-bottom: 10px">
          <el-descriptions-item label="状态">
            <el-tag :type="tryRes.status === 'success' ? 'success' : 'danger'" size="small">{{ tryRes.status }}</el-tag>
          </el-descriptions-item>
          <el-descriptions-item label="耗时">{{ tryRes.durationMs }} ms</el-descriptions-item>
          <el-descriptions-item label="输入行数">{{ tryRes.inputRows }}</el-descriptions-item>
          <el-descriptions-item label="输出行数">{{ tryRes.outputRows }}</el-descriptions-item>
        </el-descriptions>
        <el-alert v-if="tryRes.error" type="error" :title="tryRes.error" :closable="false" style="margin-bottom: 10px" />
        <div class="compare-row">
          <div class="compare-col">
            <div class="compare-title">清洗前（输入）</div>
            <pre class="code-pre">{{ pretty(tryRes.input) }}</pre>
          </div>
          <div class="compare-col">
            <div class="compare-title">清洗后（输出）</div>
            <pre class="code-pre">{{ pretty(tryRes.output) }}</pre>
          </div>
        </div>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from "vue"
import { ElMessage, ElMessageBox } from "element-plus"
import { Plus, Refresh, MagicStick } from "@element-plus/icons-vue"
import { http } from "@/api/http"
import { fmtTs } from "@/utils/format"

defineProps<{ embedded?: boolean }>()

interface ScriptRow {
  id: number
  name: string
  scriptType: string
  language: string
  description: string
  createdBy: string
  createdAt: string
}
interface VersionRow {
  id: number
  scriptId: number
  version: number
  content: string
  active: boolean
  checksum: string
  publishedBy: string
  publishedAt: string
  remark: string
}

const items = ref<ScriptRow[]>([])
const loading = ref(false)
const error = ref("")
async function load() {
  loading.value = true
  error.value = ""
  try {
    const d = (await http.get("/api/v1/scripts")) as unknown as ScriptRow[]
    items.value = d ?? []
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

// ---------- 登记 ----------
const formVisible = ref(false)
const saving = ref(false)
const form = ref({ name: "", scriptType: "collect", language: "python", description: "" })
function openCreate() {
  form.value = { name: "", scriptType: "collect", language: "python", description: "" }
  formVisible.value = true
}
// 清洗脚本仅在本地沙箱执行, 后端只接受 python; 切换用途时归位语言选项
function onScriptTypeChange() {
  if (form.value.scriptType === "clean") form.value.language = "python"
}

const LANGUAGE_LABELS: Record<string, string> = { python: "Python", bash: "Bash", powershell: "PowerShell" }
function languageLabel(lang: string): string {
  return LANGUAGE_LABELS[lang] ?? "Python"
}
// 只有 Python 走本地沙箱语法校验; bash/powershell 由目标机解释器执行
const pythonScript = computed(() => (currentScript.value?.language ?? "python") === "python")
function savedMsg(version: number, suffix = ""): string {
  const note = pythonScript.value ? "语法校验通过" : "由目标机解释器执行, 未做本地语法校验"
  return `已保存 v${version}（${note}）${suffix}`
}

// 版本编辑时按语言给出提示, 避免 bash/powershell 脚本套用 Python 模板
const editorPlaceholder = computed(() => {
  const lang = currentScript.value?.language ?? "python"
  if (lang === "bash") return "# Bash 脚本, 入参读取同目录 params.json; stdout 最后一行输出 JSON"
  if (lang === "powershell") return "# PowerShell 脚本, 入参读取同目录 params.json; stdout 最后一行输出 JSON"
  return "import json, os, re\nparams = json.loads(os.environ.get('CAP_PARAMS', '{}'))\n# ... 采集: stdout 最后一行输出 JSON 数组\n#       [{\"metric\": \"xxx\", \"value\": 1.0, \"unit\": \"GB\"}]\n#   清洗: 读 CAP_INPUT 原始行数组, 输出同构数组"
})
async function saveScript() {
  if (!form.value.name.trim()) { ElMessage.warning("请输入脚本名"); return }
  saving.value = true
  try {
    await http.post("/api/v1/scripts", form.value)
    ElMessage.success("已登记, 请添加脚本版本")
    formVisible.value = false
    await load()
  } finally {
    saving.value = false
  }
}

// ---------- 版本 ----------
const verVisible = ref(false)
const versionsLoading = ref(false)
const currentScript = ref<ScriptRow | null>(null)
const versions = ref<VersionRow[]>([])
const newContent = ref("")
const newRemark = ref("")
const savingVersion = ref(false)
const activatingV = ref<number | null>(null)

async function openVersions(row: ScriptRow) {
  currentScript.value = row
  verVisible.value = true
  versionsLoading.value = true
  try {
    const d = (await http.get(`/api/v1/scripts/${row.id}/versions`)) as unknown as VersionRow[]
    versions.value = d ?? []
  } finally {
    versionsLoading.value = false
  }
}
async function saveVersion() {
  if (!currentScript.value) return
  if (!newContent.value.trim()) { ElMessage.warning("请输入脚本内容"); return }
  savingVersion.value = true
  try {
    const v = (await http.post(`/api/v1/scripts/${currentScript.value.id}/versions`, {
      content: newContent.value,
      remark: newRemark.value,
    })) as unknown as VersionRow
    ElMessage.success(savedMsg(v.version))
    newContent.value = ""
    newRemark.value = ""
    await openVersions(currentScript.value)
  } finally {
    savingVersion.value = false
  }
}
async function doActivate(row: VersionRow) {
  if (!currentScript.value) return
  activatingV.value = row.version
  try {
    await http.post(`/api/v1/scripts/${currentScript.value.id}/versions/${row.version}/activate`)
    ElMessage.success(`v${row.version} 已生效（热生效, 无需重启）`)
    await openVersions(currentScript.value)
  } finally {
    activatingV.value = null
  }
}
// ---------- 查看 / 编辑脚本内容 ----------
const viewVisible = ref(false)
const viewVersion = ref(0)
const viewContent = ref("")
const viewActive = ref(false)
const viewPublishedBy = ref("")
const viewPublishedAt = ref("")
const viewRemark = ref("")
function openView(row: VersionRow) {
  viewVersion.value = row.version
  viewContent.value = row.content ?? ""
  viewActive.value = row.active
  viewPublishedBy.value = row.publishedBy
  viewPublishedAt.value = row.publishedAt
  viewRemark.value = row.remark
  viewVisible.value = true
}
const editVisible = ref(false)
const editSourceVersion = ref(0)
const editContent = ref("")
const editRemark = ref("")
function openEdit(row: VersionRow) {
  editSourceVersion.value = row.version
  editContent.value = row.content ?? ""
  editRemark.value = `基于 v${row.version} 修改`
  editVisible.value = true
}
function editFromView() {
  const row = versions.value.find((v) => v.version === viewVersion.value)
  viewVisible.value = false
  if (row) openEdit(row)
}
async function saveEdit() {
  if (!currentScript.value) return
  if (!editContent.value.trim()) { ElMessage.warning("脚本内容不能为空"); return }
  savingVersion.value = true
  try {
    const v = (await http.post(`/api/v1/scripts/${currentScript.value.id}/versions`, {
      content: editContent.value,
      remark: editRemark.value,
    })) as unknown as VersionRow
    ElMessage.success(savedMsg(v.version, ", 请试跑确认后设为生效"))
    editVisible.value = false
    await openVersions(currentScript.value)
  } catch { /* 拦截器已提示 */ } finally {
    savingVersion.value = false
  }
}

async function doDelete(row: ScriptRow) {
  try {
    await ElMessageBox.confirm(`确认删除脚本「${row.name}」? 已被流程引用的脚本不允许删除, 删除后不可恢复。`, "警告", { type: "warning" })
  } catch { return }
  try {
    await http.delete(`/api/v1/scripts/${row.id}`)
    ElMessage.success("已删除")
    await load()
  } catch { /* 拦截器已提示 */ }
}

// ---------- AI 生成清洗脚本（FR-AI-02） ----------
const cleanScripts = computed(() => items.value.filter((s) => s.scriptType === "clean"))
const aiVisible = ref(false)
const aiTargetMode = ref<"existing" | "new">("existing")
const aiScriptId = ref<number | null>(null)
const aiNewName = ref("")
const aiSample = ref("")
const aiRequirement = ref("")
const aiGenLoading = ref(false)
const aiTryLoading = ref(false)
const aiSaveLoading = ref(false)
const aiScript = ref("")
const aiNotice = ref("")
interface TryRes {
  status: string
  input: unknown
  inputRows: number
  output: unknown
  outputRows: number
  durationMs: number
  error: string
  stdoutTail?: string
  stderrTail?: string
}
const tryRes = ref<TryRes | null>(null)

function openAi() {
  aiTargetMode.value = cleanScripts.value.length ? "existing" : "new"
  aiScriptId.value = cleanScripts.value[0]?.id ?? null
  aiNewName.value = ""
  aiSample.value = ""
  aiRequirement.value = ""
  aiScript.value = ""
  aiNotice.value = ""
  tryRes.value = null
  aiVisible.value = true
}

function validSample(): boolean {
  try {
    const arr = JSON.parse(aiSample.value)
    if (!Array.isArray(arr) || arr.length === 0) {
      ElMessage.warning("样例数据须为非空 JSON 数组")
      return false
    }
    return true
  } catch {
    ElMessage.warning("样例数据须为合法 JSON")
    return false
  }
}

function pretty(v: unknown): string {
  try { return JSON.stringify(v, null, 2) } catch { return String(v) }
}

async function doAiGen() {
  if (!validSample()) return
  if (!aiRequirement.value.trim()) { ElMessage.warning("请填写清洗需求"); return }
  if (aiTargetMode.value === "existing" && !aiScriptId.value) { ElMessage.warning("请选择清洗脚本"); return }
  if (aiTargetMode.value === "new" && !aiNewName.value.trim()) { ElMessage.warning("请填写新脚本名"); return }
  aiGenLoading.value = true
  try {
    const d = (await http.post("/api/v1/ai/clean-generate", {
      sampleData: aiSample.value,
      requirement: aiRequirement.value.trim(),
    })) as unknown as { script: string; notice: string }
    aiScript.value = d.script
    aiNotice.value = d.notice
    tryRes.value = null
  } finally {
    aiGenLoading.value = false
  }
}

async function doAiTryRun() {
  if (!validSample()) return
  aiTryLoading.value = true
  try {
    tryRes.value = (await http.post("/api/v1/ai/clean-tryrun", {
      script: aiScript.value,
      sampleData: aiSample.value,
    })) as unknown as TryRes
  } catch { /* 拦截器已提示 */ } finally {
    aiTryLoading.value = false
  }
}

async function doAiSave() {
  if (aiTargetMode.value === "existing" && aiScriptId.value == null) { ElMessage.warning("请选择清洗脚本"); return }
  if (aiTargetMode.value === "new" && !aiNewName.value.trim()) { ElMessage.warning("请填写新脚本名"); return }
  aiSaveLoading.value = true
  try {
    let sid: number
    if (aiTargetMode.value === "new") {
      const created = (await http.post("/api/v1/scripts", {
        name: aiNewName.value.trim(),
        scriptType: "clean",
        description: "AI 生成清洗脚本",
      })) as unknown as { id: number }
      sid = created.id
    } else {
      sid = Number(aiScriptId.value)
    }
    const v = (await http.post(`/api/v1/scripts/${sid}/versions`, {
      content: aiScript.value,
      remark: "AI 生成",
    })) as unknown as { version: number }
    ElMessage.success(`已保存 v${v.version}（语法校验通过），请在版本管理中试跑确认后设为生效`)
    aiVisible.value = false
    await load()
  } catch { /* 拦截器已提示 */ } finally {
    aiSaveLoading.value = false
  }
}

onMounted(load)
</script>

<style scoped>
.embedded-page { min-width: 0; }
.toolbar { display: flex; gap: 8px; align-items: center; margin-bottom: 14px; }
.toolbar-title { font-size: 15px; font-weight: 600; }
.form-tip { font-size: 12px; color: var(--el-text-color-secondary); line-height: 1.5; }
.active-mark { color: var(--el-color-success); font-size: 12px; }
.code-input :deep(textarea) { font-family: Consolas, Monaco, monospace; font-size: 13px; }
.ai-actions { display: flex; gap: 8px; margin-bottom: 8px; }
.ai-script-item { margin-top: 4px; }
.compare-row { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; }
.compare-col { min-width: 0; }
.compare-title { font-size: 12px; font-weight: 600; margin-bottom: 4px; }
.code-pre {
  background: #f7f8fa; border: 1px solid #ebeef5; border-radius: 6px; padding: 8px;
  font-size: 12px; line-height: 1.6; max-height: 260px; overflow: auto; white-space: pre-wrap; word-break: break-all;
}
.code-view {
  background: #f7f8fa; border: 1px solid #ebeef5; border-radius: 6px; padding: 10px;
  font-family: Consolas, Monaco, monospace; font-size: 12.5px; line-height: 1.6;
  max-height: 420px; overflow: auto; white-space: pre-wrap; word-break: break-all; margin: 0;
}
</style>
