package service

import (
	"context"
	"lxdapi/internal/db"
	"lxdapi/internal/lxc"
	"lxdapi/internal/node"
	"lxdapi/models"
	"lxdapi/pkg/logger"
)

type StorageService struct {
	lxcClient *lxc.Client
}

func NewStorageService() *StorageService {
	return &StorageService{lxcClient: lxc.NewClient()}
}

func (s *StorageService) List(nodeID uint) ([]models.StoragePool, error) {
	var pools []models.StoragePool
	query := db.DB.Order("priority ASC, name ASC")
	if nodeID > 0 {
		query = query.Where("node_id = ?", nodeID)
	}
	err := query.Find(&pools).Error
	return pools, err
}

func (s *StorageService) SyncFromLXD(ctx context.Context, nodeID uint) (int, int, int, error) {
	logger.Info("开始从LXD同步存储池")

	// 按节点选择 client：远程节点走 NewRemoteClient
	client := ClientForNode(nodeID)
	pools, err := client.ListStoragePools(ctx)
	if err != nil {
		return 0, 0, 0, err
	}

	added, updated := 0, 0
	lxdPools := make(map[string]bool)

	for _, p := range pools {
		lxdPools[p.Name] = true

		var totalSpace, usedSpace int64
		if res, err := client.GetStoragePoolResources(ctx, p.Name); err == nil {
			totalSpace = res.Space.Total
			usedSpace = res.Space.Used
		}

		var existing models.StoragePool
		// (node_id, name) 复合唯一：按节点+名查，不同节点的同名池互不干扰
		err := db.DB.Where("node_id = ? AND name = ?", nodeID, p.Name).First(&existing).Error

		pool := models.StoragePool{
			NodeID:      nodeID,
			Name:        p.Name,
			Driver:      p.Driver,
			Description: p.Description,
			Status:      p.Status,
			UsedBy:      len(p.UsedBy),
			TotalSpace:  totalSpace,
			UsedSpace:   usedSpace,
		}

		if err != nil {
			pool.Priority = 2
			if p.Name == "default" {
				pool.Priority = 1
			}
			if err := db.DB.Create(&pool).Error; err != nil {
				logger.Warn("添加存储池失败 %s: %v", p.Name, err)
				continue
			}
			added++
			logger.Info("添加存储池: %s (%s)", p.Name, p.Driver)
		} else {
			pool.Priority = existing.Priority
			db.DB.Model(&existing).Updates(pool)
			updated++
		}
	}

	var dbPools []models.StoragePool
	db.DB.Where("node_id = ?", nodeID).Find(&dbPools)
	deleted := 0
	for _, dbPool := range dbPools {
		if !lxdPools[dbPool.Name] {
			db.DB.Unscoped().Delete(&dbPool)
			deleted++
			logger.Info("删除过期存储池: %s", dbPool.Name)
		}
	}

	logger.OK("同步完成: 新增 %d, 更新 %d, 删除 %d", added, updated, deleted)
	return added, updated, deleted, nil
}

func (s *StorageService) SetPriority(name string, priority int, nodeID uint) error {
	q := db.DB.Model(&models.StoragePool{}).Where("name = ?", name)
	if nodeID > 0 {
		q = q.Where("node_id = ?", nodeID)
	}
	return q.Update("priority", priority).Error
}

func (s *StorageService) GetDefault() string {
	return s.GetDefaultForNode(0)
}

// GetDefaultForNode 按节点选默认存储池：只看该节点的池，并用该节点的客户端探活。
// 此前不分节点混选，远程节点没有同名池时建容器/重装会失败。
func (s *StorageService) GetDefaultForNode(nodeID uint) string {
	ctx := context.Background()
	eff := nodeID
	if eff == 0 {
		eff = 1
	}
	client := node.ClientForNode(nodeID)
	var pools []models.StoragePool
	db.DB.Where("node_id = ? AND priority > 0", eff).Order("priority ASC").Find(&pools)

	for _, pool := range pools {
		res, err := client.GetStoragePoolResources(ctx, pool.Name)
		if err != nil {
			continue
		}
		if res.Space.Total > 0 && float64(res.Space.Used)/float64(res.Space.Total) < 0.95 {
			return pool.Name
		}
	}
	// 兜底：节点登记时填的存储池
	var n models.Node
	if err := db.DB.First(&n, eff).Error; err == nil && n.StoragePool != "" {
		return n.StoragePool
	}
	return "default"
}
