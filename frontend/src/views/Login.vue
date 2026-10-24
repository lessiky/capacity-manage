<template>
  <div class="login-wrap">
    <div class="login-card">
      <div class="login-title">
        <span class="brand-logo" :class="{ 'has-image': branding.icon }">
          <img v-if="branding.icon" :src="branding.icon" alt="" />
          <template v-else>{{ branding.initial }}</template>
        </span>
        {{ branding.name }}
      </div>
      <div class="login-sub">Capacity Management Platform</div>
      <el-form :model="form" @submit.prevent="onLogin">
        <el-form-item>
          <el-input v-model="form.username" placeholder="用户名" size="large" :prefix-icon="User" />
        </el-form-item>
        <el-form-item>
          <el-input v-model="form.password" type="password" placeholder="密码" size="large" show-password :prefix-icon="Lock" @keyup.enter="onLogin" />
        </el-form-item>
        <el-button type="primary" size="large" class="login-btn" :loading="loading" @click="onLogin">
          登 录
        </el-button>
      </el-form>
      <div class="login-hint">默认账号 admin / Admin@123（开发环境）</div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { reactive, ref } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { ElMessage } from 'element-plus'
import { User, Lock } from '@element-plus/icons-vue'
import { useAuthStore } from '@/stores/auth'
import { useBrandingStore } from '@/stores/branding'

const auth = useAuthStore()
const branding = useBrandingStore()
const route = useRoute()
const router = useRouter()
const loading = ref(false)
const form = reactive({ username: 'admin', password: '' })

async function onLogin() {
  if (!form.username || !form.password) {
    ElMessage.warning('请输入用户名和密码')
    return
  }
  loading.value = true
  try {
    await auth.login(form.username, form.password)
    const redirect = (route.query.redirect as string) || '/dashboard'
    router.push(redirect)
  } catch {
    // 错误提示已由 http 拦截器统一处理
  } finally {
    loading.value = false
  }
}
</script>

<style scoped>
.login-wrap {
  height: 100%; display: flex; align-items: center; justify-content: center;
  background: linear-gradient(135deg, #0b1f3a 0%, #123a6d 55%, #1f6feb 100%);
}
.login-card {
  width: 380px; background: #fff; border-radius: 12px; padding: 36px 32px 24px;
  box-shadow: 0 12px 40px rgba(0, 0, 0, 0.25);
}
.login-title { font-size: 20px; font-weight: 700; display: flex; align-items: center; gap: 10px; justify-content: center; }
.brand-logo {
  width: 30px; height: 30px; border-radius: 7px; background: var(--cap-primary); color: #fff;
  display: inline-flex; align-items: center; justify-content: center; font-size: 15px;
  flex: 0 0 30px; overflow: hidden;
}
.brand-logo.has-image { background: #fff; }
.brand-logo img { width: 100%; height: 100%; object-fit: contain; display: block; }
.login-sub { text-align: center; color: #909399; font-size: 12px; margin: 6px 0 22px; }
.login-btn { width: 100%; }
.login-hint { margin-top: 16px; text-align: center; color: #c0c4cc; font-size: 12px; }
</style>
