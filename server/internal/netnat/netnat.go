// Package netnat 封装宿主机侧的 LXD 桥网络 NAT 操作（lxdbr0 的 ipv4/ipv6 NAT
// 开关与出网一键修复）。逻辑原来写在管理端 handler 里、只能作用于本机；
// 抽到这里后，管理端（本机节点）与节点系统端点（远程节点在本机执行）共用同一份实现。
package netnat

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"strings"
)

// Status 读取 lxdbr0 的 ipv4.nat / ipv6.nat 当前值。
func Status(ctx context.Context) map[string]bool {
	result := map[string]bool{}
	v4Output, err := exec.CommandContext(ctx, "lxc", "network", "get", "lxdbr0", "ipv4.nat").Output()
	result["ipv4_nat"] = err == nil && strings.TrimSpace(string(v4Output)) == "true"
	v6Output, err := exec.CommandContext(ctx, "lxc", "network", "get", "lxdbr0", "ipv6.nat").Output()
	result["ipv6_nat"] = err == nil && strings.TrimSpace(string(v6Output)) == "true"
	return result
}

// Set 设置 lxdbr0 的 NAT 开关（nil 表示不改）。
func Set(ctx context.Context, ipv4NAT, ipv6NAT *bool) error {
	if ipv4NAT != nil {
		value := "false"
		if *ipv4NAT {
			value = "true"
		}
		if err := exec.CommandContext(ctx, "lxc", "network", "set", "lxdbr0", "ipv4.nat", value).Run(); err != nil {
			return fmt.Errorf("设置IPv4 NAT失败: %v", err)
		}
	}
	if ipv6NAT != nil {
		value := "false"
		if *ipv6NAT {
			value = "true"
		}
		if err := exec.CommandContext(ctx, "lxc", "network", "set", "lxdbr0", "ipv6.nat", value).Run(); err != nil {
			return fmt.Errorf("设置IPv6 NAT失败: %v", err)
		}
	}
	return nil
}

// FixEgress 一键修复容器出网：开启转发、放行 LXD 桥、补 MASQUERADE 并尝试持久化。
func FixEgress(ctx context.Context) (map[string]interface{}, error) {
	cidr := bridgeCIDR(ctx)
	if cidr == "" {
		return nil, fmt.Errorf("未找到 lxdbr0 的 IPv4 网段")
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

	return map[string]interface{}{"cidr": cidr, "actions": actions, "persisted": persisted}, nil
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
