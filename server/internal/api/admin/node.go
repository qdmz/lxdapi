package admin

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"lxdapi/internal/db"
	"lxdapi/internal/lxc"
	"lxdapi/models"
	"lxdapi/pkg/response"
)

// GetNodes 节点列表
func GetNodes(c *gin.Context) {
	var nodes []models.Node
	if err := db.DB.Order("sort asc, id asc").Find(&nodes).Error; err != nil {
		response.Error(c, 500, "获取节点列表失败")
		return
	}
	response.Success(c, nodes)
}

// GetNode 单个节点
func GetNode(c *gin.Context) {
	var node models.Node
	if err := db.DB.First(&node, c.Param("id")).Error; err != nil {
		response.Error(c, 404, "节点不存在")
		return
	}
	response.Success(c, node)
}

// CreateNode 创建节点
func CreateNode(c *gin.Context) {
	var node models.Node
	if err := c.ShouldBindJSON(&node); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	if node.Name == "" {
		response.Error(c, 400, "节点名称不能为空")
		return
	}
	if node.Status == "" {
		node.Status = "online"
	}
	// 机房归属：优先 region_id，否则按 region 文本自动建/取（空则归入「默认机房」）
	r, err := resolveNodeRegion(db.DB, node.RegionID, node.Region)
	if err != nil {
		response.Error(c, 400, err.Error())
		return
	}
	node.RegionID = r.ID
	node.Region = r.Name
	if err := db.DB.Create(&node).Error; err != nil {
		response.Error(c, 500, "创建节点失败")
		return
	}
	response.Success(c, node)
}

// UpdateNode 更新节点
func UpdateNode(c *gin.Context) {
	var node models.Node
	if err := db.DB.First(&node, c.Param("id")).Error; err != nil {
		response.Error(c, 404, "节点不存在")
		return
	}
	var input models.Node
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	node.Name = input.Name
	// 机房归属：优先 region_id，否则按 region 文本自动建/取
	r, err := resolveNodeRegion(db.DB, input.RegionID, input.Region)
	if err != nil {
		response.Error(c, 400, err.Error())
		return
	}
	node.Region = r.Name
	node.RegionID = r.ID
	node.Host = input.Host
	node.APIPort = input.APIPort
	node.APIHash = input.APIHash
	node.Image = input.Image
	node.StoragePool = input.StoragePool
	node.Network = input.Network
	node.Status = input.Status
	node.Sort = input.Sort
	node.Remark = input.Remark
	if err := db.DB.Save(&node).Error; err != nil {
		response.Error(c, 500, "更新节点失败")
		return
	}
	// 节点机房变化后，同步其下商品的 region_id
	if err := syncProductsRegionOfNode(db.DB, node.ID, node.RegionID); err != nil {
		response.Error(c, 500, "节点已更新，但商品机房同步失败")
		return
	}
	response.Success(c, node)
}

// DeleteNode 删除节点
func DeleteNode(c *gin.Context) {
	if err := db.DB.Delete(&models.Node{}, c.Param("id")).Error; err != nil {
		response.Error(c, 500, "删除节点失败")
		return
	}
	response.Success(c, nil)
}

// TestNode 测试节点连通性
// @Summary 测试节点
// @Tags Admin API - 节点
// @Router /api/admin/nodes/:id/test [post]
func TestNode(c *gin.Context) {
	var node models.Node
	if err := db.DB.First(&node, c.Param("id")).Error; err != nil {
		response.Error(c, 404, "节点不存在")
		return
	}
	// 用 lxc info 测试连通性（10秒超时）
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var client *lxc.Client
	if node.Host == "" {
		// 本机节点：用本地 socket
		client = lxc.NewClient()
	} else {
		client = lxc.NewRemoteClient(node.Host, node.APIPort)
	}
	if err := client.Info(ctx); err != nil {
		db.DB.Model(&node).Update("status", "offline")
		response.Error(c, 400, "节点不可达: "+err.Error())
		return
	}
	db.DB.Model(&node).Update("status", "online")
	response.Success(c, gin.H{"status": "online"})
}

// GetNodeImages 获取指定节点的镜像列表
func GetNodeImages(c *gin.Context) {
	var node models.Node
	if err := db.DB.First(&node, c.Param("id")).Error; err != nil {
		response.Error(c, 404, "节点不存在")
		return
	}
	var client *lxc.Client
	if node.Host == "" {
		client = lxc.NewClient()
	} else {
		client = lxc.NewRemoteClient(node.Host, node.APIPort)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	images, err := client.ListImages(ctx)
	if err != nil {
		response.Error(c, 500, "获取镜像列表失败: "+err.Error())
		return
	}
	response.Success(c, images)
}
