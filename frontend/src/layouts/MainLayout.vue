<template>
  <el-container class="layout">
    <el-aside width="210px" class="aside">
      <div class="brand">
        <span class="brand-logo" :class="{ 'has-image': branding.icon }">
          <img v-if="branding.icon" :src="branding.icon" alt="" />
          <template v-else>{{ branding.initial }}</template>
        </span>
        <span class="brand-text">{{ branding.name }}</span>
      </div>
      <el-menu :default-active="route.path" router background-color="#001529" text-color="#a6adb4" active-text-color="#ffffff">
        <el-menu-item v-for="m in menus" :key="m.path" :index="m.path">
          <el-icon><component :is="m.icon" /></el-icon>
          <span>{{ m.title }}</span>
        </el-menu-item>
      </el-menu>
    </el-aside>
    <el-container>
      <el-header class="header">
        <div class="header-title">{{ (route.meta?.title as string) || '' }}</div>
        <el-dropdown @command="onCommand">
          <span class="user">
            <el-avatar :size="28">{{ avatarText }}</el-avatar>
            <span class="user-name">{{ auth.user?.realName || auth.user?.username || '' }}</span>
            <el-icon><ArrowDown /></el-icon>
          </span>
          <template #dropdown>
            <el-dropdown-menu>
              <el-dropdown-item command="logout">退出登录</el-dropdown-item>
            </el-dropdown-menu>
          </template>
        </el-dropdown>
      </el-header>
      <el-main class="main">
        <router-view />
      </el-main>
    </el-container>
  </el-container>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import { useRoute } from 'vue-router'
import { Odometer, OfficeBuilding, DataBoard, DataLine, Coin, SetUp, Grid, Bell, Message, Document, TrendCharts, Connection, Calendar, Setting, ArrowDown } from '@element-plus/icons-vue'
import { useAuthStore } from '@/stores/auth'
import { useBrandingStore } from '@/stores/branding'
import { ElMessageBox } from 'element-plus'

const route = useRoute()
const auth = useAuthStore()
const branding = useBrandingStore()

const menus = [
  { path: '/dashboard', title: '首页', icon: Odometer },
  { path: '/systems', title: '业务系统', icon: OfficeBuilding },
  { path: '/systems/view', title: '系统视图', icon: DataBoard },
  { path: '/metrics', title: '指标管理', icon: DataLine },
  { path: '/datasources', title: '数据源', icon: Coin },
  { path: '/flows', title: '采集管理', icon: SetUp },
  { path: '/data', title: '数据管理', icon: Grid },
  { path: '/alerts', title: '告警中心', icon: Bell },
  { path: '/notifications', title: '通知订阅', icon: Message },
  { path: '/reports', title: '报表中心', icon: Document },
  { path: '/forecast-risk', title: '预测风险', icon: TrendCharts },
  { path: '/open-api', title: '开放 API', icon: Connection },
  { path: '/tradingdays', title: '交易日', icon: Calendar },
  { path: '/admin', title: '系统管理', icon: Setting },
]

const avatarText = computed(() => (auth.user?.realName || auth.user?.username || '?').slice(0, 1).toUpperCase())

async function onCommand(cmd: string) {
  if (cmd === 'logout') {
    await ElMessageBox.confirm('确定退出登录？', '提示', { type: 'warning' })
    await auth.logout()
  }
}
</script>

<style scoped>
.layout { height: 100%; }
.aside { background: #001529; }
.brand {
  height: 56px; display: flex; align-items: center; gap: 8px;
  padding: 0 16px; color: #fff; font-size: 15px; font-weight: 600;
}
.brand-logo {
  width: 28px; height: 28px; border-radius: 6px; background: var(--cap-primary);
  display: inline-flex; align-items: center; justify-content: center; font-size: 14px;
  flex: 0 0 28px; overflow: hidden;
}
.brand-logo.has-image { background: #fff; }
.brand-logo img { width: 100%; height: 100%; object-fit: contain; display: block; }
.brand-text { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.aside :deep(.el-menu) { border-right: none; }
.header {
  background: #fff; display: flex; align-items: center; justify-content: space-between;
  border-bottom: 1px solid #ebeef5; height: 56px;
}
.header-title { font-size: 16px; font-weight: 600; }
.user { display: flex; align-items: center; gap: 8px; cursor: pointer; outline: none; }
.user-name { font-size: 13px; color: #303133; }
.main { padding: 16px; overflow: auto; }
</style>
