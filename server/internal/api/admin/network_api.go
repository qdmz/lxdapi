package admin

import (
	"context"
	"net"
	"os/exec"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"lxdapi/pkg/response"
)

func GetNetworkNATStatus(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	result := gin.H{}

	v4Output, err := exec.CommandContext(ctx, "lxc", "network", "get", "lxdbr0", "ipv4.nat").Output()
	if err != nil {
		result["ipv4_nat"] = false
	} else {
		result["ipv4_nat"] = strings.TrimSpace(string(v4Output)) == "true"
	}

	v6Output, err := exec.CommandContext(ctx, "lxc", "network", "get", "lxdbr0", "ipv6.nat").Output()
	if err != nil {
		result["ipv6_nat"] = false
	} else {
		result["ipv6_nat"] = strings.TrimSpace(string(v6Output)) == "true"
	}

	response.Success(c, result)
}

func SetNetworkNATStatus(c *gin.Context) {
	var req struct {
		IPv4NAT *bool `json:"ipv4_nat"`
		IPv6NAT *bool `json:"ipv6_nat"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误: "+err.Error())
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if req.IPv4NAT != nil {
		value := "false"
		if *req.IPv4NAT {
			value = "true"
		}
		if err := exec.CommandContext(ctx, "lxc", "network", "set", "lxdbr0", "ipv4.nat", value).Run(); err != nil {
			response.Error(c, 500, "设置IPv4 NAT失败: "+err.Error())
			return
		}
	}

	if req.IPv6NAT != nil {
		value := "false"
		if *req.IPv6NAT {
			value = "true"
		}
		if err := exec.CommandContext(ctx, "lxc", "network", "set", "lxdbr0", "ipv6.nat", value).Run(); err != nil {
			response.Error(c, 500, "设置IPv6 NAT失败: "+err.Error())
			return
		}
	}

	response.Success(c, "设置成功")
}

// FixEgressNAT 一键修复容器出网：开启转发、放行 LXD 桥、补 MASQUERADE 并持久化
func FixEgressNAT(c *gin.Context) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	cidr := bridgeCIDR(ctx)
	if cidr == "" {
		response.Error(c, 500, "未找到 lxdbr0 的 IPv4 网段")
		return
	}

	actions := []string{}
	run := func(desc, shell string) {
		if err := exec.CommandContext(ctx, "sh", "-c", shell).Run(); err != nil {
			actions = append(actions, desc+"：失败("+err.Error()+")")
			return
		}
		actions = append(actions, desc+"：完成")
	}

	run("开启 ip_forward", "sysctl -w net.ipv4.ip_forward=1 >/dev/null 2>&1; echo 'net.ipv4.ip_forward=1' > /etc/sysctl.d/99-lxd-forward.conf")
	run("放行 LXD 桥转发", "if iptables -n -L DOCKER-USER >/dev/null 2>&1; then iptables -C DOCKER-USER -i lxdbr0 -j ACCEPT 2>/dev/null || iptables -I DOCKER-USER 1 -i lxdbr0 -j ACCEPT; else iptables -C FORWARD -i lxdbr0 -j ACCEPT 2>/dev/null || iptables -I FORWARD 1 -i lxdbr0 -j ACCEPT; fi")
	run("放行回程连接", "if iptables -n -L DOCKER-USER >/dev/null 2>&1; then iptables -C DOCKER-USER -o lxdbr0 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT 2>/dev/null || iptables -I DOCKER-USER 2 -o lxdbr0 -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT; fi")
	run("出站 MASQUERADE "+cidr, "iptables -t nat -C POSTROUTING -s "+cidr+" ! -o lxdbr0 -j MASQUERADE 2>/dev/null || iptables -t nat -A POSTROUTING -s "+cidr+" ! -o lxdbr0 -j MASQUERADE")

	persisted := false
	if err := exec.CommandContext(ctx, "sh", "-c", "command -v netfilter-persistent >/dev/null 2>&1 && netfilter-persistent save >/dev/null 2>&1").Run(); err == nil {
		persisted = true
	}

	response.Success(c, gin.H{"cidr": cidr, "actions": actions, "persisted": persisted})
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
