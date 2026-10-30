package admin

import (
	"context"
	"encoding/hex"
	"time"

	"github.com/gin-gonic/gin"
	"lxdapi/internal/db"
	"lxdapi/internal/lxc"
	"lxdapi/models"
	"lxdapi/pkg/response"
)

// resolveImageAlias 把 64 位指纹形式的默认镜像解析成该节点模板的别名（可读名）；
// 解析不到（节点离线/模板未同步）则原样返回。节点列表展示与保存归一都走这里，
// 避免再出现默认镜像显示成一串指纹字母。
func resolveImageAlias(nodeID uint, image string) string {
	if len(image) != 64 {
		return image
	}
	if _, err := hex.DecodeString(image); err != nil {
		return image
	}
	var tpl models.Template
	if err := db.DB.Where("node_id = ? AND fingerprint = ?", nodeID, image).First(&tpl).Error; err == nil && tpl.Alias != "" {
		return tpl.Alias
	}
	return image
}

type nodeView struct {
	models.Node
	ImageDisplayName string `json:"image_display_name"`
}

func toNodeView(n models.Node) nodeView {
	return nodeView{Node: n, ImageDisplayName: resolveImageAlias(n.ID, n.Image)}
}

// GetNodes 节点列表
func GetNodes(c *gin.Context) {
	var nodes []models.Node
	if err := db.DB.Order("sort asc, id asc").Find(&nodes).Error; err != nil {
		response.Error(c, 500, "获取节点列表失败")
		return
	}
	views := make([]nodeView, 0, len(nodes))
	for _, n := range nodes {
		views = append(views, toNodeView(n))
	}
	response.Success(c, views)
}

// GetNode 单个节点
func GetNode(c *gin.Context) {
	var node models.Node
	if err := db.DB.First(&node, c.Param("id")).Error; err != nil {
		response.Error(c, 404, "节点不存在")
		return
	}
	response.Success(c, toNodeView(node))
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
	// 默认镜像若填的是指纹，归一成别名存（展示与开通都更可读）
	if alias := resolveImageAlias(node.ID, node.Image); alias != node.Image {
		node.Image = alias
		db.DB.Model(&node).Update("image", alias)
	}
	response.Success(c, toNodeView(node))
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
	node.Image = resolveImageAlias(node.ID, input.Image)
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
	response.Success(c, toNodeView(node))
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
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var err error
	if node.Host == "" {
		// 本机节点：用 lxc info 测试连通性
		err = lxc.NewClient().Info(ctx)
	} else {
		// 远程节点：探测节点系统级 API（地址+密钥都对才算在线）
		err = lxc.PingRemoteNode(ctx, node.Host, node.APIPort, node.APIHash)
	}
	if err != nil {
		db.DB.Model(&node).Update("status", "offline")
		response.Error(c, 400, "节点不可达: "+err.Error())
		return
	}
	db.DB.Model(&node).Update("status", "online")
	response.Success(c, gin.H{"status": "online"})
}

// GetNodeImages 获取指定节点的镜像列表
func GetNodeImages(c *gin.Context) {
	client, _, ok := nodeClientByID(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	images, err := client.ListImages(ctx)
	if err != nil {
		response.Error(c, 500, "获取镜像列表失败: "+err.Error())
		return
	}
	response.Success(c, images)
}
