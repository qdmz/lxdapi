package service

import (
	"lxdapi/internal/db"
	"lxdapi/models"
)

// OrderProductInfo 订单列表/详情附带的产品展示信息（库存 / 限购 / 各周期价格）。
type OrderProductInfo struct {
	Stock          int     `json:"stock"` // -1 表示不限
	MaxPerUser     int     `json:"max_per_user"` // 0 表示不限
	PriceHourly    float64 `json:"price_hourly"`
	PriceMonthly   float64 `json:"price_monthly"`
	PriceQuarterly float64 `json:"price_quarterly"`
	PriceYearly    float64 `json:"price_yearly"`
}

// ProductInfoMap 按产品 ID 批量取展示信息；已删除的产品不出现在结果里（前端据此不展示）。
func ProductInfoMap(ids []uint) map[uint]*OrderProductInfo {
	out := make(map[uint]*OrderProductInfo)
	if len(ids) == 0 {
		return out
	}
	var products []models.Product
	if err := db.DB.Where("id IN ?", ids).Find(&products).Error; err != nil {
		return out
	}
	for i := range products {
		p := &products[i]
		out[p.ID] = &OrderProductInfo{
			Stock:          p.Stock,
			MaxPerUser:     p.MaxPerUser,
			PriceHourly:    p.PriceHourly,
			PriceMonthly:   p.PriceMonthly,
			PriceQuarterly: p.PriceQuarterly,
			PriceYearly:    p.PriceYearly,
		}
	}
	return out
}
