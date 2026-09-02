# ZPWU Agent

> 以 GitHub 为唯一存储、支持自定义大模型 API 的云端 AI 编程助手。  
> 手机浏览器即用，1 核 1G Linux 服务器即可运行，服务器不持久化任何数据。

---

## ✨ 核心特性

| 特性 | 说明 |
|------|------|
| 🚫 零磁盘存储 | 服务器完全无状态，不写入任何文件，随时重启不丢数据 |
| 🐙 GitHub 登录 & 存储 | OAuth 登录即绑定仓库，所有文件读写直接走 GitHub API |
| 🤖 Agent 模式 + 授权确认 | AI 调用 `write_file` 前必须经用户逐步确认，任何写入操作都可拒绝 |
| 📝 草稿箱 | 编辑完先存服务器草稿，查看 diff 后再决定推送或丢弃，永不意外覆盖 |
| 📊 Inline Diff | 草稿箱展示修改前 vs 修改后的逐行对比，新增绿色 / 删除红色 |
| 🔑 双 AI 引擎 | 原生支持 OpenAI 兼容（DeepSeek、Qwen、Ollama…）和 Claude（Anthropic）|
| 📡 SSE 实时流式输出 | Agent 思考过程、工具调用进度实时推送到浏览器 |
| 📱 移动优先 PWA | 底部 Tab 导航，支持添加到手机主屏幕 |
| 🪶 极轻量 | Go 单进程，空载 ~10MB 内存 |
| 🔒 隐私安全 | API Key 仅存浏览器 localStorage，不经服务器持久化 |

---

## 🖥️ 界面功能

### 💬 对话（工作区）

- **普通模式**：多轮对话，支持将当前编辑文件内容注入上下文
- **🤖 Agent 模式**：AI 自动调用工具完成任务，实时显示推理过程和工具卡片
  - `list_dir` / `read_file` / `search_files` 直接执行
  - **`write_file` 必须用户点击「✔ 允许写入」才会提交 GitHub**，可随时点「✕ 拒绝」

### 📁 文件

- 从 GitHub 账号仓库列表中选择目标仓库和分支
- 浏览目录树，点击文件在线编辑
- 编辑完点「**存草稿**」→ 保存到服务器暂存区，**不会直接改动 GitHub**
- 「→AI」按钮：将当前文件内容注入对话上下文

### 📝 草稿箱

- 列出所有暂存草稿，显示仓库、文件路径、更新时间
- 每条草稿展示 **Inline Diff**（修改前 vs 修改后，折叠/展开）
- 可修改提交信息后点「**✔ 授权推送 GitHub**」，弹窗二次确认后提交
- 点「**✕ 拒绝此草稿**」直接删除，不改动 GitHub
- 「🗑 清空全部」一键清空所有草稿

### 🔑 API 管理

- 本地管理多个 AI 提供商，随时切换激活
- 支持自定义 Base URL、模型、Headers
- API Key 仅存浏览器，不离开设备

### ⚙️ 设置

- GitHub 账号信息 & 退出登录
- 可选服务器访问令牌（APP_ACCESS_TOKEN）
- 服务器健康检查

---

## 🤖 Agent 工具说明

| 工具 | 授权方式 | 功能 |
|------|----------|------|
| `list_dir` | 自动执行 | 列出仓库目录内容 |
| `read_file` | 自动执行 | 读取文件内容（最大 500KB）|
| `search_files` | 自动执行 | GitHub 代码搜索查找文件 |
| `write_file` | **需用户点击允许** | 写入/更新文件并 commit |

> **安全限制**：`write_file` 禁止写入 `.github/workflows`、`.github/actions`、`.ssh` 等敏感路径，防止 AI 提示注入攻击篡改 CI/CD。

---

## 🚀 部署指南

### 第一步：创建 GitHub OAuth App

1. 访问 [github.com/settings/developers](https://github.com/settings/developers) → **New OAuth App**
2. 填写：
   - **Homepage URL**：`https://your-domain.com`
   - **Authorization callback URL**：`https://your-domain.com/api/auth/github/callback`
3. 创建后记录 **Client ID** 和生成的 **Client Secret**

---

### 方式一：Docker 一键部署（推荐）

```bash
docker run -d \
  --name zpwu \
  --restart unless-stopped \
  -p 8080:8080 \
  -e GITHUB_CLIENT_ID=你的ClientID \
  -e GITHUB_CLIENT_SECRET=你的ClientSecret \
  -e APP_ACCESS_TOKEN=可选的访问令牌 \
  ghcr.io/wszpwu1/zpwu-code:latest
```

访问 `http://your-server:8080`

---

### 方式二：Docker Compose

```bash
git clone https://github.com/wszpwu1/ZPWU-CODE.git
cd ZPWU-CODE

# 复制并填写环境变量（不要提交 .env 到 git）
cp .env.example .env
# 编辑 .env 填入 GITHUB_CLIENT_ID 和 GITHUB_CLIENT_SECRET

# 启动
docker compose up -d

# 查看日志
docker compose logs -f
```

---

### 方式三：从源码构建 Docker 镜像

```bash
git clone https://github.com/wszpwu1/ZPWU-CODE.git
cd ZPWU-CODE
docker build -t zpwu-code .
docker run -d \
  --name zpwu \
  --restart unless-stopped \
  -p 8080:8080 \
  -e GITHUB_CLIENT_ID=你的ClientID \
  -e GITHUB_CLIENT_SECRET=你的ClientSecret \
  zpwu-code
```

---

### 方式四：直接编译运行（需 Go 1.23+）

```bash
git clone https://github.com/wszpwu1/ZPWU-CODE.git
cd ZPWU-CODE

# 设置环境变量
export GITHUB_CLIENT_ID=你的ClientID
export GITHUB_CLIENT_SECRET=你的ClientSecret
export APP_ADDR=:8080

# 直接运行
go run ./cmd/server

# 或编译为单文件二进制（可上传服务器直接运行）
go build -ldflags="-s -w" -o zpwu ./cmd/server && ./zpwu
```

访问 `http://localhost:8080`

---

## 🔧 环境变量

| 变量 | 必填 | 默认值 | 说明 |
|------|------|--------|------|
| `GITHUB_CLIENT_ID` | ✅ | — | GitHub OAuth App Client ID |
| `GITHUB_CLIENT_SECRET` | ✅ | — | GitHub OAuth App Client Secret |
| `APP_ADDR` | ❌ | `:8080` | 服务监听地址，如 `0.0.0.0:80` |
| `APP_ACCESS_TOKEN` | ❌ | 空（不启用）| 服务器级别接口访问令牌，设置后前端「设置」页面需填写才可使用 |

---

## 📲 使用流程

```
1. 打开网页 → 点击「使用 GitHub 登录」
2. 授权后自动跳回，右上角头像出现即登录成功

── 配置 AI ──
3. 前往「🔑 API」面板 → 添加提供商（填名称、类型、Base URL、模型、API Key）→ 激活

── 编辑文件 ──
4. 前往「📁 文件」面板 → 选仓库 → 点 ↺ 加载目录树
5. 点击文件在线编辑 → 点「存草稿」暂存到服务器
6. 前往「📝 草稿箱」→ 查看 Diff → 确认无误点「✔ 授权推送 GitHub」

── Agent 自动编码 ──
7. 前往「💬 对话」→ 开启「🤖 Agent 模式」
8. 输入任务（如：「帮我给 main.go 添加错误处理」）
9. AI 会自动列目录、读文件，遇到写操作时弹出授权卡片
10. 点「✔ 允许写入」确认 → AI 完成 commit；或点「✕ 拒绝」跳过
```

---

## 🤖 支持的 AI 提供商

| 类型 | 代表服务 | Base URL 填写 |
|------|----------|---------------|
| OpenAI 兼容 | GPT-4o、GPT-4.1 | `https://api.openai.com` |
| OpenAI 兼容 | DeepSeek | `https://api.deepseek.com` |
| OpenAI 兼容 | 通义千问 | `https://dashscope.aliyuncs.com/compatible-mode` |
| OpenAI 兼容 | Ollama（本地）| `http://localhost:11434` |
| OpenAI 兼容 | 任意自建网关 | 填写你的 Base URL |
| Claude 原生 | Claude 3.5/3.7 Sonnet | 留空（自动使用官方地址）|

---

## 🔐 安全说明

- **GitHub Token**：OAuth 获取后仅存于浏览器 `localStorage`，每次请求由前端携带，服务器转发后不保留
- **API Key**：同样仅存 `localStorage`，随请求传至服务器转发 AI API，服务器不落盘
- **APP_ACCESS_TOKEN**（可选）：为公网部署提供额外的接口访问控制
- **Agent 写文件黑名单**：阻止 AI 写入 `.github/workflows` 等 CI/CD 配置
- **写操作二次确认**：Agent 模式每次 `write_file` 必须用户手动授权，草稿箱推送前展示完整 Diff 并弹窗确认
- **服务器无状态**：重启不丢失任何数据（数据在 GitHub 仓库和用户浏览器中）

---

## 🏗️ 项目结构

```
ZPWU-CODE/
├── cmd/server/main.go              # 服务入口（HTTP + 静态文件）
├── internal/
│   ├── config/config.go            # 环境变量配置
│   ├── handlers/handlers.go        # 所有 API 路由（含草稿、授权）
│   └── agent/
│       ├── tools.go                # 4 个 GitHub 工具定义与执行
│       └── loop.go                 # Agent Loop（OpenAI + Claude，含授权拦截）
├── web/
│   ├── index.html                  # PWA 主页（5 Tab）
│   ├── app.js                      # 前端逻辑（Auth、Agent SSE、草稿箱、Diff）
│   ├── styles.css                  # 移动优先 Dark 样式
│   ├── sw.js                       # Service Worker
│   └── manifest.webmanifest        # PWA 配置
├── Dockerfile                      # 多阶段构建（scratch 镜像 ~10MB）
├── docker-compose.yml              # Compose 一键部署
├── .env.example                    # 环境变量示例
└── go.mod                          # Go 1.23 模块
```

---

## 📡 API 参考

| 方法 | 路径 | 鉴权 | 说明 |
|------|------|------|------|
| `GET` | `/api/health` | 无 | 服务健康检查 |
| `GET` | `/api/auth/github` | 无 | 发起 GitHub OAuth 授权 |
| `GET` | `/api/auth/github/callback` | 无 | OAuth 回调（自动处理）|
| `GET` | `/api/auth/user` | GH Token | 获取当前 GitHub 用户信息 |
| `GET` | `/api/auth/repos` | GH Token | 获取用户仓库列表 |
| `POST` | `/api/chat` | App Token | 提交单次 AI 对话任务（异步轮询）|
| `POST` | `/api/agent/run` | App Token + GH Token | Agent Loop SSE（实时工具调用）|
| `POST` | `/api/agent/approve` | App Token | 提交工具调用授权决定 |
| `POST` | `/api/git/sync` | App Token + GH Token | 直接写入文件到 GitHub |
| `GET` | `/api/git/files` | App Token + GH Token | 列出 GitHub 仓库目录 |
| `GET` | `/api/git/file` | App Token + GH Token | 读取 GitHub 仓库文件 |
| `GET` | `/api/tasks` | App Token | 查询任务列表 |
| `GET` | `/api/tasks/{id}` | App Token | 查询单个任务状态 |
| `POST` | `/api/drafts` | App Token + GH Token | 保存草稿到服务器暂存区 |
| `GET` | `/api/drafts` | App Token + GH Token | 列出当前用户的草稿 |
| `DELETE` | `/api/drafts?id=xxx` | App Token + GH Token | 删除单个草稿 |
| `DELETE` | `/api/drafts?all=1` | App Token + GH Token | 清空所有草稿 |
| `POST` | `/api/drafts/push` | App Token + GH Token | 将草稿推送到 GitHub |

> `GH Token` = 请求头 `X-GitHub-Token`  
> `App Token` = 请求头 `X-App-Token`（仅在设置了 `APP_ACCESS_TOKEN` 时需要）
