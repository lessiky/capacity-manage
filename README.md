# 容量管理平台（Capacity Management Platform）

一期（MVP）目标：跑通"交易日感知的采集 → 数据落地 → 监控预警 → 报表分析"闭环，覆盖 2~3 个试点业务系统。

## 仓库结构

```
├── docs/                    # 项目文档
│   ├── 容量管理平台需求文档.md
│   ├── 容量管理平台开发计划.md
│   ├── 00-baseline/         # 需求基线（M0 签署）
│   ├── 10-design/           # 总体架构 / DB DDL / API 规范 / 告警状态机 / 接口契约
│   ├── 20-spikes/           # 4 项技术 Spike 报告
│   ├── 30-ui/               # UI 设计规范
│   └── 40-test/             # 测试计划
├── db/migrations/           # PostgreSQL DDL 与种子数据脚本
├── spike/                   # Phase 0 技术 Spike PoC（独立可运行）
├── backend/                 # Go（Gin）单体模块化后端
│   ├── cmd/capacity-server/ # 入口：server | migrate | seed
│   ├── internal/            # 配置/中间件/持久化/业务模块
│   └── worker/              # Python 沙箱 Worker（独立进程池）
├── frontend/                # Vue3 + TS + Vite + Element Plus 管理端
├── ui-mockup/               # M0 主页面/大盘/报表 UI 视觉稿（HTML）
├── deploy/                  # Docker Compose 四环境部署
└── .gitlab-ci.yml           # CI/CD 流水线
```

## 技术栈

| 类别 | 选型 |
| --- | --- |
| 后端 | Go 1.22+（Gin，单体模块化）+ robfig/cron 调度 |
| 数据 | PostgreSQL 14+（业务配置 + 日内/日数据月分区）、Redis 7、MinIO |
| 前端 | Vue 3 + TypeScript + Vite + Element Plus + ECharts 5 |
| 安全 | JWT + Casbin（RBAC + 数据权限）、AES-256-GCM 凭据加密、登录锁定 |
| Python 沙箱 | 独立 Python 3.11 Worker（子进程 + OS 级限制，超时/白名单） |
| 部署 | Docker + Docker Compose（dev/sit/uat/prod 四环境） |

## 快速开始（开发环境）

前置：Docker Desktop 已启动。

```bash
# 1. 启动基础设施与全部服务（dev 环境）
docker compose -f deploy/docker-compose.yml --env-file deploy/env/dev.env up -d

# 2. 或本地直接运行后端
cd backend
go run ./cmd/capacity-server migrate   # 建表
go run ./cmd/capacity-server seed      # 初始化管理员/角色/交易日日历
go run ./cmd/capacity-server server    # 启动服务 :8080

# 3. 前端
cd frontend
npm install
npm run dev                             # :5173，默认账号 admin / Admin@123
```

默认管理员账号：`admin / Admin@123`（首次登录强制改密，生产环境禁止使用）。

## 环境与流水线

- 环境：`dev → sit → uat → prod`，Compose 配置见 `deploy/`；
- CI/CD：GitLab CI 四阶段 `lint → test → build → deploy`，dev 环境自动部署，sit/uat/prod 手工触发；
- 分支策略：`main`（生产）/ `develop`（集成）/ `feature/*`，评审 100% 通过方可合并。

## 文档索引

- 需求基线：`docs/00-baseline/需求基线-V1.0.md`
- 总体架构：`docs/10-design/01-总体架构设计.md`
- 数据库设计：`docs/10-design/02-数据库设计.md`
- API 规范：`docs/10-design/03-API规范.md`
- 告警状态机：`docs/10-design/04-告警状态机与判定规则.md`
- 接口契约：`docs/10-design/05-接口契约.md`
- Spike 报告：`docs/20-spikes/`
- 测试计划：`docs/40-test/测试计划.md`