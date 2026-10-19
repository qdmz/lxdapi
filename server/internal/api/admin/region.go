package admin

import (
	"fmt"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"lxdapi/internal/db"
	"lxdapi/models"
	"lxdapi/pkg/logger"
	"lxdapi/pkg/response"
)

// 机房 regions 移植自 lxdcloud v1.1.17+（admin/region.go + service/multinode.go 机房部分）。
// 语义：节点按机房分组（nodes.region_id + region 文本双写，兼容旧数据），
// 商品按机房展示与筛选（products.region_id，随所属节点同步）。

// DefaultRegionName 无机房名称时的归属机房
const DefaultRegionName = "默认机房"

// RegionInput 机房入参
type RegionInput struct {
	Name        string `json:"name"`
	Code        string `json:"code"`
	Description string `json:"description"`
	SortOrder   int    `json:"sort_order"`
	Status      string `json:"status"`
}

// RegionView 机房 + 统计
type RegionView struct {
	models.Region
	NodeCount    int64 `json:"node_count"`
	ProductCount int64 `json:"product_count"`
}

// ListRegions 机房列表（含节点/商品数量）
func ListRegions(c *gin.Context) {
	var regions []models.Region
	if err := db.DB.Order("sort_order ASC, id ASC").Find(&regions).Error; err != nil {
		response.Error(c, 500, "获取机房列表失败")
		return
	}
	out := make([]RegionView, 0, len(regions))
	for _, r := range regions {
		v := RegionView{Region: r}
		db.DB.Model(&models.Node{}).Where("region_id = ?", r.ID).Count(&v.NodeCount)
		db.DB.Model(&models.Product{}).Where("region_id = ?", r.ID).Count(&v.ProductCount)
		out = append(out, v)
	}
	response.Success(c, out)
}

// CreateRegion 新增机房
func CreateRegion(c *gin.Context) {
	var in RegionInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		response.Error(c, 400, "机房名称不能为空")
		return
	}
	var cnt int64
	db.DB.Model(&models.Region{}).Where("name = ?", in.Name).Count(&cnt)
	if cnt > 0 {
		response.Error(c, 400, "机房名称已存在")
		return
	}
	r := models.Region{Name: in.Name, Code: in.Code, Description: in.Description, SortOrder: in.SortOrder, Status: in.Status}
	if r.Status == "" {
		r.Status = "active"
	}
	if err := db.DB.Create(&r).Error; err != nil {
		response.Error(c, 500, "创建机房失败")
		return
	}
	response.Success(c, r)
}

// UpdateRegion 更新机房（改名时同步节点的 region 文本）
func UpdateRegion(c *gin.Context) {
	var r models.Region
	if err := db.DB.First(&r, c.Param("id")).Error; err != nil {
		response.Error(c, 404, "机房不存在")
		return
	}
	var in RegionInput
	if err := c.ShouldBindJSON(&in); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" {
		response.Error(c, 400, "机房名称不能为空")
		return
	}
	var cnt int64
	db.DB.Model(&models.Region{}).Where("name = ? AND id <> ?", in.Name, r.ID).Count(&cnt)
	if cnt > 0 {
		response.Error(c, 400, "机房名称已存在")
		return
	}
	if in.Status == "" {
		in.Status = "active"
	}
	err := db.DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&r).Updates(map[string]interface{}{
			"name": in.Name, "code": in.Code, "description": in.Description,
			"sort_order": in.SortOrder, "status": in.Status,
		}).Error; err != nil {
			return err
		}
		return tx.Model(&models.Node{}).Where("region_id = ?", r.ID).Update("region", in.Name).Error
	})
	if err != nil {
		response.Error(c, 500, "更新机房失败")
		return
	}
	response.Success(c, gin.H{"updated": true})
}

// DeleteRegion 删除机房（仍有节点时拒绝）
func DeleteRegion(c *gin.Context) {
	var r models.Region
	if err := db.DB.First(&r, c.Param("id")).Error; err != nil {
		response.Error(c, 404, "机房不存在")
		return
	}
	var cnt int64
	db.DB.Model(&models.Node{}).Where("region_id = ?", r.ID).Count(&cnt)
	if cnt > 0 {
		response.Error(c, 400, fmt.Sprintf("该机房下仍有 %d 个节点，请先移走节点", cnt))
		return
	}
	if err := db.DB.Delete(&r).Error; err != nil {
		response.Error(c, 500, "删除机房失败")
		return
	}
	response.Success(c, gin.H{"deleted": true})
}

// ensureRegionByName 按名称取机房，不存在则创建；空名称归入「默认机房」
func ensureRegionByName(tx *gorm.DB, name string) (*models.Region, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		name = DefaultRegionName
	}
	var r models.Region
	if err := tx.Where("name = ?", name).First(&r).Error; err == nil {
		return &r, nil
	}
	r = models.Region{Name: name, Status: "active"}
	if err := tx.Create(&r).Error; err != nil {
		return nil, err
	}
	return &r, nil
}

// resolveNodeRegion 节点保存时决定其机房：优先 region_id，否则按 region 名称自动建/取
func resolveNodeRegion(tx *gorm.DB, regionID uint, regionName string) (*models.Region, error) {
	if regionID > 0 {
		var r models.Region
		if err := tx.First(&r, regionID).Error; err != nil {
			return nil, fmt.Errorf("机房不存在")
		}
		return &r, nil
	}
	return ensureRegionByName(tx, regionName)
}

// syncProductsRegionOfNode 节点机房变化后，同步该节点下商品的 region_id
func syncProductsRegionOfNode(tx *gorm.DB, nodeID, regionID uint) error {
	return tx.Model(&models.Product{}).Where("node_id = ?", nodeID).Update("region_id", regionID).Error
}

// BackfillNodeRegions 启动时一次性回填：按节点现有 region 文本建立机房、
// 补齐 nodes.region_id / region 文本与 products.region_id（幂等，可重复执行）
func BackfillNodeRegions() {
	var nodes []models.Node
	if err := db.DB.Find(&nodes).Error; err != nil {
		logger.Error("机房回填：读取节点失败: %v", err)
		return
	}
	fixed := 0
	err := db.DB.Transaction(func(tx *gorm.DB) error {
		for _, n := range nodes {
			r, err := resolveNodeRegion(tx, n.RegionID, n.Region)
			if err != nil {
				return err
			}
			if n.RegionID != r.ID || n.Region != r.Name {
				if err := tx.Model(&models.Node{}).Where("id = ?", n.ID).
					Updates(map[string]interface{}{"region_id": r.ID, "region": r.Name}).Error; err != nil {
					return err
				}
				fixed++
			}
			if err := syncProductsRegionOfNode(tx, n.ID, r.ID); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		logger.Error("机房回填失败（已回滚，不影响启动）: %v", err)
		return
	}
	if fixed > 0 {
		logger.OK("机房回填：%d 个节点的机房归属已补齐", fixed)
	}
}
