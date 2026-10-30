package system

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	"lxdapi/internal/lxc"
	"lxdapi/pkg/response"
)

// 节点系统级 API：镜像与存储池。
// 主控对远程节点的镜像/存储操作全部经这里下发到节点本机执行
// （lxc.Client 在节点本机走本地 socket）。

func resCtx(c *gin.Context, timeout time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Request.Context(), timeout)
}

// ListImages 节点镜像列表
// @Router /api/system/images [get]
func ListImages(c *gin.Context) {
	ctx, cancel := resCtx(c, 60*time.Second)
	defer cancel()
	images, err := lxc.NewClient().ListImages(ctx)
	if err != nil {
		response.Error(c, 500, "获取镜像列表失败: "+err.Error())
		return
	}
	response.Success(c, images)
}

// ListRemoteImages 节点远程镜像仓库可下载系统列表（按发行版去重、易读名）
// @Router /api/system/remote-images [get]
func ListRemoteImages(c *gin.Context) {
	ctx, cancel := resCtx(c, 300*time.Second)
	defer cancel()
	images, err := lxc.NewClient().ListRemoteImages(ctx, c.Query("remote"))
	if err != nil {
		response.Error(c, 500, "获取远程镜像列表失败: "+err.Error())
		return
	}
	response.Success(c, images)
}

// GetImage 节点镜像详情
// @Router /api/system/images/{fingerprint} [get]
func GetImage(c *gin.Context) {
	ctx, cancel := resCtx(c, 60*time.Second)
	defer cancel()
	info, err := lxc.NewClient().GetImageInfo(ctx, c.Param("fingerprint"))
	if err != nil {
		response.Error(c, 500, "获取镜像详情失败: "+err.Error())
		return
	}
	response.Success(c, info)
}

// DeleteImage 删除节点镜像
// @Router /api/system/images/{fingerprint} [delete]
func DeleteImage(c *gin.Context) {
	ctx, cancel := resCtx(c, 120*time.Second)
	defer cancel()
	if err := lxc.NewClient().DeleteImage(ctx, c.Param("fingerprint")); err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	response.Success(c, gin.H{"message": "镜像已删除"})
}

// CopyImage 从远程镜像仓库下载镜像到本节点
// @Router /api/system/images/copy [post]
func CopyImage(c *gin.Context) {
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
	ctx, cancel := resCtx(c, 900*time.Second)
	defer cancel()
	if err := lxc.NewClient().CopyImage(ctx, req.Remote, req.Image, req.Alias, req.CopyAliases, req.Public); err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	response.Success(c, gin.H{"message": "镜像下载完成"})
}

// ImportImage 从 URL 导入镜像到本节点
// @Router /api/system/images/import [post]
func ImportImage(c *gin.Context) {
	var req struct {
		URL    string `json:"url" binding:"required"`
		Alias  string `json:"alias"`
		Public bool   `json:"public"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	ctx, cancel := resCtx(c, 900*time.Second)
	defer cancel()
	if err := lxc.NewClient().ImportImage(ctx, req.URL, req.Alias, req.Public); err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	response.Success(c, gin.H{"message": "镜像导入完成"})
}

// PublishImage 把容器发布（生成）为镜像
// @Router /api/system/images/publish [post]
func PublishImage(c *gin.Context) {
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
	ctx, cancel := resCtx(c, 900*time.Second)
	defer cancel()
	if err := lxc.NewClient().PublishImage(ctx, req.Container, req.Alias, req.Public, req.Description); err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	response.Success(c, gin.H{"message": "镜像生成完成"})
}

// CreateImageAlias 新增镜像别名
// @Router /api/system/images/alias [post]
func CreateImageAlias(c *gin.Context) {
	var req struct {
		Fingerprint string `json:"fingerprint" binding:"required"`
		Name        string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	ctx, cancel := resCtx(c, 60*time.Second)
	defer cancel()
	if err := lxc.NewClient().CreateImageAlias(ctx, req.Fingerprint, req.Name); err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	response.Success(c, gin.H{"message": "别名已添加"})
}

// DeleteImageAlias 删除镜像别名
// @Router /api/system/images/alias/delete [post]
func DeleteImageAlias(c *gin.Context) {
	var req struct {
		Name string `json:"name" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	ctx, cancel := resCtx(c, 60*time.Second)
	defer cancel()
	if err := lxc.NewClient().DeleteImageAlias(ctx, req.Name); err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	response.Success(c, gin.H{"message": "别名已删除"})
}

// poolWithResources 存储池 + 资源用量（节点侧合并返回，主控同步用）
type poolWithResources struct {
	lxc.StoragePoolInfo
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
}

// ListStoragePools 节点存储池列表（含资源用量）
// @Router /api/system/storage-pools [get]
func ListStoragePools(c *gin.Context) {
	ctx, cancel := resCtx(c, 60*time.Second)
	defer cancel()
	client := lxc.NewClient()
	pools, err := client.ListStoragePools(ctx)
	if err != nil {
		response.Error(c, 500, "获取存储池列表失败: "+err.Error())
		return
	}
	out := make([]poolWithResources, 0, len(pools))
	for _, p := range pools {
		item := poolWithResources{StoragePoolInfo: p}
		if res, err := client.GetStoragePoolResources(ctx, p.Name); err == nil {
			item.Total = res.Space.Total
			item.Used = res.Space.Used
		}
		out = append(out, item)
	}
	response.Success(c, out)
}
