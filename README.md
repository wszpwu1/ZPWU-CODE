# ZPWU-CODE

> 自托管的移动端 AI 编程智能体：浏览器用 GitHub OAuth 登录，AI 通过 GitHub Contents API 读写你的仓库，
> 所有写入先进「草稿箱」，由你看过 diff 再亲手推送。
> Go 单二进制 + 纯静态前端，**零第三方依赖**，1 核 1G 机器即可运行。

这份 README 描述的是**当前代码的真实行为**，不是设想中的行为。凡是尚未实现的东西，
都集中在文末的「🚧 已知限制与尚未实现」一节里，不会混进部署步骤中。

---

## 30 秒了解

| 问题 | 实际答案 |
|------|----------|
| 数据存在哪？ | 只存在 GitHub。服务器进程默认不写磁盘；**可选** `DRAFTS_FILE` 让草稿箱跨重启保留 |
| 凭据存在哪？ | GitHub OAuth token 与模型 API Key 只存浏览器 `localStorage`，随请求头携带，服务器不入库 |
| AI 能直接改我的仓库吗？ | 不能。`write_file` 被强制路由到草稿箱，必须你在「草稿箱」点推送才会产生 commit |
| 支持哪些模型？ | 任意 OpenAI 兼容端点（GPT / DeepSeek / 通义 / Ollama / vLLM / 自建网关）与 Anthropic Claude 原生 API |
| 普通对话是流式吗？ | **不是**。`/api/chat` 是异步任务，前端轮询 `/api/tasks/{id}`。只有 Agent 模式走 SSE |
| 体量 | Go 约 3.1k 行（含测试）/ 9 个文件，前端 `app.js` 约 830 行，无框架、无构建步骤 |

---

## 架构与数据流

```
浏览器 (PWA, 无框架纯 JS)
   │  X-GitHub-Token: <OAuth token>     ← 只存在 localStorage
   │  X-App-Token: <APP_ACCESS_TOKEN>   ← 仅当服务端设置了该变量
   ▼
Go 单进程 (net/http + http.FileServer)
   ├── /api/auth/*     → GitHub OAuth 授权码流程（state 防 CSRF）
   ├── /api/git/*      → 转发 GitHub Contents API（读目录 / 读文件 / 写文件）
   ├── /api/chat       → 转发 LLM，返回 task_id，前端轮询结果
   ├── /api/agent/run  → Agent Loop，SSE 推送思考与工具卡片
   ├── /api/drafts*    → 草稿箱（内存 map，可选落盘 JSON）
   └── /               → 静态文件 web/
   ▼
外部：api.github.com、OpenAI 兼容端点、api.anthropic.com
```

服务端只有三份**进程内**状态：任务表（上限 100 条）、草稿箱、GitHub 登录名缓存（5 分钟 TTL）。
没有数据库，没有 Redis，没有消息队列。重启即清空——这正是设置 `DRAFTS_FILE` 的动机。

---

## 界面功能（5 个底部 Tab）

### 💬 对话
- 多轮对话，`chatHistory` 上限 60 条，超出自动裁尾。
- **System prompt** 可选填。
- **上下文注入**：在「文件」Tab 点 `→AI`，把当前文件内容随本轮消息发给模型（单次，可手动 `✕` 清除）。
- 未勾选 Agent 模式时，走 `/api/chat` 异步任务 + 轮询，界面显示 `任务 task-xxx 运行中…`。
- 勾选 **🤖 Agent 模式** 后走 `/api/agent/run` SSE，实时渲染思考与工具卡片。
  - 用 `AbortController` 实现：切换页面或再次发送会中断上一条 SSE 流。

### 📁 文件
- 选仓库、填分支（默认 `main`），加载目录树并逐层浏览。
- 点击文件在编辑器中打开，显示文件大小与 SHA。
- `存草稿`：把「原始内容 + 编辑后内容」提交到服务器草稿箱，**不碰 GitHub**。
- 提交信息输入框（`commit_message`）随草稿一起保存。

### 📝 草稿箱
- 按当前 GitHub 账号隔离，Tab 上实时显示条数角标。
- 每条草稿展示 **行级 inline diff**（新增绿 / 删除红），可折叠。
- `✔ 授权推送 GitHub`：`confirm()` 二次确认后调用 GitHub Contents API 建 commit，返回 commit SHA 与链接。
- `✕ 拒绝`：删除草稿，不产生任何提交。
- `🗑 清空全部`：清空当前账号所有草稿。
- 同仓库同路径再次保存会**原地更新**并移到列表顶部（幂等 upsert），不会堆重复项。

### 🔑 API
- 本地管理多个模型提供商，字段：名称、类型（`openai` / `claude`）、Base URL、模型、API Key、自定义 Headers(JSON)。
- 全部存 `localStorage`，随时切换激活项；每次请求把完整 provider 信息内联发给服务器，**用完即弃**。

### ⚙️ 设置
- 显示当前 GitHub 账号，退出登录（清除本地 token）。
- 填写 `APP_ACCESS_TOKEN`（若服务端启用）。
- 一键检查 `/api/health`。

---

## 🤖 Agent 模式详解

### 工具集（恰好 4 个，全部打在 GitHub Contents API 上）

| 工具 | 必填参数 | 实际行为 |
|------|----------|----------|
| `list_dir` | 无（`path` 可选，`.`/空=根） | 列目录，条目**不做任何截断**（仅受 GitHub Contents API 自身返回上限约束） |
| `read_file` | `path` | 读文件；**> 500KB 直接拒绝**；返回内容再截到 50000 字符以保护上下文窗口 |
| `search_files` | `query` | 走 GitHub 代码搜索。新建/小仓库索引不可用时返回提示，引导模型改用 `list_dir` |
| `write_file` | `path`、`content`、`commit_message` | **不写 GitHub**，转为草稿箱条目，返回 `draft_id` |

### 循环控制
- `max_rounds` 默认 **10**，服务端把任意传入值钳制到 `1..20`（非法或超 20 一律回落 10）。
- 达到上限时发出 `done` 事件并附「已达最大轮数」，**不算错误**。
- 单轮内多个工具调用**顺序执行**。
- LLM HTTP 超时 90s，GitHub 客户端超时 30s。

### `write_file` 的真实路径（重要）

HTTP 层在每次 `/api/agent/run` 都会注入 `DraftSaver`，因此 Agent 的写操作**永远进草稿箱**：

```
LLM 请求 write_file
   → 命中受保护路径？ → 是：回一条拒绝文本给模型，流程继续
   → 读 GitHub 原文件作为 diff 左侧（新文件则为空）
   → 存草稿，拿到 draft_id
   → SSE 只推 {path, commit_message, draft_id}（正文不走 SSE，避免大文件撑爆流）
   → 你在草稿箱看 diff → 手动推送
```

> **关于「授权卡片」**：代码里确实存在一套逐步授权机制（`EventApproval` 事件、
> `/api/agent/approve` 接口、前端授权卡片），但它是 `DraftSaver == nil` 时的**遗留兜底路径**。
> 当前 HTTP 层始终注入 `DraftSaver`，所以正常使用时**不会弹出「✔ 允许写入」卡片**——
> 审核权已经上移到草稿箱。保留它是为了单测覆盖与将来「直写模式」的扩展。

### 多轮上下文
每一轮的工具调用与结果都会写进前端 `chatHistory` 并在下一轮回传，所以模型能记得
「我上一轮把哪个文件存成了哪条草稿」。即使某一轮没有最终文本（撞轮数上限或出错），
这一轮也会入历史，不会丢工具轨迹。草稿正文不进历史，只有 `draft_id` 作为可追溯引用，
模型需要时再对 `path` 调 `read_file`。

### 提示注入防线
`IsRestrictedPath()` 在**草稿暂存**与**直接写入**两条路径上都会拦截下列前缀（等于该前缀或其后代），
且路径先经过 `sanitizePath`（`filepath.Clean` + 拒绝 `..` 越界 + 转斜杠）：

```
.github/workflows    .github/actions    .ssh    .gnupg    Makefile
```

目的是防止仓库里的恶意内容通过提示注入，让 AI 改写 CI/CD 或凭据文件。

---

## 🔀 两条 AI 链路的差异

| | 普通对话 | Agent 模式 |
|---|---|---|
| 端点 | `POST /api/chat` | `POST /api/agent/run` |
| 传输 | 202 返回 `task_id`，前端轮询 `GET /api/tasks/{id}` | `text/event-stream` 长连接 |
| 工具调用 | 无 | 有（4 个 GitHub 工具） |
| 需要仓库上下文 | 否 | 是（`owner` + `repo`，缺则 400） |
| 超时 | 120s | 跟随请求 context |
| 结果存放 | 内存任务表 | SSE 直出 + 草稿箱 |

SSE 事件类型：`thinking`、`tool_call`、`tool_result`、`text`、`done`、`error`，
外加遗留的 `tool_approval` / `tool_approved` / `tool_rejected`。

---

## 🔐 安全模型（按代码实况）

| 项 | 现状 |
|----|------|
| OAuth CSRF | 16 字节随机 `state`，`HttpOnly` + `SameSite=Lax` + 5 分钟 cookie，回调时用 `subtle.ConstantTimeCompare` 比对后立刻失效 |
| OAuth 端点 | 请求 `scope=repo` |
| `redirect_uri` 来源 | 优先 `APP_PUBLIC_URL`；未设置时由 `X-Forwarded-Proto` + `Host` 推导（**反代后被伪造 Host 可污染授权地址，务必设置**） |
| 接口鉴权 | `APP_ACCESS_TOKEN` 为空 → 所有 `/api/*` **不做服务端鉴权**，仅靠 GitHub token 保护数据；非空 → 逐条接口常量时间比对 `X-App-Token` |
| GitHub 身份 | 涉及草稿/Agent 的接口先 `GET /user` 解析 login，结果按 `SHA-256(token)` 摘要缓存 5 分钟（超 256 条按过期时间回收）；草稿与授权决策按 login 隔离 |
| Agent token 强度 | `X-GitHub-Token` 长度 < 10 直接 401，挡掉公开部署下的随手伪造 |
| SSRF（模型 `base_url`） | 恒定拦截：链路本地含 `169.254.0.0/16` 云元数据、`fe80::/10`、组播/广播、`0.0.0.0`/`::`，以及 `metadata.google.internal`、`metadata.goog`；scheme 仅限 http/https。`SSRF_STRICT=1` 追加：回环、RFC1918/IPv6 ULA，并对主机名做 DNS 解析校验（解析失败即拒绝） |
| SSRF 默认取向 | 宽松——默认放行 localhost/内网，因为自托管最常见形态是指向本机 Ollama/vLLM |
| 路径穿越 | `sanitizePath` 统一清洗工具与文件接口的路径，`..` 与仓库外路径被丢弃 |
| UTF-8 | 所有预览/截断（`safeSnippet` 200、SSE 预览 4096、参数展示 300）按 rune 而非 byte，避免撕裂 CJK |
| 凭据持久化 | GitHub token 与 API Key 只进浏览器 `localStorage`；服务端唯一可能落盘的是 `DRAFTS_FILE`（`0600`），且草稿不含任何 token |

### ⚠️ 公开部署前必须知道的
1. 不设 `APP_ACCESS_TOKEN`，等于把 LLM 转发接口向全网敞开（任何人可拿你的端点当跳板）。
2. 只设 `APP_ACCESS_TOKEN` 也不够——它是**单个共享密钥**，无用户维度、无速率限制、无审计日志。
3. 本仓库**没有** HTTPS 终结、没有速率限制、没有 CSP 头。请交给反向代理（Caddy/Nginx）处理。

---


## 🚀 部署

> **本项目目前没有发布任何公共容器镜像，也没有 Release 产物。**
> 请从源码构建（下面两种方式都会 build 镜像）。`docker run ghcr.io/wszpwu1/zpwu-code` 拉不到东西。

### 第 0 步：创建 GitHub OAuth App

1. <https://github.com/settings/developers> → **New OAuth App**
2. 填写：
   - **Application name**：随意
   - **Homepage URL**：`https://你的域名`
   - **Authorization callback URL**：`https://你的域名/api/auth/github/callback`
3. 记下 **Client ID**，并 **Generate a new client secret** 拿到 **Client Secret**。
4. OAuth 回调地址必须与实际访问的协议+域名完全一致，否则 GitHub 会拒绝。这就是
   `APP_PUBLIC_URL` 存在的原因。

### 方式一：Docker Compose（推荐）

```bash
git clone https://github.com/wszpwu1/ZPWU-CODE.git
cd ZPWU-CODE
cp .env.example .env      # 编辑 .env，填 GITHUB_CLIENT_ID / SECRET / APP_PUBLIC_URL
docker compose up -d --build
docker compose logs -f
curl localhost:8080/api/health
```

`docker-compose.yml` 已经：
- `build: .` 本地构建镜像（不依赖任何远程镜像产物）
- 注入 `APP_PUBLIC_URL` / `SSRF_STRICT` / `DRAFTS_FILE` / `APP_ACCESS_TOKEN`
- 挂载命名卷 `zpwu-data:/data`，让 `/data/drafts.json` 跨容器重建保留
- 健康检查用 `HEALTHCHECK` 的 exec 形式调用二进制自检（`/zpwu -healthcheck`），
  因为 scratch 镜像里没有 shell 和 wget 可用

### 方式二：自己 build 镜像 + docker run

```bash
docker build -t zpwu-code:local .
docker run -d --name zpwu --restart unless-stopped -p 8080:8080 \
  -v zpwu-data:/data \
  -e GITHUB_CLIENT_ID=... \
  -e GITHUB_CLIENT_SECRET=... \
  -e APP_PUBLIC_URL=https://code.example.com \
  -e APP_ACCESS_TOKEN=一段长随机串 \
  -e DRAFTS_FILE=/data/drafts.json \
  zpwu-code:local
```

镜像构成：`golang:1.23-alpine` 构建 → `scratch` 运行，`CGO_ENABLED=0`、`-ldflags="-s -w"`，
只带 CA 证书 + 二进制 + `web/` 静态目录。

### 方式三：单二进制

```bash
go build -trimpath -ldflags="-s -w" -o zpwu ./cmd/server
export GITHUB_CLIENT_ID=... GITHUB_CLIENT_SECRET=... APP_PUBLIC_URL=https://code.example.com
./zpwu
```

**工作目录必须有 `web/`**：静态资源用 `http.FileServer(http.Dir("web"))` 按相对路径加载。
在服务器上请把 `web/` 与二进制放在一起，或从仓库根目录启动。

### 方式四：反向代理（实际生产必需）

OAuth 要求 HTTPS，而本程序只提供明文 HTTP 且不带任何安全头，所以务必前置 Caddy/Nginx：

```caddy
code.example.com {
    reverse_proxy localhost:8080
}
```

Caddy 会自动申请证书并传递 `X-Forwarded-Proto`，服务端据此给 OAuth `state` cookie 加上 `Secure`。
Nginx 需自行 `proxy_set_header X-Forwarded-Proto $scheme;`。

---

## 🔧 环境变量

| 变量 | 必填 | 默认 | 作用 |
|------|:----:|------|------|
| `GITHUB_CLIENT_ID` | ✅ | 空 | OAuth Client ID。缺失时 `/api/auth/github` 返回 503 `oauth_not_configured` |
| `GITHUB_CLIENT_SECRET` | ✅ | 空 | OAuth Client Secret。缺失时回调返回 503 |
| `APP_ADDR` | ❌ | `:8080` | 监听地址。写 `0.0.0.0:80` 直接绑公网口（Docker 端口映射场景**不要**加 `0.0.0.0` 前缀） |
| `APP_PUBLIC_URL` | 生产强烈建议 | 空 | 构建 OAuth `redirect_uri` 的可信基址，如 `https://code.example.com`（末尾 `/` 会被裁掉）。未设置时从 `Host` 头推导 |
| `APP_ACCESS_TOKEN` | ❌ | 空 | 服务端接口总闸。设置后除 `/api/health` 与 `/api/auth/*`（github / callback / user / repos）之外的 10 条 `/api/*` 全部必须带 `X-App-Token`（`subtle.ConstantTimeCompare` 比较）；留空则总闸完全不生效 |
| `DRAFTS_FILE` | ❌ | 空 | 草稿箱 JSON 落盘路径。非空则启动时加载、每次变更原子写（tmp+rename，权限 `0600`）；文件损坏时告警并忽略，**不会覆盖** |
| `SSRF_STRICT` | ❌ | 关 | `1/true/yes/on` 视为开启。开启后模型 `base_url` 不得使用回环/私网地址，并解析主机名校验；**用本地 Ollama/vLLM 时不要开** |

---

## 📡 API 参考（15 条路由，与代码逐一对应）

鉴权列说明：**GH** = 必须带 `X-GitHub-Token`；**App** = 仅当服务端设置了 `APP_ACCESS_TOKEN` 时需要 `X-App-Token`。

| 方法 | 路径 | 鉴权 | 说明 |
|------|------|:----:|------|
| `GET` | `/api/health` | — | 返回 `status/time/checks{github_oauth, storage, ssrf_strict}`，不要求任何凭据 |
| `GET` | `/api/auth/github` | — | 302 跳 GitHub 授权页（`scope=repo`，带随机 `state`）；未配置 ClientID → 503 |
| `GET` | `/api/auth/github/callback` | — | 校验 `state` → 换 token → 取 `/user` → 返回一小段内联脚本页面，把 token/login/avatar 写入 `localStorage['zpwu_config']` 后 `location.replace('/')`（刻意不走 URL fragment，避免 token 泄漏到历史/Referer） |
| `GET` | `/api/auth/user` | GH | 转发 `GET /user`。**不**校验 App Token |
| `GET` | `/api/auth/repos` | GH | `?sort=updated&per_page=50&type=all` —— **只返回前 50 个仓库**，无分页参数透传 |
| `GET` | `/api/tasks` | App | 最近任务列表（硬编码 20 条，存储上限 100 条） |
| `GET` | `/api/tasks/{id}` | App | 单任务状态：`queued` / `running` / `completed` / `failed` |
| `POST` | `/api/chat` | App | 异步对话：立即 202 + `task_id`，需内联 `provider.api_key` |
| `POST` | `/api/git/sync` | App+GH | 异步直写文件到 GitHub，创建 `git_sync` 任务。**前端「文件」Tab 走的是草稿箱，不调用此接口** |
| `GET` | `/api/git/files` | App+GH | `?owner=&repo=&branch=&path=`（branch 默认 `main`）列目录 |
| `GET` | `/api/git/file` | App+GH | 同上参数读单文件 |
| `POST` | `/api/agent/run` | App+GH | SSE 流。GH token 长度需 ≥ 10；`owner`/`repo` 必填 |
| `POST` | `/api/agent/approve` | App+GH | 提交 `{call_id, approved}`，唤醒遗留授权兜底路径 |
| `POST` | `/api/drafts` | App+GH | 新建/更新草稿（需 `owner`/`repo`/`file_path`），按 login 隔离 |
| `GET` | `/api/drafts` | App+GH | 列出当前账号草稿 |
| `DELETE` | `/api/drafts?id=xxx` | App+GH | 删除单条 |
| `DELETE` | `/api/drafts?all=1` | App+GH | 清空当前账号全部草稿 |
| `POST` | `/api/drafts/push` | App+GH | 推送指定草稿 → 真正产生 GitHub commit |

> 表中 `DELETE /api/drafts` 的两个变体在代码里是同一路由的分支（`/api/drafts` 按 query 参数区分 POST/GET/DELETE）。
> 错误统一为 `{error_code, message}`；方法不匹配一律 405。

---

## 📱 PWA 与静态资源

- `web/manifest.webmanifest` 声明 `display: standalone`、`start_url: "/"`、`scope: "/"`、
  3 个 `icons` 条目（192 any / 512 any / 512 maskable），`apple-touch-icon.png` 另由 `index.html` 的
  `<link rel="apple-touch-icon">` 引用；名称 `ZPWU-CODE 控制台` / 短名 `ZPWU`，主题色 `#080c14`。
- 服务端已显式注册 `.webmanifest → application/manifest+json`。**这一行不能删**：Go 内置 MIME
  表不认识 `.webmanifest`，否则会被嗅探成 `text/plain`，Chrome 直接拒绝解析清单，安装功能静默失效。
- `web/sw.js`：`CACHE_NAME = 'zpwu-code-v4'`，预缓存 9 个静态资源，**网络优先**，失败才回缓存，
  且**只接管同源 GET**——`/api/*` 与跨域请求一律直连服务器。这意味着：
  - 界面壳子可离线打开，但任何真实数据/对话都需要服务器在线。
  - 改了前端资源必须把 `CACHE_NAME` 版本号 `+1`，否则老客户端继续吃旧缓存。
- 图标：`web/icons/` 内置 `icon-192.png`、`icon-512.png`、`maskable-icon-512.png`、
  `apple-touch-icon.png`。重新生成（Windows PowerShell，依赖 System.Drawing）：

  ```powershell
  powershell -File tools/gen-icons.ps1
  ```

---

## 🛠 本地开发

```bash
go build ./...          # 零第三方依赖，不需要联网拉包
go vet ./...
go test ./...           # 10 个测试函数（agent 5 / 草稿箱 3 / 授权存储 2）
go run ./cmd/server     # 默认 :8080
```

常用调试组合：

```bash
APP_ADDR=:8080 DRAFTS_FILE=./tmp-drafts.json go run ./cmd/server
curl -s localhost:8080/api/health
curl -sI localhost:8080/manifest.webmanifest   # 必须是 application/manifest+json
```

说明与注意：
- 仓库内**没有** `.github/workflows`，CI 尚未配置；也没有 `LICENSE` 文件。
- `go test -race` 需要 cgo/GCC，纯 Windows 无 GCC 的环境跑不动，请在 Linux 容器里执行：
  `docker run --rm -v "$PWD":/w -w /w golang:1.23-alpine sh -c "apk add --no-cache gcc musl-dev && go test -race ./..."`
- 没有 `.dockerignore`，构建上下文会把 `.git` 一起送进 daemon；仓库小，暂时不影响正确性。
- `internal/githubsync/` 目前只有一个 `.gitkeep` 占位，没有代码。

---




## 🧭 使用流程

```
1. 打开站点 → 「使用 GitHub 登录」→ GitHub 授权 → 自动写 localStorage 并跳回首页
2. 「🔑 API」→ 添加提供商（名称/类型/Base URL/模型/API Key）→ 保存并激活
3. 「📁 文件」→ 选仓库 → 填分支 → ↺ 加载目录树

   手工改文件：点文件 → 编辑 → 「存草稿」 → 「📝 草稿箱」看 diff → 「✔ 授权推送」
   让 AI 改文件：「💬 对话」→ 勾「🤖 Agent 模式」→ 描述任务
                 → AI 自己 list_dir / read_file / search_files
                 → write_file 自动变成草稿 → 去草稿箱审核并推送
```

需要先在「文件」Tab 选定仓库，Agent 模式才会可用（未选仓库就勾选会提示）。

---

## 🚧 已知限制与尚未实现

按重要度排列，全部是**代码现状**而非推测：

**功能边界**
- 只支持 GitHub，且 OAuth 申请的是 `scope=repo`；GitLab/Gitea/自建 Gitea 均不支持。
- 单文件操作。没有多文件原子提交、没有分支/PR 管理、没有 diff 之外的合并能力、不支持删除文件。
- `read_file` 上限 500KB，超出部分还会被截到 50000 字符喂给模型——大文件场景会静默丢内容。
- `list_dir` 完全不截断，遇到超大目录会把整份清单塞进上下文，可能顶爆窗口；同目录条目数也受 GitHub
  Contents API 自身上限约束（超限目录该接口会报错而非分页返回）。
- `/api/auth/repos` 固定 `per_page=50` 且不透传分页，仓库多于 50 个的账号选不到后面的。
- 普通对话无流式输出，只有任务轮询；界面上看不到逐字打字效果。
- Agent 无「删除文件 / 执行命令 / 联网检索」能力，工具面就是那 4 个。

**部署与运维**
- **未发布公共镜像、无 Release、无 CI**：README 里不存在可直接 pull 的镜像地址。
- 无 HTTPS、无 HTTP 安全头（CSP/HSTS/X-Frame-Options 全部依赖反向代理）、无速率限制、无审计日志。
- **`APP_ACCESS_TOKEN` 默认为空 = 服务端总闸完全不生效**（`authorizeRequest` 直接放行）。此时任何能连到该端口的客户端都能拉 `/api/tasks`，而 `chat` 任务的 `Input` 里存着**完整对话 messages**。公网暴露必须先设这个变量，或把端口锁在反向代理后面。
- `APP_ACCESS_TOKEN` 是单个共享密钥，无多用户、无轮换、无撤销机制。
- 草稿落盘失败（如卷只读）只打日志、不返回错误，会静默退回纯内存模式。
- 任务表与（默认配置下的）草稿箱都在内存里：进程重启即丢，任务表还只保留最近 100 条。
- 单进程单二进制，无优雅停机（收到 SIGTERM 直接退出，进行中的 SSE 会被掐断）。

**安全**
- `SSRF_STRICT` 默认关闭，即默认允许把模型端点指向内网/回环地址——自托管友好，但公网多租户部署必须开启。
- 非严格模式下不对主机名做 DNS 解析，**DNS rebinding（域名解析到内网 IP）不在防御范围内**。
- 受限路径黑名单是固定的 5 个前缀，写死在代码里，**没有配置项**，也不能覆盖 `.github/ISSUE_TEMPLATE` 之外的自定义敏感目录。
- 草稿内容对所有已鉴权的同 login 请求可见，但没有更细粒度的权限分层。

**测试覆盖缺口**
- 10 个测试集中在 SSRF / 端点规范化 / rune 截断 / 草稿存储 / 授权存储。
- **`IsRestrictedPath` 没有直接单测**，所有 `/api/*` HTTP 处理器、前端逻辑同样没有测试。
- 未在真实浏览器里验证过 GitHub OAuth 全链路与 PWA 安装弹窗。
- 一个已知的元数据小瑕疵：`index.html` 把 512×512 的 `apple-touch-icon.png` 声明为 `sizes="180x180"`。

---

## 🗂 项目结构

```
ZPWU-CODE/
├── cmd/server/
│   ├── main.go            # 入口：路由挂载、静态服务、MIME 注册、-healthcheck/-version 子命令
│   └── healthcheck.go     # 容器自检（scratch 镜像无 shell，只能由二进制自证）
├── internal/
│   ├── config/config.go   # 7 个环境变量 → Config
│   ├── handlers/
│   │   ├── handlers.go    # 15 条路由 + 任务表 + 草稿箱 + 授权存储 + LLM 网关 + login 缓存
│   │   ├── handlers_test.go
│   │   └── draft_store_test.go
│   ├── agent/
│   │   ├── tools.go       # 4 个工具的 schema 与 GitHub Contents API 执行、受限路径判断
│   │   ├── loop.go        # Agent Loop（OpenAI + Claude 双栈、SSE 事件、草稿路由）
│   │   ├── ssrf.go        # 模型 base_url 校验（默认/严格两档）
│   │   └── ssrf_test.go
│   └── githubsync/        # 空目录占位（.gitkeep），无代码
├── web/
│   ├── index.html         # 5 Tab 单页，308 行
│   ├── app.js             # Auth / 文件 / 草稿+diff / Agent SSE / provider 管理，832 行
│   ├── styles.css         # 移动优先暗色主题，726 行
│   ├── manifest.webmanifest
│   ├── sw.js              # 网络优先 SW，缓存版本 zpwu-code-v4
│   └── icons/             # 4 个 PNG 图标
├── tools/gen-icons.ps1    # 依赖 System.Drawing，仅 Windows PowerShell
├── Dockerfile             # golang:1.23-alpine → scratch，含 HEALTHCHECK
├── docker-compose.yml     # 含 zpwu-data:/data 卷与全部新环境变量
├── .env.example
└── go.mod                 # module github.com/wszpwu1/ZPWU-CODE, go 1.23, 无 require 依赖
```

---

## 🗺 下一步

按投入产出排序，均为**尚不存在**的能力：

1. 给 `IsRestrictedPath` 与 `/api/*` 各处理器补单测（当前最大的盲区）。
2. 加 `.github/workflows/ci.yml`：`gofmt` + `go vet` + `go build` + `go test -race`，
   再串 `docker build` + GHCR 推送，让上面那个 `image:` 真的能拉到。
3. 补 `LICENSE`（目前没有任何授权声明，默认保留所有权利）。
4. `.dockerignore` 排除 `.git`、`*.exe`、`tmp-*`。
5. 受限路径黑名单改为可配置（如 `BLOCKED_PATHS` 环境变量），而不是写死 5 项。
6. 草稿箱加内容级校验与过期时间；任务表加落盘或明确声明「仅调试用途」。
7. 若确认草稿箱是唯一写入通道，删掉遗留授权子系统（`approvalStore`、`/api/agent/approve`、
   `EventApproval` 三件套与前端卡片），减少一条永不被执行却仍需维护的代码路径。

---

## 📄 许可

尚未添加 `LICENSE` 文件，因此**默认保留所有权利**。发布前请先补上你需要的许可证。
