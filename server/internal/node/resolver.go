package node

import (
	"lxdapi/internal/db"
	"lxdapi/internal/lxc"
	"lxdapi/models"
)

// ClientForNode 根据节点 ID 返回对应客户端。
// nodeID==0 或节点 host 为空时返回本机默认客户端（本地 socket）。
// 远程节点返回经节点系统级 API 下发的远程客户端（见 lxc/remote.go）。
func ClientForNode(nodeID uint) *lxc.Client {
	if nodeID == 0 {
		return lxc.NewClient()
	}
	var n models.Node
	if err := db.DB.First(&n, nodeID).Error; err != nil || n.Host == "" {
		return lxc.NewClient()
	}
	return lxc.NewRemoteClient(n.Host, n.APIPort, n.APIHash)
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
	ep := lxc.NodeEndpoint(n.Host, n.APIPort)
	ep.APIHash = n.APIHash
	return ep
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

// ResolveNodeIDForContainer 先查 containers 表的 node_id，没有再回退订单。
func ResolveNodeIDForContainer(containerName string) uint {
	var ct models.Container
	if err := db.DB.Where("name = ?", containerName).First(&ct).Error; err == nil && ct.NodeID != 0 {
		return ct.NodeID
	}
	return NodeIDForContainer(containerName)
}

// ClientForContainer 按容器归属返回对应节点的客户端。
func ClientForContainer(containerName string) *lxc.Client {
	return ClientForNode(ResolveNodeIDForContainer(containerName))
}

// IsRemoteNode 判断节点是否为远程节点（登记了 host）。
func IsRemoteNode(nodeID uint) bool {
	if nodeID == 0 {
		return false
	}
	var n models.Node
	if err := db.DB.First(&n, nodeID).Error; err != nil {
		return false
	}
	return n.Host != ""
}
