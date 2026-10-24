<template>
  <el-dialog :model-value="modelValue" title="AI 配置助手" width="560px" :close-on-click-modal="false"
    @update:model-value="(v: boolean) => emit('update:modelValue', v)">
    <div class="msg-list">
      <div v-if="!msgs.length" class="empty">
        描述你的采集/清洗配置问题或需求，例如：<br />
        "如何配置交易日非交易时间窗口采集数据库水位？" <br />
        "正则匹配不上日志行，样例：2026-10-15 17:00:01 TABLESPACE pct 87.5"
      </div>
      <div v-for="(m, i) in msgs" :key="i" class="msg" :class="m.role">
        <div class="bubble">{{ m.content }}</div>
        <div v-if="m.role === 'assistant' && m.model" class="model">{{ m.model }}</div>
      </div>
      <div v-if="sending" class="msg assistant">
        <div class="bubble thinking">思考中…</div>
      </div>
    </div>
    <div class="input-row">
      <el-input v-model="input" type="textarea" :rows="3" placeholder="输入问题，Enter 发送" @keydown.enter.exact.prevent="send" />
      <el-button type="primary" class="send-btn" :loading="sending" @click="send">发送</el-button>
    </div>
    <div class="foot-note">AI 回答仅供参考，配置请人工审核；输入输出已脱敏并记录调用审计</div>
  </el-dialog>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { http } from '@/api/http'

interface Msg {
  role: 'user' | 'assistant'
  content: string
  model?: string
}

const props = withDefaults(defineProps<{ modelValue: boolean; context?: string }>(), { context: '' })
const emit = defineEmits<{ (e: 'update:modelValue', v: boolean): void }>()

const msgs = ref<Msg[]>([])
const input = ref('')
const sending = ref(false)

async function send() {
  const text = input.value.trim()
  if (!text || sending.value) return
  input.value = ''
  msgs.value.push({ role: 'user', content: text })
  sending.value = true
  try {
    const d = (await http.post('/api/v1/ai/chat', { message: text, context: props.context })) as unknown as { reply: string; model: string }
    msgs.value.push({ role: 'assistant', content: d.reply, model: d.model })
  } catch (e) {
    msgs.value.push({ role: 'assistant', content: `请求失败：${e instanceof Error ? e.message : String(e)}` })
  } finally {
    sending.value = false
  }
}
</script>

<style scoped>
.msg-list { max-height: 420px; overflow-y: auto; background: #f7f8fa; border-radius: 6px; padding: 12px; margin-bottom: 12px; }
.empty { color: #909399; font-size: 12px; line-height: 1.8; }
.msg { display: flex; flex-direction: column; margin-bottom: 10px; }
.msg.user { align-items: flex-end; }
.msg.assistant { align-items: flex-start; }
.bubble {
  max-width: 85%; padding: 8px 12px; border-radius: 8px; font-size: 13px; line-height: 1.7;
  white-space: pre-wrap; word-break: break-word;
}
.msg.user .bubble { background: var(--cap-primary); color: #fff; }
.msg.assistant .bubble { background: #fff; border: 1px solid #ebeef5; }
.bubble.thinking { color: #909399; }
.model { color: #c0c4cc; font-size: 11px; margin-top: 2px; }
.input-row { display: flex; gap: 8px; align-items: flex-start; }
.input-row .el-input { flex: 1; }
.send-btn { height: 60px; }
.foot-note { color: #c0c4cc; font-size: 11px; margin-top: 8px; text-align: right; }
</style>
