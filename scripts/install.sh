#!/usr/bin/env bash
# lxdapi 一键部署脚本
# 用法:
#   curl -fsSL https://raw.githubusercontent.com/qdmz/lxdapi/main/scripts/install.sh | bash
#   bash install.sh --local /path/to/repo      # 用本地源码部署
#   bash install.sh --domain panel.example.com # 指定域名（写入 nginx server_name）
#   bash install.sh --skip-lxd                 # 已装好 LXD 时跳过
#   bash install.sh --no-image                 # 不制作 debian-12-net 镜像
set -euo pipefail

REPO="qdmz/lxdapi"
BRANCH="${BRANCH:-main}"
RELEASE_TAG="${RELEASE_TAG:-v0.02}"
INSTALL_DIR="/opt/lxdapi"
PANEL_DIR="/opt/lxdpanel"
SRC_DIR=""
DOMAIN=""
SKIP_LXD=0
MAKE_IMAGE=1

log()  { printf "\033[32m[INFO]\033[0m %s\n" "$*"; }
warn() { printf "\033[33m[WARN]\033[0m %s\n" "$*"; }
err()  { printf "\033[31m[ERR ]\033[0m %s\n" "$*"; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --local)     SRC_DIR="$2"; shift 2;;
    --domain)    DOMAIN="$2"; shift 2;;
    --branch)    BRANCH="$2"; shift 2;;
    --tag)       RELEASE_TAG="$2"; shift 2;;
    --skip-lxd)  SKIP_LXD=1; shift;;
    --no-image)  MAKE_IMAGE=0; shift;;
    -h|--help)   sed -n "2,12p" "$0"; exit 0;;
    *) err "未知参数: $1（用 --help 查看用法）";;
  esac
done

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64) GOARCH=amd64; BIN_NAME=lxdapi-linux-amd64;;
  aarch64) GOARCH=arm64; BIN_NAME=lxdapi-linux-arm64;;
  *) err "不支持的架构: $ARCH";;
esac

# ---------- 1. 环境检查 ----------
[ "$(id -u)" = "0" ] || err "请用 root 用户执行本脚本"
if [ -f /etc/os-release ]; then
  . /etc/os-release
  case "$ID" in
    debian|ubuntu) log "系统: $PRETTY_NAME";;
    *) warn "未适配的系统: $ID，脚本将继续但可能失败";;
  esac
fi
log "架构: $ARCH (GOARCH=$GOARCH)"

# ---------- 2. 依赖 ----------
log "安装依赖..."
export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq curl wget tar openssl python3 nginx debootstrap snapd iptables-persistent git >/dev/null 2>&1 || warn "部分依赖安装失败，继续尝试"

# ---------- 3. LXD ----------
if [ "$SKIP_LXD" = "1" ]; then
  log "跳过 LXD 安装（--skip-lxd）"
else
  if ! command -v lxc >/dev/null 2>&1; then
    log "安装 LXD (snap)..."
    snap install lxd >/dev/null 2>&1 || err "LXD 安装失败，请手动执行: snap install lxd"
  fi
  if ! lxc storage list 2>/dev/null | grep -q "default"; then
    log "初始化 LXD..."
    lxd init --auto >/dev/null 2>&1 || lxd init || warn "LXD 初始化未完成，请手动执行 lxd init"
  else
    log "LXD 已初始化"
  fi
fi

# ---------- 4. 获取源码 ----------
WORK="/tmp/lxdapi-install"
rm -rf "$WORK"; mkdir -p "$WORK"
if [ -n "$SRC_DIR" ]; then
  [ -d "$SRC_DIR" ] || err "源码目录不存在: $SRC_DIR"
  log "使用本地源码: $SRC_DIR"
  cp -a "$SRC_DIR"/. "$WORK/src/"
else
  log "拉取源码 $REPO@$BRANCH..."
  if command -v git >/dev/null 2>&1; then
    git clone --depth 1 -b "$BRANCH" "https://github.com/$REPO.git" "$WORK/src" >/dev/null 2>&1 \
      || { tarball="https://codeload.github.com/$REPO/tar.gz/refs/heads/$BRANCH"; mkdir -p "$WORK/src"; curl -fsSL "$tarball" | tar xz -C "$WORK/src" --strip-components 1; }
  else
    mkdir -p "$WORK/src"
    curl -fsSL "https://codeload.github.com/$REPO/tar.gz/refs/heads/$BRANCH" | tar xz -C "$WORK/src" --strip-components 1
  fi
fi
[ -d "$WORK/src/server" ] || err "源码结构异常：缺少 server/ 目录"

# ---------- 5. 获取二进制 ----------
BIN="$WORK/lxdapi"
log "获取后端二进制..."
if curl -fsSL -o "$BIN" "https://github.com/$REPO/releases/download/$RELEASE_TAG/$BIN_NAME" 2>/dev/null; then
  log "已下载 Release 二进制 $RELEASE_TAG/$BIN_NAME"
else
  warn "Release 下载失败，尝试从源码编译"
  if command -v go >/dev/null 2>&1; then
    (cd "$WORK/src/server" && GOOS=linux GOARCH="$GOARCH" CGO_ENABLED=0 go build -o "$BIN" ./cmd/lxdapi) \
      || err "编译失败，请先安装 Go 或手动下载二进制到 $BIN"
    log "编译完成"
  else
    err "既无 Release 二进制也无 Go 环境。请安装 Go 后重试，或手动下载二进制放到 $BIN 再重跑脚本"
  fi
fi
chmod +x "$BIN"

# ---------- 6. 安装后端 ----------
log "安装后端到 $INSTALL_DIR..."
mkdir -p "$INSTALL_DIR/configs"
systemctl stop lxdapi >/dev/null 2>&1 || true
if [ -f "$INSTALL_DIR/lxdapi" ]; then
  cp "$INSTALL_DIR/lxdapi" "$INSTALL_DIR/lxdapi.bak.$(date +%s)" 2>/dev/null || true
fi
install -m 0755 "$BIN" "$INSTALL_DIR/lxdapi"

GEN_PWD=""; GEN_HASH=""; GEN_SECRET=""
if [ ! -f "$INSTALL_DIR/configs/config.yaml" ]; then
  log "生成初始配置..."
  cp "$WORK/src/deploy/config.example.yaml" "$INSTALL_DIR/configs/config.yaml"
  GEN_SECRET="$(openssl rand -hex 24)"
  GEN_HASH="$(openssl rand -hex 16)"
  GEN_PWD="$(openssl rand -base64 9 | tr -d '/+=' )"
  sed -i "s/CHANGE_ME_RANDOM_SESSION_SECRET/$GEN_SECRET/g; s/CHANGE_ME_API_HASH/$GEN_HASH/g; s/CHANGE_ME_ADMIN_PASSWORD/$GEN_PWD/g" "$INSTALL_DIR/configs/config.yaml"
else
  log "已存在 config.yaml，保留不覆盖"
fi

# ---------- 7. systemd ----------
log "配置 systemd 服务..."
cp "$WORK/src/deploy/systemd/lxdapi.service" /etc/systemd/system/lxdapi.service
systemctl daemon-reload
systemctl enable lxdapi >/dev/null 2>&1 || true

# ---------- 8. nginx ----------
log "配置 nginx..."
cp "$WORK/src"/deploy/nginx/*.conf "$WORK/src"/deploy/nginx/*.inc /etc/nginx/conf.d/ 2>/dev/null || warn "nginx 配置复制失败"
rm -f /etc/nginx/sites-enabled/default 2>/dev/null || true
mkdir -p /etc/nginx/ssl
if [ ! -f /etc/nginx/ssl/lxdpanel.crt ]; then
  CN="${DOMAIN:-$(hostname -I 2>/dev/null | awk '{print $1}')}"
  openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \
    -keyout /etc/nginx/ssl/lxdpanel.key -out /etc/nginx/ssl/lxdpanel.crt \
    -subj "/CN=${CN}" >/dev/null 2>&1 || warn "自签证书生成失败"
fi
if [ -n "$DOMAIN" ]; then
  sed -i "s/server_name _;/server_name $DOMAIN;/g" /etc/nginx/conf.d/lxdpanel-common.inc 2>/dev/null || true
fi
nginx -t >/dev/null 2>&1 && log "nginx 配置校验通过" || warn "nginx 配置校验失败，请检查 /etc/nginx/conf.d/"

# ---------- 9. 前端 ----------
log "部署前端到 $PANEL_DIR..."
mkdir -p "$PANEL_DIR"
cp -a "$WORK/src"/web/. "$PANEL_DIR/"

# ---------- 10. 容器出网规则（Docker 共存时必需）----------
if iptables -n -L DOCKER-USER >/dev/null 2>&1; then
  log "检测到 Docker，放行 LXD 网桥转发..."
  iptables -C DOCKER-USER -i lxdbr0 -j ACCEPT 2>/dev/null || iptables -I DOCKER-USER 1 -i lxdbr0 -j ACCEPT
  iptables -C DOCKER-USER -o lxdbr0 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT 2>/dev/null || iptables -I DOCKER-USER 2 -o lxdbr0 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT
  CIDR="$(ip -4 -o addr show lxdbr0 2>/dev/null | awk '{print $4}' | head -1)"
  if [ -n "$CIDR" ]; then
    NET="$(python3 -c "import ipaddress,sys; print(ipaddress.ip_network(sys.argv[1], strict=False))" "$CIDR" 2>/dev/null || echo "")"
    if [ -n "$NET" ]; then
      iptables -t nat -C POSTROUTING -s "$NET" ! -o lxdbr0 -j MASQUERADE 2>/dev/null || iptables -t nat -A POSTROUTING -s "$NET" ! -o lxdbr0 -j MASQUERADE
    fi
  fi
  echo "net.ipv4.ip_forward=1" > /etc/sysctl.d/99-lxd-forward.conf
  sysctl -w net.ipv4.ip_forward=1 >/dev/null 2>&1 || true
  netfilter-persistent save >/dev/null 2>&1 || true
fi

# ---------- 11. 制作基础镜像（可选）----------
if [ "$MAKE_IMAGE" = "1" ] && command -v lxc >/dev/null 2>&1; then
  if ! lxc image list --format csv -c l 2>/dev/null | grep -qx "debian-12-net"; then
    log "制作 debian-12-net 基础镜像（约 1-3 分钟）..."
    if debootstrap --arch="$GOARCH" bookworm /tmp/rootfs http://mirrors.aliyun.com/debian/ >/dev/null 2>&1; then
      tar -C /tmp/rootfs -czf /tmp/debian12.tar.gz . 2>/dev/null
      lxc image import /tmp/debian12.tar.gz --alias debian-12 >/dev/null 2>&1
      if lxc image list --format csv -c l | grep -qx "debian-12"; then
        lxc launch debian-12 tmp-mkimg >/dev/null 2>&1 && sleep 5
        lxc config set tmp-mkimg security.nesting true >/dev/null 2>&1 || true
        lxc exec tmp-mkimg -- sh -c 'mkdir -p /etc/systemd/network; printf "[Match]\nName=eth0\n\n[Network]\nDHCP=yes\n" > /etc/systemd/network/80-eth0.network; systemctl enable --now systemd-networkd' >/dev/null 2>&1 || true
        sleep 5
        lxc publish tmp-mkimg --alias debian-12-net --force >/dev/null 2>&1 && log "镜像 debian-12-net 已生成" || warn "镜像固化失败，请手动执行 lxc publish"
        lxc delete tmp-mkimg --force >/dev/null 2>&1 || true
      fi
    else
      warn "debootstrap 失败，跳过镜像制作（可手动制作或用 --no-image 跳过）"
    fi
  else
    log "镜像 debian-12-net 已存在"
  fi
fi

# ---------- 12. 启动 ----------
log "启动服务..."
systemctl restart lxdapi
systemctl reload nginx >/dev/null 2>&1 || systemctl restart nginx >/dev/null 2>&1 || true
sleep 4

# ---------- 13. 健康检查与输出 ----------
IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
CODE="$(curl -s -m 8 -o /dev/null -w '%{http_code}' http://127.0.0.1/login.html || echo 000)"
echo
echo "================= 部署结果 ================="
echo " 服务状态 : $(systemctl is-active lxdapi 2>/dev/null)"
echo " 本地自检 : /login.html -> HTTP $CODE"
echo " 后端端口 : 8081   前端端口: 80 / 443 / 8080"
echo " 访问地址 : http://${DOMAIN:-$IP}/login.html?hash=<容器访问码>"
echo " 管理后台 : http://${DOMAIN:-$IP}/admin/login"
echo " 用户后台 : http://${DOMAIN:-$IP}/user/login"
if [ -n "$GEN_PWD" ]; then
  echo " 初始管理员密码: $GEN_PWD   (配置文件: $INSTALL_DIR/configs/config.yaml)"
  echo " >>> 请登录后立即修改密码 <<<"
fi
echo "==========================================="
[ "$CODE" = "200" ] || warn "自检未返回 200，请检查: journalctl -u lxdapi -n 50"
