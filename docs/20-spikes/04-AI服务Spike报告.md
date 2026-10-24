# Spike 04：AI 服务连通与脱敏 Spike 报告

- **WBS**：0-4 技术 Spike 之 AI 服务连通与脱敏 PoC
- **日期**：2026-10-14　**执行人**：BE　**状态**：✅ 通过（6/6；2026-10-15 行内 AI 网关真实端点复测通过）
- **代码**：`backend/cmd/spike-ai/`（脱敏验证 + httptest mock OpenAI 协议 + 无 key/不可达/401 降级）
- **原始输出**：`spike/04-ai-conn/output.txt`

## 1. 目的

验证 AI 辅助能力（FR-AI-01~04，WBS 4-3）前置技术条件：

1. **脱敏**（FR-AI-04）：提供给模型的数据样例不含生产凭据、内网 IP、手机号、证件号；
2. **协议**：OpenAI 兼容 `/chat/completions` 的请求构造、Bearer 鉴权、响应解析（mock 服务验证）；
3. **降级**（NFR §7.4）：未配置端点 / 端点不可达 / 401 鉴权失败时均返回可读错误，上层映射 50300 降级，不 panic、不挂起、不阻塞主流程。

## 2. 环境

| 项 | 值 |
| --- | --- |
| Go | 1.26.5（与主工程同栈，复用 `internal/ai` 与 `pkg/masking`） |
| mock | `net/http/httptest` 模拟 OpenAI 协议 |
| 真实 LLM | 行内 AI 网关 `https://aihub.test.dhzq.cn/v1`（OpenAI 兼容），key 打码 `sk-5Gve***eOWY`，模型 `dhzq-qwen3.8`（vLLM 后端，实际路由 `dhzq-qwen3.8-2`） |

## 3. 用例与结果

| # | 用例 | 预期 | 结果 |
| --- | --- | --- | --- |
| 1 | 脱敏：凭据/主机/手机/证件不出现在提示词 | 全部打码，泄露项为空 | PASS |
| 2 | 协议：OpenAI 兼容请求/鉴权/响应解析 | mock 收到 Bearer token，解析 content/usage | PASS |
| 3 | 降级：未配置端点 | 返回可读错误（上层 50300） | PASS |
| 4 | 降级：端点不可达 | 返回错误，不 panic/不挂起 | PASS |
| 5 | 降级：401 鉴权失败 | 返回含响应的可读错误 | PASS |
| 6 | 连通：真实 LLM 端点（行内 AI 网关） | 200 + 非空回复，标准字段解析 | PASS（model=`dhzq-qwen3.8-2`，usage=57/121，回复"正常"） |

## 4. 真实输出（output.txt 全文）

```text
Go spike 04 (AI) @ 2026-10-15 11:53:33

--- 脱敏-凭据/主机/手机/证件不出现在提示词 [PASS]
原始: 帮我生成采集流程: 数据源 mysql://scott:tiger@10.1.2.3:3306/coredb, 密码 password: hunter22, 联系人 13812345678, 证件 110101199003077758
脱敏后: 帮我生成采集流程: 数据源 <CONN>, 密码 password=****** 联系人 138****5678, 证件 1101**********7758
泄露项: []
--- 协议-OpenAI 兼容请求/鉴权/响应解析 [PASS]
err=<nil> content="done: print ok" model="mock-model" usage=10/5
--- 降级-未配置端点时返回错误(上层映射 50300, 不阻塞主流程) [PASS]
err=llm 请求失败: Post "/chat/completions": unsupported protocol scheme ""
--- 降级-端点不可达时返回错误(不 panic/不挂起) [PASS]
err=llm 请求失败: Post "http://127.0.0.1:1/chat/completions": dial tcp 127.0.0.1:1: connectex: No connection could be made because the target machine actively refused it.
--- 降级-401 鉴权失败返回可读错误 [PASS]
err=llm 返回 401: {"error":"invalid api key"}
--- 连通-真实 LLM 端点(行内 AI 网关 OpenAI 兼容协议) [PASS]
err=<nil> model="dhzq-qwen3.8-2" usage=57/121 content="\n\n正常"

===== 6 passed, 0 failed =====
```

## 5. 关键发现

- **网关协议与路径**：行内 AI 网关 OpenAI 兼容前缀为 `/v1`，完整端点 `https://aihub.test.dhzq.cn/v1/chat/completions`；平台 `AI_BASE_URL` 应配置为 `https://aihub.test.dhzq.cn/v1`（客户端拼接 `/chat/completions`，见 `internal/ai/llm.go`）；
- **权限时序**：首次调用 403（`package is disabled or you do not have invocation permission` / `token has no access to model`），网关侧开通 `dhzq-qwen3.8` 权限后复测 200 通过；对照实验：伪造 key 返回 401 Invalid token → 证明所配 key 本身有效、阻塞在套餐/模型授权层（网关控制台可见），排除凭据错误可能；
- **响应形态**：标准 `chat.completion` 结构；实际模型名 `dhzq-qwen3.8-2`（网关路由别名），`system_fingerprint=vllm-0.25.1-tp2-bc772341`；message 内含扩展 `reasoning` 字段（推理链）——生产客户端按标准字段 `choices[0].message.content` 解析，未知字段自动忽略，兼容无影响；usage 显示 completion 121 token 而可见回复仅 2 字，即推理 token 计入用量（成本评估时注意）；
- **/v1/models 列表按 key 权限过滤**：开通前列表仅 4 个模型（不含 dhzq-qwen3.8），开通后调用即放行——模型可用性以调用侧权限为准，上线前需与网关侧确认 `dhzq-qwen3.8` 为生产固定模型并纳入网关变更管理。

## 6. 风险与应对

| 风险 | 等级 | 应对 |
| --- | --- | --- |
| 模型可用性依赖网关侧套餐/授权状态 | 中 | ✅ 2026-10-15 已实测开通并调通；模型授权变更纳入网关变更管理，平台侧 50300 降级路径兜底（不阻塞主流程，符合 NFR §7.4） |
| 脱敏规则覆盖不全（新型凭据格式） | 中 | 脱敏规则版本化并纳入安全测试（M5，TC-SEC 专项）；AI 调用日志（输入输出摘要）可审计兜底 |
| 模型输出不可控（生成错误 SQL/脚本） | 中 | 流程强制"人工审核 + 预览试跑"（FR-AI-01/02），AI 内容不直接生效 |

## 7. 结论

**通过。** 脱敏、OpenAI 兼容协议、全部降级路径验证成功；2026-10-15 行内 AI 网关真实端点复测通过（`dhzq-qwen3.8` 调通，标准字段解析正常，未知扩展字段兼容）。`AI_BASE_URL=https://aihub.test.dhzq.cn/v1`、`AI_MODEL=dhzq-qwen3.8` 可直接写入 dev.env，供 M4 AI 功能（WBS 4-3）开发联调。
