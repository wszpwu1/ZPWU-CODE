# ZPWU-CODE

一款移动优先（PWA）的云端智能体管理与交互系统，支持自定义大模型 API、任务状态跟踪、以及 GitHub 仓库存储同步。

## 当前简易框架

### 后端（Go API）
- `cmd/server/main.go`：启动 HTTP 服务，提供 API 与静态文件
- `internal/config`：环境变量配置加载
- `internal/handlers`：MVP 能力实现
  - `GET /api/health`
  - `GET/POST /api/providers`：保存/读取自定义模型提供商配置
  - `POST /api/providers/active`：切换当前激活提供商
  - `POST /api/chat`：提交对话编程任务（异步）
  - `POST /api/git/sync`：提交 GitHub 文件同步任务（异步）
  - `GET /api/tasks` 与 `GET /api/tasks/{id}`：任务状态查询（queued/running/completed/failed）
  - `POST /api/exec/validate`：命令与路径风险校验（MVP 风控）

### 前端（PWA）
- `web/index.html`：手机端控制台
- `web/app.js`：提供商管理、对话任务提交、GitHub 同步、任务轮询
- `web/styles.css`：移动优先样式
- `web/manifest.webmanifest`：PWA 基础配置
- `web/sw.js`：基础离线缓存

## 快速启动

```bash
go run ./cmd/server
```

启动后访问：`http://localhost:8080`

## 环境变量

参考 `.env.example`：
- `APP_ADDR`
- `GITHUB_REPO_OWNER`
- `GITHUB_REPO_NAME`
- `GITHUB_REPO_BRANCH`
- `PROVIDER_STORE_PATH`
- `APP_ENCRYPTION_KEY`（必填，至少 16 位）
- `APP_ACCESS_TOKEN`（必填，接口需通过 `X-App-Token` 访问）

## API 使用说明（MVP）

### 1) 配置模型提供商
`POST /api/providers`

请求头需带 `X-App-Token: <APP_ACCESS_TOKEN>`。

```json
{
  "name": "OpenAI",
  "base_url": "https://api.openai.com",
  "model": "gpt-4.1-mini",
  "api_key": "sk-***",
  "headers": {
    "X-Custom": "value"
  },
  "active": true
}
```

### 2) 提交聊天任务
`POST /api/chat`

请求头需带 `X-App-Token: <APP_ACCESS_TOKEN>`。

```json
{
  "agent": "mobile-dev",
  "provider_id": "provider-xxx",
  "message": "帮我生成一个 HTTP handler"
}
```

响应为 `202`，返回 `task_id`，再通过 `GET /api/tasks/{id}` 轮询。

### 3) 同步内容到 GitHub
`POST /api/git/sync`（请求头需带 `X-App-Token` 与 `X-GitHub-Token`）

```json
{
  "owner": "wszpwu1",
  "repo": "ZPWU-CODE",
  "branch": "main",
  "file_path": "mobile-agent/output.txt",
  "content": "hello",
  "commit_message": "feat: add output"
}
```

## 安全说明（MVP）

- Provider API Key 在服务端 AES-GCM 加密后落盘（`PROVIDER_STORE_PATH`）。
- 未设置有效 `APP_ENCRYPTION_KEY`（<16 位）时，服务会禁用 provider 存储能力。
- 所有受保护接口要求 `X-App-Token`，用于最小访问控制。
- API Key 不在读取接口明文回显。
- GitHub Token 仅从请求头读取，不持久化。
- 提供 `POST /api/exec/validate` 用于命令白名单与敏感路径拦截。

## 后续建议

1. 增加流式返回（SSE/WebSocket）以实现实时 token 展示。
2. GitHub 同步扩展为“分支创建 + PR 自动创建 + 冲突引导修复”完整流程。
3. 落地真实沙箱执行环境（容器隔离、网络与文件系统策略）。
4. 增加限流、熔断、审计追踪与告警。
