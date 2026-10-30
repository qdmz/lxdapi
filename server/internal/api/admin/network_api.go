package admin

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"lxdapi/internal/netnat"
	"lxdapi/internal/node"
	"lxdapi/pkg/response"
)

// 网络 NAT 相关接口按 node_id 路由：本机节点在本机执行（internal/netnat），
// 远程节点下发到该节点的系统端点执行——此前不分节点永远作用于主控本机，
// 在页面选了远程节点时看到的、改的都是主控自己的 lxdbr0。

func GetNetworkNATStatus(c *gin.Context) {
	nodeID := parseNodeIDQuery(c)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	if node.IsRemoteNode(nodeID) {
		var data map[string]bool
		if err := node.ClientForNode(nodeID).RemoteDoJSON(ctx, "GET", "/api/system/network/nat", nil, &data); err != nil {
			response.Error(c, 500, "读取节点NAT状态失败: "+err.Error())
			return
		}
		response.Success(c, data)
		return
	}

	response.Success(c, netnat.Status(ctx))
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

	nodeID := parseNodeIDQuery(c)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()

	if node.IsRemoteNode(nodeID) {
		if err := node.ClientForNode(nodeID).RemoteDoJSON(ctx, "POST", "/api/system/network/nat", req, nil); err != nil {
			response.Error(c, 500, "设置节点NAT失败: "+err.Error())
			return
		}
		response.Success(c, "设置成功")
		return
	}

	if err := netnat.Set(ctx, req.IPv4NAT, req.IPv6NAT); err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	response.Success(c, "设置成功")
}

// FixEgressNAT 一键修复容器出网：开启转发、放行 LXD 桥、补 MASQUERADE 并持久化
func FixEgressNAT(c *gin.Context) {
	nodeID := parseNodeIDQuery(c)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()

	if node.IsRemoteNode(nodeID) {
		var data map[string]interface{}
		if err := node.ClientForNode(nodeID).RemoteDoJSON(ctx, "POST", "/api/system/network/fix-egress", nil, &data); err != nil {
			response.Error(c, 500, "节点出网修复失败: "+err.Error())
			return
		}
		response.Success(c, data)
		return
	}

	result, err := netnat.FixEgress(ctx)
	if err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	response.Success(c, result)
}
