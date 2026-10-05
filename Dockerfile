# 多阶段构建 - 第一阶段：构建
# 上下文必须是仓库根目录（web 通过 replace => ../ 引用根模块协议库）
FROM golang:1.25-alpine AS builder

# 替换 Alpine 镜像源为阿里云
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories

WORKDIR /app

ENV GO111MODULE=on \
    GOPROXY=https://goproxy.cn,https://mirrors.aliyun.com/goproxy/,direct \
    GOTOOLCHAIN=auto \
    CGO_ENABLED=0 \
    GOOS=linux \
    GOARCH=amd64

# 先复制模块文件，利用 Docker 层缓存加速依赖下载
COPY go.mod go.sum ./
COPY web/go.mod web/go.sum ./web/
RUN go mod download && (cd web && go mod download)

# 复制全部源码并编译（web 为独立 module，replace 指向根目录协议库）
COPY . .
RUN (cd web && go build -ldflags="-s -w" -o ../tdx-web .)

# 第二阶段：运行
FROM alpine:latest

RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories && \
    apk --no-cache add ca-certificates tzdata wget

ENV TZ=Asia/Shanghai

RUN addgroup -g 1000 appuser && \
    adduser -D -u 1000 -G appuser appuser

WORKDIR /app

COPY --from=builder /app/tdx-web .
COPY --from=builder /app/web/static ./static

# 代码库/交易日等 sqlite 数据目录（tdx.DefaultDatabaseDir = ./data/database）
RUN mkdir -p ./data/database && chown -R appuser:appuser /app

USER appuser

# 容器内固定 8080，宿主机端口由 compose 映射决定（PORT 环境变量一般无需改动）
EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=30s --retries=3 \
    CMD wget --no-verbose --tries=1 --spider http://localhost:8080/api/health || exit 1

CMD ["./tdx-web"]
