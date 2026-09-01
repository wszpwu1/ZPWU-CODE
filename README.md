# ZPWU 云智能体

> 极低资源消耗、**以 GitHub 为唯一存储**、支持自定义大模型 API 的云端智能体管理与交互系统。  
> 手机网页即用，1 核 1G Linux 服务器即可运行，服务器不存储任何数据。

---

## ✨ 核心特性

| 特性 | 说明 |
|------|------|
| 🚫 零磁盘存储 | 服务器完全无状态，不写入任何文件，随时重启不丢数据 |
| 🐙 GitHub 登录 & 存储 | OAuth 登录即绑定仓库，所有文件读写直接走 GitHub API |
| 🤖 完整 Agent Loop | AI 可自动调用工具：列目录、读文件、写文件、搜索，多轮推理完成复杂编码任务 |
| 🔑 双 AI 引擎 | 原生支持 **OpenAI 兼容**（DeepSeek、Qwen、Ollama…）和 **Claude（Anthropic）** 两种协议 |
| 📡 SSE 实时流式输出 | Agent 思考过程、工具调用进度实时推送到浏览器 |
| 📱 移动优先 PWA | 底部 Tab 导航，手机浏览器即用，支持添加到主屏幕 |
| 🪶 极轻量 | Go 单进程，空载 ~10MB 内存，1 核 1G 服务器完全够用 |
| 🔒 隐私安全 | API Key 仅存浏览器 localStorage，不经服务器持久化；GitHub Token 用完即丢 |

---

## 🖥️ 界面功能

### 💬 对话面板
- 普通模式：单/多轮对话，支持文件内容注入上下文
- **🤖 Agent 模式**：AI 自动调用 4 个工具完成任务，实时显示推理过程和工具调用卡片

### 📁 文件面板
- 从已登录 GitHub 账号的仓库列表中选择目标仓库
- 浏览目录树，在线编辑文件
- 一键提交修改到 GitHub（自动处理文件 SHA 更新）
- 「→AI」按钮：将当前文件内容注入对话上下文

### 🔑 API 管理面板
- 本地管理多个 AI 提供商，随时切换激活
- 支持自定义 Base URL、Headers
- API Key 仅存浏览器，不离开设备

### ⚙️ 设置面板
- GitHub 账号信息 & 退出登录
- 可选服务器访问令牌（APP_ACCESS_TOKEN）

---

## 🤖 Agent 工具说明

Agent 模式下 AI 可自动调用以下 4 个工具操作你的 GitHub 仓库：

| 工具 | 功能 |
|------|------|
| `list_dir` | 列出仓库目录内容，探索项目结构 |
| `read_file` | 读取文件内容（最大 500KB） |
| `write_file` | 写入/更新文件并自动 commit（含安全路径黑名单） |
| `search_files` | 通过 GitHub 代码搜索查找文件 |

> **安全限制**：`write_file` 禁止写入 `.github/workflows`、`.github/actions`、`.ssh` 等敏感路径。

---

## 🚀 部署指南

### 前置步骤：创建 GitHub OAuth App

1. 访问 [github.com/settings/developers](https://github.com/settings/developers) → **New OAuth App**
2. 填写：
   - **Homepage URL**：`https://your-domain.com`
   - **Authorization callback URL**：`https://your-domain.com/api/auth/github/callback`
3. 保存后记录 **Client ID** 和 **Client Secret**

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

---

### 方式二：Docker Compose

```bash
git clone https://github.com/wszpwu1/ZPWU-CODE.git
cd ZPWU-CODE

# 创建环境变量文件（不要提交到 git）
cat > .env <<EOF
GITHUB_CLIENT_ID=你的ClientID
GITHUB_CLIENT_SECRET=你的ClientSecret
APP_ACCESS_TOKEN=
EOF

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
docker run -d --name zpwu --restart unless-stopped -p 8080:8080 \
  -e GITHUB_CLIENT_ID=xxx -e GITHUB_CLIENT_SECRET=xxx zpwu-code
```

---

### 方式四：直接编译运行（需 Go 1.23+）

```bash
git clone https://github.com/wszpwu1/ZPWU-CODE.git
cd ZPWU-CODE

export GITHUB_CLIENT_ID=你的ClientID
export GITHUB_CLIENT_SECRET=你的ClientSecret
export APP_ADDR=:8080

# 直接运行
go run ./cmd/server

# 或编译为单文件二进制
go build -ldflags="-s -w" -o zpwu ./cmd/server && ./zpwu
```

访问 `http://localhost:8080` 开始使用。

---

## 🔧 环境变量

| 变量 | 必填 | 默认值 | 说明 |
|------|------|--------|------|
| `GITHUB_CLIENT_ID` | ✅ | — | GitHub OAuth App Client ID |
| `GITHUB_CLIENT_SECRET` | ✅ | — | GitHub OAuth App Client Secret |
| `APP_ADDR` | ❌ | `:8080` | 服务监听地址 |
| `APP_ACCESS_TOKEN` | ❌ | 空（不启用）| 服务器级别访问令牌，设置后前端需填写才能使用 |

---

## 📲 使用流程

```
1. 打开网页 → 点击「使用 GitHub 登录」
2. 授权后自动跳回，头像出现即登录成功
3. 前往「🔑 API」面板 → 添加 AI 提供商（填名称、类型、模型、API Key）
4. 前往「📁 文件」面板 → 选择仓库 → 点击↺加载文件树
5. （可选）打开文件 → 点「→AI」将文件内容注入对话上下文
6. 前往「💬 对话」面板 → 开启「🤖 Agent 模式」→ 输入任务，AI 自动完成
```

---

## 🤖 支持的 AI 提供商

| 类型 | 代表服务 | Base URL 示例 |
|------|----------|---------------|
| OpenAI 兼容 | GPT-4o, GPT-4.1 | `https://api.openai.com` |
| OpenAI 兼容 | DeepSeek | `https://api.deepseek.com` |
| OpenAI 兼容 | 通义千问 | `https://dashscope.aliyuncs.com/compatible-mode` |
| OpenAI 兼容 | Ollama（本地） | `http://localhost:11434` |
| OpenAI 兼容 | 任意自建网关 | 填写你的 Base URL |
| Claude 原生 | Claude 3.5/3.7 Sonnet | 留空（自动用官方地址）|

---

## 🔐 安全说明

- **GitHub Token**：OAuth 获取后仅存于浏览器 `localStorage`，每次请求由前端携带，服务器转发后不保留
- **API Key**：同样仅存 `localStorage`，随请求传至服务器转发 AI，服务器不落盘
- **APP_ACCESS_TOKEN**（可选）：为公网部署提供额外的接口访问控制
- **Agent 写文件黑名单**：阻止 AI 写入 `.github/workflows` 等 CI/CD 配置，防止提示注入攻击
- **服务器无状态**：重启不丢失任何数据（数据在 GitHub 和浏览器中）

---

## 🏗️ 项目结构

```
ZPWU-CODE/
├── cmd/server/main.go              # 服务入口（HTTP + 静态文件）
├── internal/
│   ├── config/config.go            # 环境变量配置
│   ├── handlers/handlers.go        # 所有 API 路由（无状态）
│   └── agent/
│       ├── tools.go                # 4 个 GitHub 工具定义与执行
│       └── loop.go                 # Agent Loop 引擎（OpenAI + Claude）
├── web/
│   ├── index.html                  # 移动端 PWA 主页（4 Tab）
│   ├── app.js                      # 前端逻辑（Auth、Agent SSE、文件操作）
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
| `POST` | `/api/agent/run` | App Token + GH Token | **Agent Loop SSE**（流式工具调用）|
| `POST` | `/api/git/sync` | App Token + GH Token | 写入文件到 GitHub |
| `GET` | `/api/git/files` | App Token + GH Token | 列出 GitHub 仓库目录 |
| `GET` | `/api/git/file` | App Token + GH Token | 读取 GitHub 仓库文件 |
| `GET` | `/api/tasks` | App Token | 查询任务列表 |
| `GET` | `/api/tasks/{id}` | App Token | 查询单个任务状态 |

> `GH Token` = 请求头 `X-GitHub-Token`  
> `App Token` = 请求头 `X-App-Token`（仅在设置了 `APP_ACCESS_TOKEN` 时需要）
