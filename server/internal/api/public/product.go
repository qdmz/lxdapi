package public

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"lxdapi/internal/db"
	"lxdapi/models"
	"lxdapi/pkg/response"
)

// ProductView 前台产品视图
type ProductView struct {
	models.Product
	NodeName   string `json:"node_name"`
	NodeRegion string `json:"node_region"`
	// ImageName 镜像展示名（由模板别名生成，避免前台直接露出 64 位指纹哈希）；查不到模板时为空
	ImageName string `json:"image_name,omitempty"`
}

// imageDisplayName 模板的人类可读名：别名优先，其次 系统+版本，最后描述
func imageDisplayName(t models.Template) string {
	if t.Alias != "" {
		return t.Alias
	}
	if name := strings.TrimSpace(t.OS + " " + t.Release); name != "" {
		return name
	}
	return t.Description
}

// GetProducts 前台可购买产品列表
func GetProducts(c *gin.Context) {
	var products []models.Product
	if err := db.DB.Where("status = ?", "active").Order("sort asc, id asc").Find(&products).Error; err != nil {
		response.Error(c, 500, "获取产品失败")
		return
	}

	nodeNames := map[uint]models.Node{}
	var nodes []models.Node
	db.DB.Find(&nodes)
	for _, n := range nodes {
		nodeNames[n.ID] = n
	}

	// 镜像指纹 -> 模板（指纹全局唯一；优先同节点行，退回任意同指纹行）
	tplByFPNode := map[string]models.Template{}
	tplByFP := map[string]models.Template{}
	var templates []models.Template
	db.DB.Find(&templates)
	for _, t := range templates {
		tplByFP[t.Fingerprint] = t
		tplByFPNode[fmt.Sprintf("%s|%d", t.Fingerprint, t.NodeID)] = t
	}

	list := make([]ProductView, 0, len(products))
	for _, p := range products {
		v := ProductView{Product: p}
		if n, ok := nodeNames[p.NodeID]; ok {
			v.NodeName = n.Name
			v.NodeRegion = n.Region
		}
		if p.Image != "" {
			t, ok := tplByFPNode[fmt.Sprintf("%s|%d", p.Image, p.NodeID)]
			if !ok {
				t, ok = tplByFP[p.Image]
			}
			if ok {
				v.ImageName = imageDisplayName(t)
			}
		}
		list = append(list, v)
	}
	response.Success(c, list)
}
