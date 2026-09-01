# ZPWU-CODE

一款极低资源消耗、以 GitHub 为唯一存储与配置中心、支持自定义大模型 API Key 的云端智能体管理与交互系统。

## 当前简易框架

### 后端（Go）
- `cmd/server/main.go`：启动 HTTP 服务，提供 API 与静态文件
- `internal/config`：环境变量配置加载
- `internal/handlers`：基础 API 占位
  - `GET /api/health`
  - `POST /api/chat`（请求头需带 `X-API-Key`）
  - `POST /api/git/sync`
  - 注意：当前仅做 `X-API-Key` 非空校验，后续需在真实转发链路中完善安全策略

### 前端（PWA 静态页面）
- `web/index.html`：控制台页面
- `web/app.js`：健康检查、对话请求、同步触发
- `web/styles.css`：简易样式
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

## 后续建议

1. 在 `POST /api/chat` 中接入真实 LLM 流式转发。
2. 在 `POST /api/git/sync` 中接入 `git pull` 或 GitHub API 同步逻辑。
3. 增加 API Key 安全策略（加密传输、脱敏日志、短生命周期缓存）。
4. 增加 Systemd 日志读取与实时展示接口。
