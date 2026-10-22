package service

import (
	"lxdapi/internal/db"
	"lxdapi/internal/lxc"
	"lxdapi/models"
)

// ClientForNode 按节点选择 LXD client
// nodeID=0、节点不存在、Host 为空 → 本机 client；否则 → 远程 client
func ClientForNode(nodeID uint) *lxc.Client {
	if nodeID == 0 {
		return lxc.NewClient()
	}
	var node models.Node
	if err := db.DB.First(&node, nodeID).Error; err != nil || node.Host == "" {
		return lxc.NewClient()
	}
	return lxc.NewRemoteClient(node.Host, node.APIPort, node.APIHash)
}
