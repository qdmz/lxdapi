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

// nodeClientByID 按节点记录构造 LXD 客户端：本机节点（host 为空）走本地 socket，
// 远程节点经节点系统级 API 下发（镜像/存储操作在节点本机执行）。
func nodeClientByID(c *gin.Context) (*lxc.Client, *models.Node, bool) {
	var node models.Node
	if err := db.DB.First(&node, c.Param("id")).Error; err != nil {
		response.Error(c, 404, "节点不存在")
		return nil, nil, false
	}
	if node.Host == "" {
		return lxc.NewClient(), &node, true
	}
	return lxc.NewRemoteClient(node.Host, node.APIPort, node.APIHash), &node, true
}

// DeleteNodeImage 删除指定节点的镜像
func DeleteNodeImage(c *gin.Context) {
	client, _, ok := nodeClientByID(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if err := client.DeleteImage(ctx, c.Param("fingerprint")); err != nil {
		response.Error(c, 500, "删除镜像失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"message": "镜像已删除"})
}

// CopyNodeImage 下载镜像到指定节点（从远程镜像仓库拷贝）
func CopyNodeImage(c *gin.Context) {
	var req struct {
		Remote      string `json:"remote"`
		Image       string `json:"image" binding:"required"`
		Alias       string `json:"alias"`
		CopyAliases bool   `json:"copy_aliases"`
		Public      bool   `json:"public"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	client, _, ok := nodeClientByID(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 900*time.Second)
	defer cancel()
	if err := client.CopyImage(ctx, req.Remote, req.Image, req.Alias, req.CopyAliases, req.Public); err != nil {
		response.Error(c, 500, "下载镜像失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"message": "镜像下载完成"})
}

// ImportNodeImage 从 URL 导入镜像到指定节点
func ImportNodeImage(c *gin.Context) {
	var req struct {
		URL    string `json:"url" binding:"required"`
		Alias  string `json:"alias"`
		Public bool   `json:"public"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	client, _, ok := nodeClientByID(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 900*time.Second)
	defer cancel()
	if err := client.ImportImage(ctx, req.URL, req.Alias, req.Public); err != nil {
		response.Error(c, 500, "导入镜像失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"message": "镜像导入完成"})
}

// PublishNodeImage 把指定节点上的容器生成（发布）为镜像
func PublishNodeImage(c *gin.Context) {
	var req struct {
		Container   string `json:"container" binding:"required"`
		Alias       string `json:"alias"`
		Public      bool   `json:"public"`
		Description string `json:"description"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	client, _, ok := nodeClientByID(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 900*time.Second)
	defer cancel()
	if err := client.PublishImage(ctx, req.Container, req.Alias, req.Public, req.Description); err != nil {
		response.Error(c, 500, "生成镜像失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"message": "镜像生成完成"})
}

// CreateNodeImageAlias 给节点镜像新增别名
func CreateNodeImageAlias(c *gin.Context) {
	var req struct {
		Fingerprint string `json:"fingerprint" binding:"required"`
		Name        string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	client, _, ok := nodeClientByID(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := client.CreateImageAlias(ctx, req.Fingerprint, req.Name); err != nil {
		response.Error(c, 500, "新增别名失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"message": "别名已添加"})
}

// DeleteNodeImageAlias 删除节点镜像别名
func DeleteNodeImageAlias(c *gin.Context) {
	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	client, _, ok := nodeClientByID(c)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := client.DeleteImageAlias(ctx, req.Name); err != nil {
		response.Error(c, 500, "删除别名失败: "+err.Error())
		return
	}
	response.Success(c, gin.H{"message": "别名已删除"})
}
