package admin

import (
	"github.com/gin-gonic/gin"
	"lxdapi/internal/db"
	"lxdapi/models"
	"lxdapi/pkg/response"
)

// GetProducts 获取产品列表
func GetProducts(c *gin.Context) {
	var products []models.Product
	if err := db.DB.Find(&products).Error; err != nil {
		response.Error(c, 500, "获取产品列表失败")
		return
	}
	response.Success(c, products)
}

// GetProduct 获取单个产品
func GetProduct(c *gin.Context) {
	id := c.Param("id")
	var product models.Product
	if err := db.DB.First(&product, id).Error; err != nil {
		response.Error(c, 404, "产品不存在")
		return
	}
	response.Success(c, product)
}

// CreateProduct 创建产品
func CreateProduct(c *gin.Context) {
	var product models.Product
	if err := c.ShouldBindJSON(&product); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	if err := db.DB.Create(&product).Error; err != nil {
		response.Error(c, 500, "创建产品失败")
		return
	}
	response.Success(c, product)
}

// UpdateProduct 更新产品
func UpdateProduct(c *gin.Context) {
	id := c.Param("id")
	var product models.Product
	if err := db.DB.First(&product, id).Error; err != nil {
		response.Error(c, 404, "产品不存在")
		return
	}
	var input models.Product
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	product.Name = input.Name
	product.Description = input.Description
	product.Category = input.Category
	product.Status = input.Status
	product.CPU = input.CPU
	product.Memory = input.Memory
	product.Disk = input.Disk
	product.Bandwidth = input.Bandwidth
	product.TrafficLimit = input.TrafficLimit
	product.PriceHourly = input.PriceHourly
	product.PriceMonthly = input.PriceMonthly
	product.PriceQuarterly = input.PriceQuarterly
	product.PriceYearly = input.PriceYearly
	product.MaxContainers = input.MaxContainers
	product.AllowNesting = input.AllowNesting
	product.AllowPortForward = input.AllowPortForward
	product.AllowDomainBinding = input.AllowDomainBinding
	product.AllowSFTP = input.AllowSFTP
	product.AutoSync = input.AutoSync

	if err := db.DB.Save(&product).Error; err != nil {
		response.Error(c, 500, "更新产品失败")
		return
	}
	response.Success(c, product)
}

// DeleteProduct 删除产品
func DeleteProduct(c *gin.Context) {
	id := c.Param("id")
	if err := db.DB.Delete(&models.Product{}, id).Error; err != nil {
		response.Error(c, 500, "删除产品失败")
		return
	}
	response.Success(c, nil)
}

// GetPlans 获取所有套餐
func GetPlans(c *gin.Context) {
	var plans []models.Plan
	if err := db.DB.Find(&plans).Error; err != nil {
		response.Error(c, 500, "获取套餐列表失败")
		return
	}
	response.Success(c, plans)
}

// CreatePlan 创建套餐
func CreatePlan(c *gin.Context) {
	var plan models.Plan
	if err := c.ShouldBindJSON(&plan); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	if err := db.DB.Create(&plan).Error; err != nil {
		response.Error(c, 500, "创建套餐失败")
		return
	}
	response.Success(c, plan)
}

// UpdatePlan 更新套餐
func UpdatePlan(c *gin.Context) {
	id := c.Param("id")
	var plan models.Plan
	if err := db.DB.First(&plan, id).Error; err != nil {
		response.Error(c, 404, "套餐不存在")
		return
	}
	var input models.Plan
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	plan.Name = input.Name
	plan.Description = input.Description
	plan.ProductID = input.ProductID
	plan.CPU = input.CPU
	plan.Memory = input.Memory
	plan.Disk = input.Disk
	plan.Bandwidth = input.Bandwidth
	plan.TrafficLimit = input.TrafficLimit
	plan.PriceHourly = input.PriceHourly
	plan.PriceMonthly = input.PriceMonthly
	plan.PriceQuarterly = input.PriceQuarterly
	plan.PriceYearly = input.PriceYearly
	plan.MaxContainers = input.MaxContainers
	plan.AllowNesting = input.AllowNesting
	plan.AllowPortForward = input.AllowPortForward
	plan.AllowDomainBinding = input.AllowDomainBinding
	plan.AllowSFTP = input.AllowSFTP
	plan.Status = input.Status

	if err := db.DB.Save(&plan).Error; err != nil {
		response.Error(c, 500, "更新套餐失败")
		return
	}
	response.Success(c, plan)
}

// DeletePlan 删除套餐
func DeletePlan(c *gin.Context) {
	id := c.Param("id")
	if err := db.DB.Delete(&models.Plan{}, id).Error; err != nil {
		response.Error(c, 500, "删除套餐失败")
		return
	}
	response.Success(c, nil)
}
