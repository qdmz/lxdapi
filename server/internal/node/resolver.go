package node

import (
	"lxdapi/internal/db"
	"lxdapi/internal/lxc"
	"lxdapi/models"
)

// ClientForNode 根据节点 ID 返回对应 LXD 客户端。
// nodeID==0 或节点 host 为空时返回本机默认客户端（本地 socket）。
// 远程节点通过 LXD_HOST / LXD_PORT 环境变量按次路由到对应 LXD API。
func ClientForNode(nodeID uint) *lxc.Client {
	if nodeID == 0 {
		return lxc.NewClient()
	}
	var n models.Node
	if err := db.DB.First(&n, nodeID).Error; err != nil || n.Host == "" {
		return lxc.NewClient()
	}
	return lxc.NewRemoteClient(n.Host, n.APIPort)
}

// EndpointForNode 返回节点对应的 LXD 接入端点。nodeID==0 或 host 为空为本机。
func EndpointForNode(nodeID uint) lxc.Endpoint {
	if nodeID == 0 {
		return lxc.Endpoint{}
	}
	var n models.Node
	if err := db.DB.First(&n, nodeID).Error; err != nil || n.Host == "" {
		return lxc.Endpoint{}
	}
	return lxc.NodeEndpoint(n.Host, n.APIPort)
}

// NodeByName 按容器名归属解析节点：容器名通过订单定位到节点。
// 找不到关联时返回 0（本机）。
func NodeIDForContainer(containerName string) uint {
	var o models.Order
	if err := db.DB.Where("container_name = ?", containerName).Order("created_at DESC").First(&o).Error; err == nil {
		return o.NodeID
	}
	return 0
}
