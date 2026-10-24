<template>
  <div class="panel">
    <div class="toolbar">
      <el-input-number v-model="year" :min="2000" :max="2100" />
      <el-button type="primary" :icon="Search" :loading="checking" @click="check">检查完整性</el-button>
    </div>

    <el-alert
      v-if="result"
      :type="result.status === 'success' ? 'success' : 'warning'"
      :closable="false"
      show-icon
      :title="`${result.year} 年：期望 ${result.expectedDays} 天 / 实际 ${result.actualDays} 天，${result.message}`"
    />
    <el-table v-if="missingDates.length" :data="missingDates" border stripe size="small" class="missing-table">
      <el-table-column prop="date" label="缺失日期" min-width="180" />
    </el-table>
  </div>
</template>

<script setup lang="ts">
import { computed, ref } from 'vue'
import { Search } from '@element-plus/icons-vue'
import { http } from '@/api/http'

const year = ref(new Date().getFullYear())
const checking = ref(false)
const result = ref<Record<string, any> | null>(null)

const missingDates = computed(() => {
  try {
    const parsed = JSON.parse(result.value?.missingDates ?? '[]')
    return Array.isArray(parsed) ? parsed.map((date) => ({ date: String(date) })) : []
  } catch {
    return []
  }
})

async function check() {
  checking.value = true
  try {
    result.value = (await http.post(`/api/v1/open-api/calendar/check?year=${year.value}`)) as Record<string, any>
  } finally {
    checking.value = false
  }
}
</script>

<style scoped>
.panel { padding-top: 4px; }
.toolbar { display: flex; gap: 8px; align-items: center; margin-bottom: 12px; }
.missing-table { margin-top: 12px; }
</style>
