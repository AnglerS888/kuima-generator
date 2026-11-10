# ============================================================
# 阶段 1：编译
# ============================================================
# 基础镜像做成参数：国内服务器直连 docker.io 常被墙，而 daemon.json 里配的
# 镜像站又可能缺某个 tag 的 manifest（报 "load metadata ... not found"）。
# 这时不用改 Dockerfile，直接换来源即可：
#   docker build --build-arg GO_IMAGE=docker.m.daocloud.io/library/golang:1.22-alpine \
#                --build-arg RUNTIME_IMAGE=docker.m.daocloud.io/library/golang:1.22-alpine .
#
# 运行镜像直接用 golang 基座（不是精简的 alpine）：后台「立即更新」在没有
# 发布产物时会下载源码并在容器内 go build，所以镜像里得带编译器。
# 代价是镜像约 300MB；想要小镜像可 --build-arg RUNTIME_IMAGE=alpine:3.20，
# 但那样容器内就只能下载现成的 Release 产物来更新。
ARG GO_IMAGE=golang:1.22-alpine
ARG RUNTIME_IMAGE=golang:1.22-alpine

FROM ${GO_IMAGE} AS builder

WORKDIR /app

# 版本号：优先 --build-arg VERSION=1.1.0，否则读仓库根目录的 VERSION 文件
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
# 阶段 2：运行（golang:alpine 基座，容器内可自编译更新）
# ============================================================
FROM ${RUNTIME_IMAGE}

# 换阿里云镜像源加速（国内 dl-cdn 常超时），并补三样运行期需要的东西：
#   git             —— 容器内 go build 时，GOPROXY 出错会回退 direct 直连 VCS，
#                      golang:alpine 本身不带 git，缺了会报 "git: executable file not found"
#   tzdata          —— 日志要打东八区时间
#   ca-certificates —— 拉 logo、检查更新、下载源码都走 HTTPS
ARG APK_MIRROR=mirrors.aliyun.com
RUN sed -i "s|dl-cdn.alpinelinux.org|${APK_MIRROR}|g" /etc/apk/repositories && \
    apk add --no-cache git tzdata ca-certificates

# GOPROXY 给容器内「立即更新」的 go build 用：第一个挂了自动换下一个（"|" 语义）
ENV TZ=Asia/Shanghai \
    PORT=8080 \
    LOG_LEVEL=info \
    LOG_FORMAT=text \
    LOG_OUTPUT=both \
    LOG_ACCESS=1 \
    GOPROXY=https://goproxy.cn|https://goproxy.io|direct \
    GOTOOLCHAIN=auto

WORKDIR /app

# 页面已 go:embed 进二进制，运行阶段只需要一个可执行文件
COPY --from=builder /app/wxcode /app/wxcode

# 数据目录（SQLite 文件放这里），挂卷持久化
ENV DATA_DIR=/app/data
# ⚠ 必须把整个 /app 交给 app 用户，而不只是 /app/wxcode：
# 在线更新要先把 wxcode 改名成 wxcode.old、再写入新文件，这需要「目录」写权限。
# 只 chown 文件时 rename 会报 permission denied。
RUN mkdir -p /app/data && \
    adduser -D -H -u 10001 app && \
    chown -R app:app /app
VOLUME ["/app/data"]

# 容器内以非 root 的 app 用户（uid 10001）运行，降低被利用的影响面
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
