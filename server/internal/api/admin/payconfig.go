package admin

import (
	"github.com/gin-gonic/gin"
	"lxdapi/internal/db"
	"lxdapi/models"
	"lxdapi/pkg/response"
)

// GetPayConfig 获取支付配置（易支付）
func GetPayConfig(c *gin.Context) {
	var config models.PayConfig
	if err := db.DB.Where("provider = ?", "epay").First(&config).Error; err != nil {
		if err.Error() == "record not found" {
			response.Success(c, models.PayConfig{Provider: "epay", Enabled: false})
			return
		}
		response.Error(c, 500, "获取支付配置失败")
		return
	}
	response.Success(c, config)
}

// SavePayConfig 保存支付配置（易支付）
func SavePayConfig(c *gin.Context) {
	var input models.PayConfig
	if err := c.ShouldBindJSON(&input); err != nil {
		response.Error(c, 400, "参数错误")
		return
	}
	input.Provider = "epay"

	var config models.PayConfig
	err := db.DB.Where("provider = ?", "epay").First(&config).Error
	if err != nil {
		if err := db.DB.Create(&input).Error; err != nil {
			response.Error(c, 500, "保存支付配置失败")
			return
		}
		response.Success(c, input)
		return
	}

	config.Enabled = input.Enabled
	config.APIURL = input.APIURL
	config.PID = input.PID
	config.Key = input.Key
	config.SignKey = input.SignKey
	config.CallbackURL = input.CallbackURL
	if err := db.DB.Save(&config).Error; err != nil {
		response.Error(c, 500, "保存支付配置失败")
		return
	}
	response.Success(c, config)
}
