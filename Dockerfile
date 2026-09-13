# ── 阶段一：编译 Go 二进制 ──────────────────────────────
FROM golang:1.23-alpine AS builder
# go.mod declares go 1.23, image matches exactly

WORKDIR /build

# 先拷贝依赖描述文件，利用层缓存
# 注意：本项目零第三方依赖（纯标准库），仓库内没有 go.sum，
# 因此只拷贝 go.mod；若将来引入依赖需同时 COPY go.sum。
COPY go.mod ./
RUN go mod download

# 拷贝源码并编译（静态链接，极小体积）
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
    go build -ldflags="-s -w" -o /zpwu ./cmd/server

# ── 阶段二：最终镜像（scratch 最小化）────────────────────
FROM scratch

# 时区与 CA 证书（访问 GitHub API / 各 AI 厂商 HTTPS 必须）
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/

# 拷贝二进制
COPY --from=builder /zpwu /zpwu

# 拷贝前端静态文件
COPY --from=builder /build/web /web

# 默认监听端口
EXPOSE 8080

# 工作目录设为 / 即可，web 目录由程序内部引用 "web"
WORKDIR /

# 监听地址可用 --build-arg APP_ADDR=:80 覆盖；运行时也可用同名环境变量覆盖。
ARG APP_ADDR=:8080
ENV APP_ADDR=${APP_ADDR}

# 最终镜像是 scratch：没有 shell 也没有 wget，健康检查必须以 exec 形式
# 由二进制自检（/zpwu -healthcheck），否则容器会一直被标记为 unhealthy。
HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
  CMD ["/zpwu", "-healthcheck"]

ENTRYPOINT ["/zpwu"]
