package public

import (
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

	list := make([]ProductView, 0, len(products))
	for _, p := range products {
		v := ProductView{Product: p}
		if n, ok := nodeNames[p.NodeID]; ok {
			v.NodeName = n.Name
			v.NodeRegion = n.Region
		}
		list = append(list, v)
	}
	response.Success(c, list)
}
