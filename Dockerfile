# ── 阶段一：编译 Go 二进制 ──────────────────────────────
FROM golang:1.23-alpine AS builder
# go.mod declares go 1.23, image matches exactly

WORKDIR /build

# 先拷贝依赖描述文件，利用层缓存
COPY go.mod go.sum ./
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

ENV APP_ADDR=:8080

ENTRYPOINT ["/zpwu"]
