# capacity-worker (Python 沙箱 Worker)

独立进程池, 供 Go 侧经本地 HTTP 调用（127.0.0.1:9100）。详见 `docs/10-design/05-接口契约.md` §1。

## 运行

```bash
cd backend/worker
python run_worker.py          # 默认 :9100
# 环境变量: WORKER_PORT / WORKER_POOL_SIZE / WORKER_TIMEOUT_MS / WORKER_WORK_ROOT
```

## 安全

- 子进程 `python -I -E sandbox.py <script>`：隔离模式 + 白名单 meta_path + 受限 os + rlimit；
- 网络隔离依赖部署层（Linux 生产：Docker `network: none` 或防火墙白名单；二期 gVisor）；
- 超时 SIGKILL 进程组；入参/出参/堆栈/耗时全量记录。

## 脚本约定

- 入参：环境变量 `CAP_PARAMS`（JSON 字符串，含 biz_date/prev_trading_day/system_code/hour 等）；
- 出参：stdout **最后一行** JSON（collect: 数组 `[{metric,value,unit}]`；clean: 同构）；
- 仅允许白名单模块；仅工作目录可写。