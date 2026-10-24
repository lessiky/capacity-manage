# 环境与 CI/CD 就绪验证记录（M0 交付物，WBS 0-3）

- **日期**：2026-10-15　**执行人**：BE　**状态**：✅ dev 全栈冒烟通过
- **范围**：四环境（dev/sit/uat/prod）配置与部署编排、CI/CD 流水线、dev 环境全栈验证
- **证据**：本文件 + `docs/30-devops/evidence-smoke.txt`（冒烟原始输出）

## 1. 交付物清单

| 项 | 位置 | 说明 |
| --- | --- | --- |
| 后端镜像 | `backend/Dockerfile` | 多阶段构建：golang:1.26-alpine（GOPROXY=goproxy.cn，GOMAXPROCS=2 防 OOM）→ python:3.11-slim（同镜像承载 Go 服务 + Python Worker，MVP 形态）；HEALTHCHECK 走 :8080/healthz |
| 前端镜像 | `frontend/Dockerfile` | node:22-alpine（npm 走 registry.npmmirror.com）→ nginx:1.27-alpine；/api 反代 backend:8080 |
| 部署编排 | `deploy/docker-compose.yml` | postgres:16-alpine / redis:7-alpine / backend-migrate（migrate+seed，restart:no）/ backend / worker / frontend；minio 已移除（2026-10-15 用户确认暂不需要；国内镜像源亦均拒绝该镜像），文件统一走本地磁盘 FILE_ROOT |
| 四环境配置 | `deploy/env/{dev,sit,uat,prod}.env` | APP_ENV 切换；compose 通过 `env/${APP_ENV:-dev}.env` 注入 |
| CI/CD | `.gitlab-ci.yml` | lint → test → build → deploy；develop 分支自动部署 dev，main 手动 sit，tag 手动 uat/prod |
| 构建排除 | `.dockerignore` | 排除 node_modules/.git/docs 等，避免 context 传输 200MB+（曾导致构建 40 分钟无进展） |

## 2. dev 全栈冒烟（2026-10-15 10:19 最终轮）

| # | 检查项 | 结果 |
| --- | --- | --- |
| 1 | `GET :8080/healthz` | 200 `{code:0, data:{status:"ok"}}` |
| 2 | `GET :8080/readyz` | 200 `{code:0, data:{status:"ready"}}`（DB+Redis 就绪） |
| 3 | `POST :8080/api/v1/auth/login`（admin/Admin@123） | code=0，accessToken 311 字符，expiresIn=7200 |
| 4 | `GET /api/v1/dashboard/overview`（Bearer） | code=0，空库返回零值聚合 |
| 5 | `GET /api/v1/systems`（Bearer） | code=0，分页结构正常 |
| 6 | `GET /api/v1/metrics`（Bearer） | code=0 |
| 7 | `GET /api/v1/trading-days?market=SH`（Bearer） | code=0，seed 的 2026 交易日日历正确（含周末/节假日判定） |
| 8 | 无 token 访问受保护接口 | 401 `{code:40100, message:"未认证"}`（Auth 中间件生效） |
| 9 | `GET :9100/v1/health`（Worker） | 200 `{ok:true, pool:{free:4}}` |
| 10 | `GET :80/`（nginx → 前端） | 200，index.html 含 `id="app"` |

容器状态：postgres/redis/backend/frontend 均 healthy；backend-migrate 以 exit 0 完成（migrate + seed 幂等）。

## 3. 验证过程中发现并修复的缺陷（M0 拦截，未流入 M1）

| # | 缺陷 | 影响 | 修复 |
| --- | --- | --- | --- |
| 1 | DDL 使用表达式表级 UNIQUE 约束 `UNIQUE (COALESCE(...))` | PostgreSQL 语法错误（表级 UNIQUE 只接受列列表），迁移失败 | 改 `CREATE UNIQUE INDEX`（表达式双括号），`db/migrations/0001_init.sql`；全量 DDL 已用 psql 干跑验证通过 |
| 2 | 迁移器把整份 DDL 当单条 SQL 执行 | pgx 扩展协议不支持多语句 | `persistence.Migrate` 重写：语句切分器（识别 $$ 体/引号/注释）+ GORM 事务顺序执行，新增单测 `migrate_split_test.go` |
| 3 | GORM 默认复数表名（sys_users）与 DDL 单数表名（sys_user）不一致 | 全部 SQL 报表不存在 | `persistence.Open` 增加 `NamingStrategy{SingularTable: true}` |
| 4 | seed 顺序错误：先建 admin（角色授权 INSERT...SELECT 依赖角色行）后建角色 | admin 无角色，全部接口 403 | `Seed` 调整为先 seedRolesAndPerms 后 seedAdmin |
| 5 | Casbin 策略加载 SQL 缺列别名，Scan 到结构体得空串 | 全部接口 403（策略为空） | `middleware/casbin.go` 两条 Raw SQL 补 `AS role_code / AS perm_code`；live 复测 82 条策略 + admin 全权限通过 |
| 6 | `/api/v1` 路由组未挂 Auth 中间件 | 权限体系整体失效（403 而非 401，安全隐患） | `registerRoutes` 在 /auth 组之后挂 `Auth + SlideSession`；login 等 3 handlers、受保护路由 6 handlers（gin 启动日志验证） |
| 7 | Worker 绑定 127.0.0.1:9100 | compose 独立容器时 backend 跨容器不可达 | `run_worker.py` 默认绑定 0.0.0.0（`WORKER_HOST` 可配收紧） |
| 8 | `DailySummaryLog.ErrMsg` 映射 `err_msg`，DDL 列为 `error_msg` | 每日汇总日志写入失败 | model 增加 `gorm:"column:error_msg"` 标签 |
| 9 | Auth 未挂载 jwtClaims，SlideSession 空转 | 会话 30 分钟滑动超时（NFR）不生效 | `Auth` 增加 `c.Set("jwtClaims", claims)` |

> 以上 9 项均为 dev 全栈验证首次真实执行时暴露（此前仅单测 + 内存 SQLite 覆盖），印证"M0 全栈冒烟"对设计冻结基线的拦截价值。DDL 修改（#1）需在 M0 设计评审时同步更新 `docs/10-design/02-数据库设计.md`。

## 4. 环境构建经验（沉淀）

- **Docker Hub 被墙**：`C:\Users\Administrator\.docker\daemon.json` 配置 registry-mirrors（docker.1ms.run / docker.m.daocloud.io / hub.rat.dev）；`docker/dockerfile:1`（BuildKit 前端）与各基础镜像需**显式从可用镜像源拉取并 retag 到 docker.io 名**，BuildKit 命中本地镜像后不再走源。
- **minio 已移除（2026-10-15 用户确认暂不需要）**：此前国内镜像源均拒绝 minio 镜像，经确认后从 compose 与 4 份 env 中删除 minio 服务、`miniodata` 卷及 `MINIO_*` 配置；文件存储统一本地磁盘 FILE_ROOT，未来需要对象存储再按 MinIO 适配层（`store.Store` 接口）引入。
- **16GB 宿主机内存紧张**：容器内 `go build` 需 `GOMAXPROCS=2`（曾 OOM）；本地 `go build` 建议 `-p 1`；compose 双镜像**串行构建**（backend → frontend）避免并行峰值。
- **BuildKit context**：必须维护 `.dockerignore`（node_modules 172MB 曾使 context 传输 >300MB）。

## 5. sit/uat/prod 就绪说明

- 编排与镜像同 dev 复用（`APP_ENV` 切换 env 文件）；`docker compose config --quiet` 对 dev/sit/uat/prod 四份 env 均校验通过。
- sit/uat/prod 的实际网络可达性（DB/AI 网关）与部署执行属 M1 环境建设事项（计划 §9-4）；本记录仅证明**部署工程链路（镜像/编排/流水线/配置）就绪**。
