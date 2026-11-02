<template>
  <div class="flow-variables">
    <div class="toolbar">
      <el-alert type="info" :closable="false" style="flex: 1; margin: 0"
        :title="builtinHint" />
      <el-button type="success" :icon="Plus" @click="openCreate">新增变量</el-button>
      <el-button :icon="Refresh" circle @click="load" />
    </div>

    <el-table :data="items" v-loading="loading" border stripe>
      <el-table-column prop="id" label="ID" width="70" />
      <el-table-column label="变量名" min-width="180">
        <template #default="{ row }">
          <code class="variable-name">{{ variablePlaceholder(row.name) }}</code>
        </template>
      </el-table-column>
      <el-table-column prop="description" label="变量说明" min-width="240" show-overflow-tooltip />
      <el-table-column prop="value" label="变量值" min-width="220" show-overflow-tooltip />
      <el-table-column prop="createdBy" label="创建人" width="110" />
      <el-table-column label="更新时间" width="170">
        <template #default="{ row }">{{ fmtTs(row.updatedAt) }}</template>
      </el-table-column>
      <el-table-column label="操作" width="130" fixed="right">
        <template #default="{ row }">
          <el-button link type="primary" @click="openEdit(row)">编辑</el-button>
          <el-button link type="danger" @click="doDelete(row)">删除</el-button>
        </template>
      </el-table-column>
    </el-table>
    <el-empty v-if="!loading && error" :description="error" />
    <el-empty v-else-if="!loading && items.length === 0" description="暂无公共自定义变量" />
    <el-pagination
      v-if="total > 0"
      class="pager"
      :current-page="page"
      :page-size="pageSize"
      :page-sizes="[20, 50, 100]"
      :total="total"
      layout="total, sizes, prev, pager, next"
      @current-change="onPageChange"
      @size-change="onSizeChange"
    />

    <el-dialog v-model="formVisible" :title="editingId ? '编辑公共变量' : '新增公共变量'" width="560px">
      <el-form :model="form" label-width="90px">
        <el-form-item label="变量名" required>
          <el-input v-model="form.name" placeholder="如 ClusterCode_2026, 仅支持大小写字母、数字和下划线" />
        </el-form-item>
        <el-form-item label="变量说明" required>
          <el-input v-model="form.description" type="textarea" :rows="2" placeholder="说明变量用途和取值范围" />
        </el-form-item>
        <el-form-item label="变量值" required>
          <el-input v-model="form.value" type="textarea" :rows="3" placeholder="按字面量参与采集模板替换" />
        </el-form-item>
        <div class="variable-preview" v-if="form.name.trim()">
          模板占位符: <code>{{ variablePlaceholder(form.name.trim()) }}</code>
        </div>
      </el-form>
      <template #footer>
        <el-button @click="formVisible = false">取消</el-button>
        <el-button type="primary" :loading="saving" @click="save">保存</el-button>
      </template>
    </el-dialog>
  </div>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from "vue"
import { ElMessage, ElMessageBox } from "element-plus"
import { Plus, Refresh } from "@element-plus/icons-vue"
import { http } from "@/api/http"
import { fmtTs } from "@/utils/format"
import {
  BUILTIN_FLOW_VARIABLE_NAMES,
  pageAfterDelete,
  validateFlowVariableName,
  type FlowVariableNameRow,
} from "@/utils/flowVariables"
import type { PageResult } from "@/composables/useList"

interface FlowVariableRow extends FlowVariableNameRow {
  description: string
  value: string
  createdBy: string
  createdAt: string
  updatedAt: string
}

const items = ref<FlowVariableRow[]>([])
const loading = ref(false)
const error = ref("")
const page = ref(1)
const pageSize = ref(20)
const total = ref(0)
const variablePlaceholder = (name: string) => "${" + name + "}"
const builtinHint = computed(() =>
  `内置变量: ${BUILTIN_FLOW_VARIABLE_NAMES.map(variablePlaceholder).join(" ")}`,
)

async function load() {
  loading.value = true
  error.value = ""
  try {
    const data = (await http.get("/api/v1/flow-variables", {
      params: { page: page.value, pageSize: pageSize.value },
    })) as unknown as PageResult<FlowVariableRow>
    items.value = data.items ?? []
    total.value = data.total ?? 0
  } catch (e) {
    error.value = e instanceof Error ? e.message : String(e)
  } finally {
    loading.value = false
  }
}

function onPageChange(value: number) {
  page.value = value
  load()
}

function onSizeChange(value: number) {
  pageSize.value = value
  page.value = 1
  load()
}

const formVisible = ref(false)
const saving = ref(false)
const editingId = ref<number | null>(null)
const form = ref({ name: "", description: "", value: "" })

function openCreate() {
  editingId.value = null
  form.value = { name: "", description: "", value: "" }
  formVisible.value = true
}

function openEdit(row: FlowVariableRow) {
  editingId.value = row.id
  form.value = { name: row.name, description: row.description, value: row.value }
  formVisible.value = true
}

async function save() {
  const name = form.value.name.trim()
  const nameError = validateFlowVariableName(name, items.value, editingId.value)
  if (nameError) {
    ElMessage.warning(nameError)
    return
  }
  if (!form.value.description.trim()) {
    ElMessage.warning("请填写变量说明")
    return
  }
  if (!form.value.value.trim()) {
    ElMessage.warning("请填写变量值")
    return
  }
  saving.value = true
  try {
    const body = {
      name,
      description: form.value.description.trim(),
      value: form.value.value,
    }
    if (editingId.value) {
      await http.put(`/api/v1/flow-variables/${editingId.value}`, body)
      ElMessage.success("已更新")
    } else {
      await http.post("/api/v1/flow-variables", body)
      ElMessage.success("已新增")
      page.value = 1
    }
    formVisible.value = false
    await load()
  } finally {
    saving.value = false
  }
}

async function doDelete(row: FlowVariableRow) {
  try {
    await ElMessageBox.confirm(`确认删除公共变量「${row.name}」?`, "提示", { type: "warning" })
  } catch {
    return
  }
  try {
    await http.delete(`/api/v1/flow-variables/${row.id}`)
    ElMessage.success("已删除")
    page.value = pageAfterDelete(page.value, items.value.length)
    await load()
  } catch {
    /* 请求拦截器已提示 */
  }
}

onMounted(load)
</script>

<style scoped>
.flow-variables { min-width: 0; }
.toolbar { display: flex; gap: 8px; align-items: center; margin-bottom: 14px; }
.pager { justify-content: flex-end; margin-top: 14px; }
.variable-name { color: var(--el-color-primary); font-family: Consolas, Monaco, monospace; }
.variable-preview {
  margin: -4px 0 4px 90px;
  font-size: 12px;
  color: var(--el-text-color-secondary);
}
.variable-preview code { color: var(--el-color-primary); }
</style>
