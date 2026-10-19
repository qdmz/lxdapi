package task

import (
	"time"

	"lxdapi/internal/service"
	"lxdapi/pkg/logger"
)

// StartAutoRenewalScheduler 自动续费调度器：每小时扫描一次开启自动续费的用户，
// 对即将到期（24h 内）的容器尝试用余额自动续费。
func StartAutoRenewalScheduler() {
	logger.OK("自动续费调度器已启动（每小时扫描）")

	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()

		// 启动时先跑一次
		service.RunAutoRenewal()

		for range ticker.C {
			service.RunAutoRenewal()
		}
	}()
}
