#!/bin/bash
# ============================================================
# 葵码生成器 — 一键部署脚本
#
# 用法：
#   ./deploy.sh                    # 自动选线路（国内 Gitee / 国外 GitHub）拉 main → 构建 → 换容器
#   ./deploy.sh --local            # 用当前工作区代码构建（不下载，适合本机调试）
#   ./deploy.sh --status           # 查看容器状态、管理员账号与密码
#   ./deploy.sh --reset-admin      # 把管理员密码重置成凭据文件里的值（忘记密码时用）
#   ./deploy.sh --rollback         # 回滚到上一次成功部署的镜像
#   PORT=9090 ./deploy.sh          # 换宿主机端口（容器内固定 8080）
#   DATA_VOLUME=wxcode-data ./deploy.sh   # 指定数据库卷名（默认 wxcode-data）
#   BRANCH=dev ./deploy.sh         # 换分支（默认 main；找不到自动再试 master / 同名 tag）
#
# 代码线路（两个仓库内容保持同步，脚本内部自动判断，没有任何选线路的参数）：
#   线路 1（国内）：https://gitee.com/anglers/kuima-generator
#   线路 2（国外）：https://github.com/AnglerS888/kuima-generator
#   * 启动时探测两个域名的响应速度，谁快先走谁，另一个自动兜底
#     （国内服务器自然走 Gitee，国外服务器自然走 GitHub）
#   * 归档下载失败时，会自动改用 git clone 再走一遍这两条线路
#   * 关掉 GitHub 加速：GH_MIRROR= ./deploy.sh（只走 GitHub 直连）
#   * 换仓库：  GITEE_REPO=owner/repo / GITHUB_REPO=owner/repo ./deploy.sh
#   * 换来源：  CODE_URL=https://example.com/src.tar.gz ./deploy.sh（自建 Git / 内网镜像）
#   * 直接克隆：GIT_URL=https://gitee.com/anglers/kuima-generator.git ./deploy.sh
#
# 管理员账号：
#   * 默认用户名 admin，密码首次部署时随机生成（16 位十六进制），
#     保存在当前目录的 .wxcode-admin.env（权限 600），部署结束会打印出来；
#   * 想自己指定：ADMIN_USER=root ADMIN_PASSWORD=你的密码 ./deploy.sh
#   * 重新部署会复用同一个密码，不会变；改了密码后想同步显示，用 --reset-admin。
#
# 私有仓库需要凭据时（GitHub 用 Personal Access Token，勾 repo 读权限即可）：
#   GIT_TOKEN=ghp_xxx ./deploy.sh
#   需要用户名时：GIT_USER=你的账号 GIT_TOKEN=xxx ./deploy.sh
#
# 基础镜像拉不下来时（国内常见：镜像站缺 manifest，报 load metadata ... not found）：
#   BASE_REGISTRY=docker.m.daocloud.io ./deploy.sh
#   （不指定时脚本会自动依次换源重试）
#
# 说明：
#   * 脚本只依赖 docker + curl + tar，可在任意 Linux 主机直接跑；
#   * 构建失败不会动线上容器（先构建、成功后才替换）；
#   * 每次部署前会把当前 latest 打个 previous 标签，出问题可 --rollback；
#   * 部署成功后会真的用「终端显示的账号密码」调一次登录接口，验证可用才报成功。
# ============================================================

set -euo pipefail

# ---------- 配置（都可以用环境变量覆盖）----------
IMAGE_NAME="${IMAGE_NAME:-wxcode}"
CONTAINER_NAME="${CONTAINER_NAME:-wxcode}"
HOST_PORT="${HOST_PORT:-${PORT:-8080}}"   # 宿主机端口（PORT 也可，和 README 里一致）
CONTAINER_PORT="${CONTAINER_PORT:-8080}"
# 代码仓库：国内用 Gitee，国外用 GitHub（两边内容保持同步）
GITEE_REPO="${GITEE_REPO:-anglers/kuima-generator}"          # Gitee owner/repo
GITHUB_REPO="${GITHUB_REPO:-${REPO:-AnglerS888/kuima-generator}}"  # GitHub owner/repo（REPO 为旧名，兼容）
BRANCH="${BRANCH:-main}"
# 版本号：显式给了就用，否则取源码里的 VERSION 文件（在线更新靠它比对），兜底时间戳
VERSION_ENV="${VERSION:-}"
VERSION=""
# CODE_URL 非空时直接用它下载，不再走上面的候选地址（自建 Git / 内网镜像用）
CODE_URL="${CODE_URL:-}"
# GitHub 下载加速前缀：默认 gh-proxy.org（国内直连 GitHub 经常超时/502）。
# 置空（GH_MIRROR=）则只走直连；换别的镜像前缀同理，如 https://ghfast.top/
GH_MIRROR="${GH_MIRROR:-https://gh-proxy.org/}"
# 非空时改用 git clone（需要 git），例如：
#   GIT_URL=https://gitee.com/anglers/kuima-generator.git
GIT_URL="${GIT_URL:-}"
HEALTH_PATH="${HEALTH_PATH:-/api/health}"
DATA_VOLUME="${DATA_VOLUME:-wxcode-data}"   # SQLite 数据卷，删容器不丢数据
GOPROXY_ARG="${GOPROXY:-https://goproxy.cn,direct}"  # 构建时拉依赖走的代理

# 管理员凭据：环境变量显式指定的优先级最高，其次读凭据文件，最后随机生成
ADMIN_USER_ENV="${ADMIN_USER:-}"
ADMIN_PASS_ENV="${ADMIN_PASSWORD:-}"
ADMIN_ENV_FILE="${ADMIN_ENV_FILE:-$PWD/.wxcode-admin.env}"
ADMIN_USER="admin"
ADMIN_PASSWORD=""

# 基础镜像（Dockerfile 里用 ARG 接收，构建时可用 build-arg 覆盖）
# 运行镜像也用 golang:alpine：镜像里带 Go，后台「立即更新」才能在容器内
# 下载源码 + go build（镜像约 300MB）。想回小镜像就把 RUNTIME 换成 alpine:3.20。
GO_NAME="golang"; GO_TAG="1.22-alpine"
RUNTIME_NAME="golang"; RUNTIME_TAG="1.22-alpine"

# 国内服务器直连 docker.io 常失败，而 daemon.json 里配的镜像站又可能缺某个 tag 的 manifest。
# 这里按顺序尝试；BASE_REGISTRY 显式指定时优先用它（如 docker.m.daocloud.io）。
IMAGE_SOURCES=()
[ -n "${BASE_REGISTRY:-}" ] && IMAGE_SOURCES+=("$BASE_REGISTRY")
IMAGE_SOURCES+=("" "docker.m.daocloud.io" "docker.1ms.run" "docker.1panel.live" "docker.xuanyuan.me")

# 这些关键词说明"基础镜像没拉到"，可以换源重试；编译错误不在此列
PULL_ERR_RE='failed to resolve source metadata|failed to pull|pull access denied|manifest unknown|no such host|i/o timeout|TLS handshake|connection refused|failed to do request|toomanyrequests|429 Too Many Requests'

LOCAL_MODE=0
ROLLBACK=0
STATUS_ONLY=0
RESET_ADMIN=0
for arg in "$@"; do
    case "$arg" in
        --local)        LOCAL_MODE=1 ;;
        --rollback)     ROLLBACK=1 ;;
        --status)       STATUS_ONLY=1 ;;
        --reset-admin)  RESET_ADMIN=1 ;;
        -h|--help)      awk 'NR>1 && /^set -euo pipefail/{exit} NR>1{sub(/^# ?/,""); print}' "$0"; exit 0 ;;
        *) echo "未知参数：$arg（--help 看用法）" >&2; exit 2 ;;
    esac
done

# ---------- 颜色 ----------
GREEN='\033[0;32m'; RED='\033[0;31m'; YELLOW='\033[1;33m'; CYAN='\033[0;36m'; NC='\033[0m'
log()  { echo -e "${GREEN}[INFO]${NC}  $(date '+%H:%M:%S')  $1"; }
warn() { echo -e "${YELLOW}[WARN]${NC}  $(date '+%H:%M:%S')  $1"; }
err()  { echo -e "${RED}[ERROR]${NC} $(date '+%H:%M:%S')  $1"; }

# ---------- 源码下载地址 ----------
# 两个仓库：国内 Gitee、国外 GitHub，内容保持同步。
# 脚本不带任何「选线路」参数：进来先探测两边连通性，谁快先走谁，
# 另一个留作兜底 —— 国内服务器自然落到 Gitee，国外服务器自然落到 GitHub。

# probe_ms 返回访问该地址的毫秒数（失败返回 9999，用于排序）。
probe_ms() {
    local t
    if ! t=$(curl -o /dev/null -sS --max-time 3 -w '%{time_total}' "$1" 2>/dev/null); then
        printf '9999\n'
        return 0
    fi
    awk -v t="$t" 'BEGIN{printf "%d\n", t * 1000}'
}

# source_order 输出尝试顺序，一行一个仓库名（gitee / github）。
# 两个都探不通时仍按「Gitee → GitHub」的顺序硬试一遍，交给后面的重试和克隆兜底。
source_order() {
    local g h
    g=$(probe_ms "https://gitee.com/${GITEE_REPO}" || echo 9999)
    h=$(probe_ms "https://github.com/${GITHUB_REPO}" || echo 9999)
    if [ "$g" -le "$h" ]; then
        printf 'gitee\ngithub\n'
    else
        printf 'github\ngitee\n'
    fi
}

# refs_to_try 输出去重后的分支候选：指定分支 → main → master。
refs_to_try() {
    local tried="" b
    for b in "$BRANCH" main master; do
        case " $tried " in *" $b "*) continue ;; esac
        tried="$tried $b"
        printf '%s\n' "$b"
    done
}

# archive_candidates 输出某个仓库下的候选归档地址。
#   Gitee：  /repository/archive/<ref>.tar.gz（分支和 tag 同一个形式）
#   GitHub：/archive/refs/heads/<ref>.tar.gz（再补一个 refs/tags/<ref>），可套 GH_MIRROR
archive_candidates() {   # $1 = gitee | github
    local src="$1" ref url
    while IFS= read -r ref; do
        [ -n "$ref" ] || continue
        if [ "$src" = "gitee" ]; then
            printf 'https://gitee.com/%s/repository/archive/%s.tar.gz\n' "$GITEE_REPO" "$ref"
            continue
        fi
        url="https://github.com/${GITHUB_REPO}/archive/refs/heads/${ref}.tar.gz"
        if [ -n "$GH_MIRROR" ]; then
            printf '%s%s\n' "${GH_MIRROR%/}/" "$url"
        fi
        printf '%s\n' "$url"
    done <<EOF
$(refs_to_try)
EOF
    # 同名 tag（有人用标签发布而不是分支）
    if [ "$src" = "github" ]; then
        url="https://github.com/${GITHUB_REPO}/archive/refs/tags/${BRANCH}.tar.gz"
        if [ -n "$GH_MIRROR" ]; then
            printf '%s%s\n' "${GH_MIRROR%/}/" "$url"
        fi
        printf '%s\n' "$url"
    fi
}

# candidate_urls 按「来源优先级 → 分支 → 直连/镜像」列出所有要尝试的地址。
# SRC_ORDER 已算过时直接复用，避免重复探测拖慢启动。
candidate_urls() {
    if [ -n "$CODE_URL" ]; then
        printf '%s\n' "$CODE_URL"
        return 0
    fi
    local src order
    order="${SRC_ORDER:-$(source_order)}"
    while IFS= read -r src; do
        [ -n "$src" ] || continue
        archive_candidates "$src"
    done <<EOF
$order
EOF
}

# ---------- 环境检查 ----------
check_env() {
    command -v docker >/dev/null 2>&1 || { err "Docker 未安装"; exit 1; }
    docker info >/dev/null 2>&1 || { err "Docker 服务未运行（或当前用户没有权限）"; exit 1; }
    command -v curl   >/dev/null 2>&1 || { err "curl 未安装"; exit 1; }
    command -v tar    >/dev/null 2>&1 || { err "tar 未安装"; exit 1; }
}

container_exists() { docker inspect "$CONTAINER_NAME" >/dev/null 2>&1; }

# ---------- 管理员凭据 ----------
# 密码只用于展示和登录验证，不做任何日志落盘（容器日志里也不会打印）
gen_password() {
    if command -v openssl >/dev/null 2>&1; then
        openssl rand -hex 8                 # 16 位十六进制
    else
        od -An -N8 -tx1 /dev/urandom | tr -d ' \n'
    fi
}

load_admin_credentials() {
    local u="" p=""
    if [ -f "$ADMIN_ENV_FILE" ]; then
        u=$(sed -n 's/^ADMIN_USER=//p' "$ADMIN_ENV_FILE" | head -1)
        p=$(sed -n 's/^ADMIN_PASSWORD=//p' "$ADMIN_ENV_FILE" | head -1)
    fi
    # 环境变量 > 凭据文件 > 默认值；密码可能为空（调用方决定要不要现场生成）
    ADMIN_USER="${ADMIN_USER_ENV:-${u:-admin}}"
    ADMIN_PASSWORD="${ADMIN_PASS_ENV:-$p}"
}

save_admin_credentials() {
    umask 077
    {
        echo "# wxcode 部署凭据 —— 明文密码，请勿提交到仓库或同步到公共位置"
        echo "# 更新时间: $(date '+%F %T')"
        echo "ADMIN_USER=${ADMIN_USER}"
        echo "ADMIN_PASSWORD=${ADMIN_PASSWORD}"
        echo "HOST_PORT=${HOST_PORT}"
    } > "$ADMIN_ENV_FILE"
    chmod 600 "$ADMIN_ENV_FILE" 2>/dev/null || true
}

# 用「即将展示给用户的账号密码」真实调用一次登录接口：
# 0 = 能登录；1 = 认证失败（密码不一致）；2 = 其他（限流 / 网络）
# 只有 0 才把密码作为"已验证可用"打印，避免展示一个登不进去的密码。
verify_admin_login() {
    local url="http://127.0.0.1:${HOST_PORT}/api/login" code
    local body
    body=$(printf '{"username":"%s","password":"%s"}' \
        "$(printf '%s' "$ADMIN_USER" | sed 's/\\/\\\\/g; s/"/\\"/g')" \
        "$(printf '%s' "$ADMIN_PASSWORD" | sed 's/\\/\\\\/g; s/"/\\"/g')")
    code=$(curl -s -o /dev/null -w '%{http_code}' --max-time 5 \
        -X POST "$url" -H 'Content-Type: application/json' --data-binary "$body" || true)
    case "$code" in
        200) return 0 ;;
        429) return 2 ;;
        *)   return 1 ;;
    esac
}

print_admin_block() {
    local ok="$1"
    echo -e "  管理员账号: ${CYAN}${ADMIN_USER}${NC}"
    if [ "$ok" = "1" ]; then
        echo -e "  管理员密码: ${CYAN}${ADMIN_PASSWORD}${NC}"
        echo -e "  登录地址:   ${YELLOW}http://localhost:${HOST_PORT}/login${NC}"
    elif [ -z "$ADMIN_PASSWORD" ]; then
        echo -e "  管理员密码: ${YELLOW}未知（没有凭据文件，可查看 docker logs 或 --reset-admin）${NC}"
    else
        echo -e "  管理员密码: ${YELLOW}（与保存的凭据不一致，未能验证）${NC}"
    fi
    echo -e "  凭据文件:   ${ADMIN_ENV_FILE}（权限 600）"
    echo -e "  修改密码:   登录后到「我的」页修改，或执行 ${YELLOW}./deploy.sh --reset-admin${NC}"
}

print_admin_hint_on_fail() {
    echo ""
    warn "无法用当前凭据登录（说明数据卷里已存在管理员，但密码不是这个）"
    echo -e "  ${YELLOW}修复方式：${NC}"
    echo -e "    1) 重置为新密码：${YELLOW}ADMIN_PASSWORD=新密码 ./deploy.sh --reset-admin${NC}"
    echo -e "    2) 找回首次启动时打印的密码：${YELLOW}docker logs ${CONTAINER_NAME} 2>&1 | grep 管理员${NC}"
    echo -e "  本次使用的凭据仍写在 ${ADMIN_ENV_FILE}"
}

# ---------- 容器操作 ----------
remove_container() {
    if container_exists; then
        log "  停止旧容器..."
        docker stop "$CONTAINER_NAME" >/dev/null 2>&1 || true
        log "  移除旧容器..."
        docker rm "$CONTAINER_NAME" >/dev/null 2>&1 || true
    fi
}

run_container() {   # $1 = 镜像引用
    local image="$1"
    local run_env=(
        -e "PORT=${CONTAINER_PORT}"
        -e "TZ=Asia/Shanghai"
        -e "ALLOW_LOCAL_IMAGE=0"   # 默认禁止把 logo/url 当本地路径读；需要读服务器本地图片时改成 1
        -e "DATA_DIR=/app/data"
        -e "ADMIN_USER=${ADMIN_USER}"
        -e "ADMIN_PASSWORD=${ADMIN_PASSWORD}"
    )
    [ "$RESET_ADMIN" = "1" ] && run_env+=(-e "ADMIN_RESET_PASSWORD=1")

    log "  启动容器（镜像 $image）..."
    docker run -d \
        --name "$CONTAINER_NAME" \
        --restart=unless-stopped \
        -p "${HOST_PORT}:${CONTAINER_PORT}" \
        -v "${DATA_VOLUME}:/app/data" \
        --log-opt max-size=10m \
        --log-opt max-file=3 \
        "${run_env[@]}" \
        "$image" >/dev/null
}

# 轮询 /api/health，最多等 30 秒
wait_ready() {
    local i url="http://127.0.0.1:${HOST_PORT}${HEALTH_PATH}"
    for i in $(seq 1 30); do
        if curl -fsS --max-time 2 "$url" >/dev/null 2>&1; then
            log "  健康检查通过（${i}s）：$(curl -fsS --max-time 2 "$url")"
            return 0
        fi
        sleep 1
    done
    err "健康检查 30 秒未通过：$url"
    docker logs --tail 50 "$CONTAINER_NAME" 2>&1 | sed 's/^/    /' || true
    return 1
}

print_done() {
    local ok="$1"
    echo ""
    echo -e "${GREEN}============================================${NC}"
    echo -e "${GREEN}  部署成功${NC}"
    echo -e "${GREEN}============================================${NC}"
    echo -e "  访问地址: ${YELLOW}http://localhost:${HOST_PORT}/${NC}"
    echo -e "  接口文档: ${YELLOW}http://localhost:${HOST_PORT}/api/docs.md${NC}"
    echo -e "  健康检查: ${YELLOW}http://localhost:${HOST_PORT}${HEALTH_PATH}${NC}"
    echo -e "${GREEN}--------------------------------------------${NC}"
    print_admin_block "$ok"
    echo -e "${GREEN}--------------------------------------------${NC}"
    echo -e "  数据库卷: ${YELLOW}${DATA_VOLUME}${NC} → /app/data/wxcode.db"
    echo -e "  查看日志: ${YELLOW}docker logs -f ${CONTAINER_NAME}${NC}"
    echo -e "  服务状态: ${YELLOW}./deploy.sh --status${NC}"
    echo -e "  回滚上一版: ${YELLOW}./deploy.sh --rollback${NC}"
    echo -e "${GREEN}============================================${NC}"
}

# ---------- --status：只看现状 ----------
do_status() {
    check_env
    echo ""
    echo -e "${GREEN}=============== 葵码生成器 运行状态 ===============${NC}"
    if ! container_exists; then
        warn "容器 ${CONTAINER_NAME} 不存在（还没部署过？）"
        load_admin_credentials
        if [ -n "$ADMIN_PASSWORD" ]; then
            echo -e "  凭据文件中记录的账号: ${CYAN}${ADMIN_USER}${NC} / ${CYAN}${ADMIN_PASSWORD}${NC}"
        else
            echo -e "  还没有凭据文件；执行 ${YELLOW}./deploy.sh${NC} 首次部署后会自动生成并显示"
        fi
        echo -e "  凭据文件: ${ADMIN_ENV_FILE}"
        return
    fi
    local state image
    state=$(docker inspect -f '{{.State.Status}}' "$CONTAINER_NAME" 2>/dev/null || echo unknown)
    image=$(docker inspect -f '{{.Config.Image}}' "$CONTAINER_NAME" 2>/dev/null || echo unknown)
    echo -e "  容器状态: ${GREEN}${state}${NC}"
    echo -e "  运行镜像: ${image}"
    echo -e "  端口映射: ${YELLOW}${HOST_PORT} → ${CONTAINER_PORT}${NC}"
    echo -e "  数据卷:   ${DATA_VOLUME}"
    if curl -fsS --max-time 3 "http://127.0.0.1:${HOST_PORT}${HEALTH_PATH}" >/dev/null 2>&1; then
        echo -e "  健康检查: ${GREEN}正常${NC}"
    else
        echo -e "  健康检查: ${RED}不通${NC}（http://127.0.0.1:${HOST_PORT}${HEALTH_PATH}）"
    fi
    load_admin_credentials
    echo -e "${GREEN}------------------------------------------------${NC}"
    if [ -z "$ADMIN_PASSWORD" ]; then
        echo -e "  管理员账号: ${CYAN}${ADMIN_USER}${NC}"
        echo -e "  管理员密码: ${YELLOW}未知（凭据文件 ${ADMIN_ENV_FILE} 不存在）${NC}"
        echo -e "  重置密码:   ${YELLOW}ADMIN_PASSWORD=新密码 ./deploy.sh --reset-admin${NC}"
    else
        local rc=0
        verify_admin_login || rc=$?
        if [ "$rc" = "0" ]; then
            print_admin_block 1
        elif [ "$rc" = "2" ]; then
            echo -e "  管理员账号: ${CYAN}${ADMIN_USER}${NC}"
            echo -e "  管理员密码: ${CYAN}${ADMIN_PASSWORD}${NC}  ${YELLOW}（本次未验证：登录接口限流）${NC}"
            echo -e "  登录地址:   ${YELLOW}http://localhost:${HOST_PORT}/login${NC}"
        else
            print_admin_block 0
            print_admin_hint_on_fail
        fi
    fi
    echo -e "${GREEN}================================================${NC}"
}

# ---------- 构建镜像（基础镜像可换源）----------
build_once() {   # $1 = registry 前缀（空 = 用 daemon/Dockerfile 默认）  $2 = 日志文件
    local reg="$1" log="$2"
    local go_img="${GO_NAME}:${GO_TAG}" rt_img="${RUNTIME_NAME}:${RUNTIME_TAG}"
    if [ -n "$reg" ]; then
        go_img="${reg}/library/${GO_NAME}:${GO_TAG}"
        rt_img="${reg}/library/${RUNTIME_NAME}:${RUNTIME_TAG}"
    fi
    docker build \
        --build-arg "VERSION=${VERSION}" \
        --build-arg "GO_IMAGE=${go_img}" \
        --build-arg "RUNTIME_IMAGE=${rt_img}" \
        --build-arg "GOPROXY=${GOPROXY_ARG}" \
        -t "${IMAGE_NAME}:${VERSION}" \
        -t "${IMAGE_NAME}:latest" \
        "$SRC_DIR" 2>&1 | tee "$log"
    return "${PIPESTATUS[0]}"   # tee 会吞掉退出码，必须取 PIPESTATUS[0]
}

build_image() {
    local log idx=0 reg label ok=0
    log=$(mktemp)
    for reg in "${IMAGE_SOURCES[@]}"; do
        idx=$((idx + 1))
        if [ -z "$reg" ]; then label="默认来源（daemon.json 配的镜像站）"; else label="$reg"; fi
        if [ "$idx" -gt 1 ]; then warn "  [$idx/${#IMAGE_SOURCES[@]}] 换源重试：$label"; fi

        if build_once "$reg" "$log"; then
            if [ -n "$reg" ]; then
                log "  ✓ 构建完成（基础镜像来自 $reg）"
            else
                log "  ✓ 构建完成"
            fi
            ok=1
            break
        fi

        # 构建上下文缺文件（Dockerfile 的 COPY 找不到目录）：换源没用，直接报清楚
        if grep -qiE 'failed to calculate checksum|failed to compute cache key|COPY failed|not found in build context' "$log"; then
            rm -f "$log"
            err "构建上下文缺少源码目录（Dockerfile 里的 COPY 找不到文件），换镜像源没用"
            err "请确认仓库里包含 cmd/ internal/ web/，再重新部署"
            return 1
        fi

        # 只有"拉不到基础镜像"才换源；编译报错换源也没用，直接失败
        if ! grep -qiE "$PULL_ERR_RE" "$log"; then
            rm -f "$log"
            err "构建失败，且不是拉基础镜像的问题（见上方日志），不再换源重试"
            return 1
        fi
        warn "  [$idx/${#IMAGE_SOURCES[@]}] $label 拉不到基础镜像"
    done
    rm -f "$log"

    if [ "$ok" != "1" ]; then
        err "所有镜像源都拉不到基础镜像（${GO_NAME}:${GO_TAG} / ${RUNTIME_NAME}:${RUNTIME_TAG}）"
        err "可手动指定一个可用镜像站：BASE_REGISTRY=docker.m.daocloud.io $0"
        return 1
    fi
    return 0
}

# ---------- 手动回滚 ----------
do_rollback() {
    if ! docker image inspect "${IMAGE_NAME}:previous" >/dev/null 2>&1; then
        err "没有可回滚的镜像（${IMAGE_NAME}:previous 不存在），至少成功部署过一次才有"
        exit 1
    fi
    try_rollback || exit 1
    load_admin_credentials
    local ok=0
    if [ -n "$ADMIN_PASSWORD" ]; then
        local rc=0
        verify_admin_login || rc=$?
        if [ "$rc" = "0" ]; then ok=1; fi
    fi
    print_done "$ok"
}

# 部署失败时尽力把上一版拉回来（没有 previous 就明说，别假装成功）
try_rollback() {
    if ! docker image inspect "${IMAGE_NAME}:previous" >/dev/null 2>&1; then
        err "没有 ${IMAGE_NAME}:previous，无法自动回滚；请手动 docker logs 排查"
        return 1
    fi
    warn "回滚到 ${IMAGE_NAME}:previous ..."
    remove_container
    if run_container "${IMAGE_NAME}:previous" && wait_ready; then
        warn "已回滚到上一版（服务可用），请查清新版本问题后再部署"
        return 0
    fi
    err "回滚后仍未就绪，请检查日志：docker logs --tail 100 ${CONTAINER_NAME}"
    return 1
}

# ---------- 主流程 ----------
main() {
    check_env

    if [ "$STATUS_ONLY" = "1" ]; then
        do_status
        return
    fi

    echo ""
    echo -e "${GREEN}============================================${NC}"
    echo -e "${GREEN}  葵码生成器 — 一键部署${NC}"
    echo -e "${GREEN}============================================${NC}"
    echo ""

    if [ "$ROLLBACK" = "1" ]; then
        do_rollback
        return
    fi

    TMP_DIR=""
    cleanup_tmp() { [ -n "$TMP_DIR" ] && rm -rf "$TMP_DIR"; }
    trap cleanup_tmp EXIT

    # 0. 准备好管理员凭据（先把密码定下来，后面容器和终端展示都用同一份）
    load_admin_credentials
    if [ -z "$ADMIN_PASSWORD" ]; then
        ADMIN_PASSWORD=$(gen_password)
        log "  已生成管理员密码（保存到 ${ADMIN_ENV_FILE}）"
    fi
    save_admin_credentials
    log "[0/4] 管理员：${ADMIN_USER}（凭据保存在 ${ADMIN_ENV_FILE}）"
    [ "$RESET_ADMIN" = "1" ] && warn "  已指定 --reset-admin：启动时会把管理员密码重置为上面的值"

    # 1. 准备源码
    if [ "$LOCAL_MODE" = "1" ]; then
        SRC_DIR="$(cd "$(dirname "$0")" && pwd)"
        [ -f "$SRC_DIR/Dockerfile" ] || SRC_DIR="$(cd "$SRC_DIR/.." && pwd)"
        log "[1/4] 使用本地源码：$SRC_DIR"
    elif [ -n "$GIT_URL" ]; then
        command -v git >/dev/null 2>&1 || { err "指定了 GIT_URL，但环境里没有 git"; exit 1; }
        log "[1/4] git clone（${BRANCH}）：${GIT_URL}"
        TMP_DIR=$(mktemp -d)
        if ! git clone --depth 1 --branch "$BRANCH" "$GIT_URL" "$TMP_DIR/src" 2>&1 | sed 's/^/    /'; then
            err "克隆失败：${GIT_URL}"
            err "  * 分支 ${BRANCH} 是否存在？可换 BRANCH=... 或直接去掉该变量用仓库默认分支"
            err "  * 私有仓库请用带凭据的地址，或改用 GIT_TOKEN + 归档下载"
            exit 1
        fi
        SRC_DIR="$TMP_DIR/src"
        log "  ✓ $(basename "$SRC_DIR")"
    else
        SRC_ORDER="$(source_order)"
        # 探测结果直接转成人话，方便排查「为什么先走了另一条」
        SRC_LABEL=""
        for s in $SRC_ORDER; do
            case "$s" in
                gitee)  SRC_LABEL="${SRC_LABEL}${SRC_LABEL:+ → }Gitee(国内)" ;;
                github) SRC_LABEL="${SRC_LABEL}${SRC_LABEL:+ → }GitHub(国外)" ;;
            esac
        done
        log "[1/4] 获取源码（${BRANCH}），自动选路：${SRC_LABEL}"
        TMP_DIR=$(mktemp -d)
        CURL_AUTH=()
        if [ -n "${GIT_TOKEN:-}" ]; then
            # GitHub / Gitea 都是 basic auth：token 当密码，用户名随便填
            CURL_AUTH=(-u "${GIT_USER:-x-access-token}:${GIT_TOKEN}")
        fi

        SRC_URL=""
        CLONED=0
        while IFS= read -r url; do
            [ -n "$url" ] || continue
            log "  下载：$url"
            if curl -fsSL --connect-timeout 20 --max-time 300 "${CURL_AUTH[@]}" \
                    "$url" -o "$TMP_DIR/source.tar.gz"; then
                SRC_URL="$url"
                break
            fi
            warn "  不可用，换下一个地址"
        done <<EOF
$(candidate_urls)
EOF

        # 两条线路的归档都拿不到时，改用 git clone 再走一遍（对代理/限速更稳）
        if [ -z "$SRC_URL" ] && command -v git >/dev/null 2>&1; then
            warn "归档下载失败，改用 git clone 重试两条线路"
            while IFS= read -r line; do
                case "$line" in
                    gitee)  git_url="https://gitee.com/${GITEE_REPO}.git" ;;
                    github) git_url="https://github.com/${GITHUB_REPO}.git" ;;
                    *)      continue ;;
                esac
                log "  克隆：$git_url"
                if git clone --depth 1 --branch "$BRANCH" "$git_url" "$TMP_DIR/src" 2>/dev/null; then
                    SRC_URL="git clone $git_url"
                    CLONED=1
                    break
                fi
                rm -rf "$TMP_DIR/src"
            done <<EOF
$(printf '%s' "$SRC_ORDER")
EOF
        fi

        if [ -z "$SRC_URL" ]; then
            err "源码获取失败：两条线路都不可用"
            err "  * 线路 1（国内）：https://gitee.com/${GITEE_REPO}"
            err "  * 线路 2（国外）：https://github.com/${GITHUB_REPO}"
            err "  * 分支：${BRANCH}；私有仓库带 token：GIT_TOKEN=ghp_xxx"
            err "  * 也可以指定来源：CODE_URL=... / GIT_URL=... / 本地构建 --local"
            exit 1
        fi
        log "  ✓ 来源：${SRC_URL}"

        if [ "$CLONED" = "1" ]; then
            log "[2/4] 使用克隆下来的源码"
            SRC_DIR="$TMP_DIR/src"
        else
            log "[2/4] 解压..."
            # 先 cd 再解压（而不是 tar -C 绝对路径）：Git Bash 会把 /tmp/xxx 转成 C:/...
            # 传给 tar 时被当成「远程主机:文件」，Windows 上会直接报错。
            ( cd "$TMP_DIR" && tar -xzf source.tar.gz )
            # 用 -print -quit 取第一个目录：不能写 `find ... | head -1`，
            # head 提前关闭管道会让 find 收到 SIGPIPE，在 set -o pipefail 下直接判失败
            SRC_DIR=$(find "$TMP_DIR" -mindepth 1 -maxdepth 1 -type d -print -quit)
        fi
        [ -n "$SRC_DIR" ] || { err "未找到源码目录"; exit 1; }
        log "  ✓ $(basename "$SRC_DIR")"
    fi
    [ -f "$SRC_DIR/Dockerfile" ] || { err "源码目录里没有 Dockerfile：$SRC_DIR"; exit 1; }

    # 版本号：优先环境变量，其次源码 VERSION 文件，最后时间戳
    if [ -z "$VERSION" ]; then
        if [ -n "$VERSION_ENV" ]; then
            VERSION="$VERSION_ENV"
        elif [ -f "$SRC_DIR/VERSION" ]; then
            VERSION="$(tr -d '[:space:]' < "$SRC_DIR/VERSION")"
        fi
        [ -n "$VERSION" ] || VERSION="$(date +%Y%m%d-%H%M)"
    fi
    log "  版本号：${VERSION}"

    # 2. 记录旧镜像，便于回滚
    if docker image inspect "${IMAGE_NAME}:latest" >/dev/null 2>&1; then
        docker tag "${IMAGE_NAME}:latest" "${IMAGE_NAME}:previous"
        log "  已把当前镜像标记为 ${IMAGE_NAME}:previous"
    fi

    # 3. 构建（失败不动线上容器；基础镜像拉不到会自动换源重试）
    log "[3/4] 构建镜像 ${IMAGE_NAME}:${VERSION} ..."
    if ! build_image; then
        err "镜像构建失败，线上容器未改动"
        exit 1
    fi

    # 4. 替换容器
    log "[4/4] 替换容器..."
    remove_container
    if ! run_container "${IMAGE_NAME}:latest"; then
        err "新容器启动失败（端口 ${HOST_PORT} 是否被占用？）"
        try_rollback
        exit 1
    fi

    if ! wait_ready; then
        err "新容器健康检查未通过，尝试自动回滚..."
        try_rollback
        exit 1
    fi

    # 清理构建产生的悬空镜像（只删 <none>，不碰正在用的）
    docker image prune -f >/dev/null 2>&1 || true

    # 5. 用真实登录验证凭据，再打印（避免展示一个登不进去的密码）
    #    验证失败不影响部署结果（服务本身是好的），只提示怎么对齐密码
    local ok=0 rc=0
    verify_admin_login || rc=$?
    if [ "$rc" = "0" ]; then
        ok=1
    elif [ "$rc" = "1" ]; then
        print_admin_hint_on_fail
    else
        warn "登录验证被限流（429），密码暂未验证；稍后执行 ./deploy.sh --status 复查"
    fi
    print_done "$ok"
}

main
