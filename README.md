# LXD 容器管理面板（lxdapi）

基于 **LXD** 的容器管理面板：Go 后端 + 纯静态前端，支持管理后台、用户后台、容器自助面板三级体系，内置 Web 终端（WebSocket）。

- 仓库：https://github.com/qdmz/lxdapi
- 当前版本：**v0.01**

---

## 一、架构与端口

```
浏览器 ──► nginx (80 / 443 / 8080)
              ├─ 静态前端  /opt/lxdpanel
              ├─ /api/     ──► Go 后端 127.0.0.1:8081
              ├─ /ws/      ──► Go 后端（WebSocket 控制台）
              └─ /admin /user /container /swagger ──► Go 后端
                                    │
                                    └─► lxc 命令 / LXD unix socket ──► 容器
```

| 端口 | 用途 | 说明 |
|---|---|---|
| 80 / 443 | 前端入口（生产） | 443 默认自签证书，可替换 |
| 8080 | 前端入口（备用） | 内网/隧道访问常用 |
| 8081 | Go 后端 API | 不建议直接暴露公网 |

### 三个登录入口

| 入口 | 地址 | 账号 |
|---|---|---|
| 管理后台 | `/admin/login` | 配置里的 `admin.username` / `admin.password` |
| 用户后台 | `/user/login` | 用户名 + **该用户的 API Key**（用户登录密码即 API Key） |
| 容器面板 | `/login.html?hash=xxx` | 容器访问码（hash）+ 验证码 |

---

## 二、目录结构

```
server/   Go 后端源码（cmd / internal / models / pkg / plugins）
web/      静态前端（login.html、dashboard.html、console.html、js、css）
deploy/
  nginx/              lxdpanel.conf、lxdpanel-common.inc、00-xfp-map.conf
  systemd/            lxdapi.service
  config.example.yaml 脱敏配置样例
```

---

## 三、部署步骤（Debian / Ubuntu）

### 1. 安装 LXD 与依赖

```bash
apt update
apt install -y snapd nginx debootstrap rsync curl
snap install lxd
lxd init --auto        # 自动初始化存储池 default、网桥 lxdbr0
lxc storage list
lxc network list
```

### 2. 制作 Debian 12 基础镜像

```bash
debootstrap --arch=amd64 bookworm /tmp/rootfs http://mirrors.aliyun.com/debian/
tar -C /tmp/rootfs -czf /tmp/debian12.tar.gz .
lxc image import /tmp/debian12.tar.gz --alias debian-12
```

> **踩坑重点**：debootstrap 出来的 rootfs 是"裸系统"，容器内没有 `ip` 命令、没有 DHCP 客户端、没有任何网络配置，启动后**拿不到内网 IP**，面板会显示空白。
>
> 解决：先起一台容器写入网络配置，再固化成新镜像：
>
> ```bash
> lxc launch debian-12 tmp-ct
> lxc config set tmp-ct security.nesting true     # 关键：否则 systemd-networkd 报 226/NAMESPACE 起不来
> lxc exec tmp-ct -- sh -c "mkdir -p /etc/systemd/network && \
>   printf '[Match]\nName=eth0\n\n[Network]\nDHCP=yes\n' > /etc/systemd/network/80-eth0.network && \
>   systemctl enable --now systemd-networkd"
> sleep 5
> lxc list tmp-ct                                  # 此时应能看到 eth0 的 IPv4
> lxc publish tmp-ct --alias debian-12-net --force # 固化成带网络的镜像
> ```
>
> 之后创建 / 重装容器请选用 **`debian-12-net`**，否则新容器依然没有 IP。

### 3. 编译后端

```bash
cd server
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o lxdapi ./cmd/lxdapi
# ARM64 机器改用 GOARCH=arm64
```

### 4. 安装到 /opt/lxdapi

```bash
mkdir -p /opt/lxdapi/configs
cp lxdapi /opt/lxdapi/
cp configs/config.yaml /opt/lxdapi/configs/    # 或用 deploy/config.example.yaml 修改后放入
```

必须修改的配置项：

| 配置项 | 说明 |
|---|---|
| `admin.password` | 管理后台密码 |
| `admin.session_secret` | 会话密钥，改成随机串 |
| `security.api_hash` | 系统级 API 密钥（`X-API-Hash` 头） |
| `lxc.socket` | LXD socket 路径，snap 安装为 `/var/snap/lxd/common/lxd/unix.socket` |

### 5. systemd 服务

```bash
cp deploy/systemd/lxdapi.service /etc/systemd/system/
systemctl daemon-reload
systemctl enable --now lxdapi
systemctl status lxdapi
```

### 6. nginx

```bash
cp deploy/nginx/*.conf deploy/nginx/*.inc /etc/nginx/conf.d/
mkdir -p /etc/nginx/ssl
openssl req -x509 -newkey rsa:2048 -nodes -days 3650 \
  -keyout /etc/nginx/ssl/lxdpanel.key -out /etc/nginx/ssl/lxdpanel.crt \
  -subj "/CN=你的域名或IP"
nginx -t && systemctl reload nginx
```

`lxdpanel-common.inc` 内含 `/api/`、`/ws/`、`/admin`、`/user`、`/swagger` 反代规则，WebSocket 超时已设 3600s。
若上游有 CDN 且 HTTP 回源，`00-xfp-map.conf` 会优先取 `X-Forwarded-Proto`，保证后端识别用户实际协议。

### 7. 部署前端

```bash
mkdir -p /opt/lxdpanel
cp -r web/* /opt/lxdpanel/
```

### 8. 验证

```bash
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1/login.html             # 200
curl -s -o /dev/null -w "%{http_code}\n" http://127.0.0.1/api/container/captcha  # 200
curl -s http://127.0.0.1/api/public/brand-settings                               # JSON
```

---

## 四、鉴权方式（API）

| 中间件 | 适用路由 | 认证方式 |
|---|---|---|
| `AdminAuth` | `/api/admin/*` | session cookie（先登录 `/admin/login`） |
| `UserAuthOrBasic` | `/api/user/*` | session cookie，或请求头 `X-User-Username` + `X-User-Password`（值为用户 API Key） |
| `ContainerAuth` | `/api/container/*` | 请求头 `X-Container-Hash`（容器访问码） |
| `SystemAuth` | `/api/system/*` | 请求头 `X-API-Hash` |

示例（用 API Key 免登录调用用户接口）：

```bash
curl -H "X-User-Username: test" -H "X-User-Password: <api_key>" \
  http://127.0.0.1:8081/api/user/containers
```

---

## 五、接入 CDN / 域名

- **回源协议选 HTTP**（源站 443 是自签证书，HTTPS 回源会校验失败）
- 回源端口填映射到源站 80 的端口，回源 HOST 用加速域名
- **不缓存路径**：`/api/*`、`/admin/*`、`/user/*`、`/container/*`、`/swagger/*`、`/console`、`/ws/*`
- **必须开启 WebSocket 支持**，否则 `/ws/console` 终端连不上
- HTTP→HTTPS 跳转放在 CDN 侧开启，不要在源站 nginx 做强跳

---

## 六、常见问题

| 现象 | 原因 | 处理 |
|---|---|---|
| 容器读不到内网 IP | 镜像无网络配置 / 未开 nesting | 见"制作基础镜像"：用 `debian-12-net` 镜像 + `security.nesting=true` |
| 验证码"获取失败" | 接口返回平铺结构 `{code, captcha_id, image}`，前端误读 `res.data` | 前端已兼容两种结构（`web/js/login.js`） |
| 提示"验证码已过期" | 验证码绑定 session cookie | 取码与提交必须在**同一地址**完成，别中途换 IP/端口/域名 |
| 重装系统下拉显示指纹 | 模板接口返回 `alias/os/release`，前端原读 `t.name` | 已修复，显示 `debian-12 · debian bookworm` |
| Web 终端一会就断 | 反向代理/网关空闲超时太短 | nginx 已设 3600s，网关侧同步放宽 |
| 域名提示"机房域名过白" | 域名未备案或 CDN 站点被拦截 | 换"不含中国大陆"加速区域或联系机房过白；期间用 IP:端口 访问 |

---

## 七、安全提示

- 部署后**务必修改** `admin.password`、`session_secret`、`api_hash`。
- 用户后台登录密码即该用户的 **API Key**，可在管理后台用户详情重新生成。
- 不要把真实 `config.yaml`、数据库文件（`*.db`）提交到仓库（已在 `.gitignore` 中排除）。

---

## 八、版本

- **v0.01** — 首个归档版本：后端 + 前端 + 部署配置，含容器管理、用户与配额、端口映射、Web 终端、重装系统、模板管理。
