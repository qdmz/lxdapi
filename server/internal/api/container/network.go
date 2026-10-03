package container

import (
	"context"
	"net"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lxdapi/pkg/response"
)

const containerProbeScript = `
IP=$(ip -4 -o addr show eth0 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | head -1)
GW=$(ip r 2>/dev/null | awk '/default/{print $3; exit}')
echo "IP=$IP"
echo "GW=$GW"
if [ -n "$GW" ] && ping -c1 -W2 "$GW" >/dev/null 2>&1; then echo "GW_PING=ok"; else echo "GW_PING=fail"; fi
if getent hosts www.baidu.com >/dev/null 2>&1; then echo "DNS=ok"; else echo "DNS=fail"; fi
if timeout 5 bash -c "cat </dev/null >/dev/tcp/223.5.5.5/53" 2>/dev/null; then echo "TCP=ok"; else echo "TCP=fail"; fi
if command -v curl >/dev/null 2>&1; then
  echo "HTTP=$(curl -s -m 8 -o /dev/null -w '%{http_code}' http://www.baidu.com 2>/dev/null)"
  EG=$(curl -s -m 6 https://api.ipify.org 2>/dev/null | grep -Eo '([0-9]{1,3}\.){3}[0-9]{1,3}' | head -1)
  [ -z "$EG" ]; EG=$(curl -s -m 6 http://myip.ipip.net 2>/dev/null | grep -Eo '([0-9]{1,3}\.){3}[0-9]{1,3}' | head -1)
  echo "EGRESS=$EG"
else
  echo "HTTP=na"
  echo "EGRESS=na"
fi
`

// NetworkCheck 容器出网自检：容器内探测 + 宿主 NAT/转发规则检查
func NetworkCheck(c *gin.Context) {
	nameVal, _ := c.Get("container_name")
	name, _ := nameVal.(string)
	if name == "" {
		response.Error(c, 400, "容器名缺失")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	// 容器运行状态
	running := false
	if out, err := exec.CommandContext(ctx, "lxc", "list", name, "--format", "csv", "-c", "s").Output(); err == nil {
		running = strings.EqualFold(strings.TrimSpace(string(out)), "RUNNING")
	}

	result := gin.H{
		"container": name,
		"running":   running,
	}
	if !running {
		result["status"] = "stopped"
		result["hint"] = "容器未运行，无法检测出网"
		fillHostEgress(ctx, result)
		response.Success(c, result)
		return
	}

	out, err := exec.CommandContext(ctx, "lxc", "exec", name, "--", "sh", "-c", containerProbeScript).Output()
	if err != nil {
		result["status"] = "error"
		result["hint"] = "容器内探测命令执行失败: " + err.Error()
		fillHostEgress(ctx, result)
		response.Success(c, result)
		return
	}

	kv := make(map[string]string)
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if idx := strings.Index(line, "="); idx > 0 {
			kv[strings.TrimSpace(line[:idx])] = strings.TrimSpace(line[idx+1:])
		}
	}

	result["container_ip"] = kv["IP"]
	result["gateway"] = kv["GW"]
	result["gw_ping"] = kv["GW_PING"]
	result["dns"] = kv["DNS"]
	result["tcp_out"] = kv["TCP"]
	result["http_code"] = kv["HTTP"]
	result["egress_ip"] = kv["EGRESS"]

	status := "blocked"
	hint := "容器能到达网关但无法访问公网，通常是宿主 FORWARD 被 Docker 设为 DROP 或缺少 MASQUERADE 规则，请联系管理员点『一键修复』。"
	switch {
	case kv["HTTP"] == "200", kv["TCP"] == "ok":
		status = "ok"
		hint = "出网正常"
	case kv["GW_PING"] != "ok":
		status = "no_gateway"
		hint = "容器连网关都不通：检查容器是否拿到 IP（镜像需带网络配置 + security.nesting=true），或 LXD 网桥状态。"
	}
	result["status"] = status
	result["hint"] = hint

	fillHostEgress(ctx, result)
	response.Success(c, result)
}

// fillHostEgress 补充宿主侧的出网规则信息
func fillHostEgress(ctx context.Context, result gin.H) {
	cidr := bridgeCIDR(ctx)
	result["bridge_cidr"] = cidr
	result["masquerade"] = false
	if cidr != "" {
		if out, err := exec.CommandContext(ctx, "iptables", "-t", "nat", "-S", "POSTROUTING").Output(); err == nil {
			result["masquerade"] = strings.Contains(string(out), "-s "+cidr+" ")
		}
	}
	if out, err := exec.CommandContext(ctx, "iptables", "-S", "FORWARD").Output(); err == nil {
		for _, line := range strings.Split(string(out), "\n") {
			if strings.HasPrefix(line, "-P FORWARD ") {
				result["forward_policy"] = strings.TrimSpace(strings.TrimPrefix(line, "-P FORWARD "))
				break
			}
		}
	}
	if b, err := os.ReadFile("/proc/sys/net/ipv4/ip_forward"); err == nil {
		result["ip_forward"] = strings.TrimSpace(string(b))
	}
}

// bridgeCIDR 取 lxdbr0 的 IPv4 网段，如 10.120.105.0/24
func bridgeCIDR(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "ip", "-4", "-o", "addr", "show", "lxdbr0").Output()
	if err != nil {
		return ""
	}
	for _, f := range strings.Fields(string(out)) {
		if strings.Contains(f, "/") {
			if _, ipnet, err := net.ParseCIDR(f); err == nil {
				return ipnet.String()
			}
		}
	}
	return ""
}
