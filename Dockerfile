# ============================================================
# 阶段 1：编译
# ============================================================
# 基础镜像做成参数：国内服务器直连 docker.io 常被墙，而 daemon.json 里配的
# 镜像站又可能缺某个 tag 的 manifest（报 "load metadata ... not found"）。
# 这时不用改 Dockerfile，直接换来源即可：
#   docker build --build-arg GO_IMAGE=docker.m.daocloud.io/library/golang:1.22-alpine \
#                --build-arg RUNTIME_IMAGE=docker.m.daocloud.io/library/alpine:3.20 .
ARG GO_IMAGE=golang:1.22-alpine
ARG RUNTIME_IMAGE=alpine:3.20

FROM ${GO_IMAGE} AS builder

WORKDIR /app

# 版本号：优先 --build-arg VERSION=1.0.1，否则读仓库根目录的 VERSION 文件
# （在线更新就是拿这个值和远端 VERSION 比对，两边必须一致）
ARG VERSION=
# 构建时间：自动取当前 UTC 时间，也可手动 --build-arg BUILD_TIME=... 覆盖
ARG BUILD_TIME=unknown

# 唯一第三方依赖是 SQLite 驱动（modernc.org/sqlite，纯 Go 实现，不需要 CGO）。
# 国内构建走 goproxy.cn；要完全离线可自行 go mod vendor 后改这里。
ARG GOPROXY=https://goproxy.cn,direct
ENV GOPROXY=${GOPROXY}

COPY go.mod go.sum ./
COPY VERSION ./
RUN go mod download

# ⚠ go:embed 的文件必须在编译阶段就位，否则报
#   "pattern index.html: no matching files found"
# 静态页面由 web/assets.go 嵌入，所以 web/ 必须在 RUN go build 之前 COPY 进来
COPY cmd/ ./cmd/
COPY internal/ ./internal/
COPY web/ ./web/

# CGO_ENABLED=0 静态编译：产物不依赖 libc，换基础镜像也能跑
# 版本号 / 构建时间注入到 internal/config 包（启动日志里能看到镜像构建于何时）
RUN V="${VERSION:-$(cat VERSION 2>/dev/null || echo dev)}" && \
    CGO_ENABLED=0 go build -trimpath \
    -ldflags="-s -w -X wxcode/internal/config.Version=${V} -X wxcode/internal/config.BuildTime=${BUILD_TIME}" \
    -o wxcode ./cmd/wxcode

# ============================================================
# 阶段 2：运行
# ============================================================
FROM ${RUNTIME_IMAGE}

# 换阿里云镜像源加速；补 tzdata（日志要打东八区时间）与证书（logo 支持 http(s) 拉取）
RUN sed -i 's/dl-cdn.alpinelinux.org/mirrors.aliyun.com/g' /etc/apk/repositories && \
    apk add --no-cache tzdata ca-certificates

ENV TZ=Asia/Shanghai \
    PORT=8080 \
    LOG_LEVEL=info \
    LOG_FORMAT=text \
    LOG_OUTPUT=both \
    LOG_ACCESS=1

WORKDIR /app

# 页面已 go:embed 进二进制，运行阶段只需要一个可执行文件
COPY --from=builder /app/wxcode /app/wxcode

# 数据目录（SQLite 文件放这里），挂卷持久化
ENV DATA_DIR=/app/data
RUN mkdir -p /app/data && \
    adduser -D -H -u 10001 app && \
    chown -R app:app /app/wxcode /app/data
VOLUME ["/app/data"]

# 容器内以 nobody 运行，降低被利用的影响面
USER app

EXPOSE 8080

# 用 busybox wget 探活（alpine 自带），docker ps 里能直接看到 healthy
HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
    CMD wget -qO- "http://127.0.0.1:${PORT}/api/health" >/dev/null || exit 1

# 显式声明停止信号：docker stop 发的是 SIGTERM，
# 程序收到后走优雅关闭（等在途请求结束，最多 5 秒），
# 不会被 SIGKILL 打断到一半留下未写完的响应。
STOPSIGNAL SIGTERM

# 日志一律走 stdout/stderr，容器里用 `docker logs` 查看；
# 不写日志文件（容器重建即丢，还会占磁盘）。
CMD ["/app/wxcode"]
