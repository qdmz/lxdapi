package service

import (
	"context"
	"fmt"
	"strings"

	"lxdapi/internal/db"
	"lxdapi/internal/lxc"
	"lxdapi/models"
	"lxdapi/pkg/logger"
)

type TemplateService struct {
	lxcClient *lxc.Client
}

func NewTemplateService() *TemplateService {
	return &TemplateService{
		lxcClient: lxc.NewClient(),
	}
}

func (s *TemplateService) List(nodeID uint) ([]models.TemplateListResponse, error) {
	var templates []models.Template
	query := db.DB.Order("created_at DESC")
	if nodeID == 1 {
		// 本机节点的模板历史上可能以 node_id=0 或 1 入库，两种都算本机
		query = query.Where("node_id IN (0, 1)")
	} else if nodeID > 1 {
		query = query.Where("node_id = ?", nodeID)
	}
	if err := query.Find(&templates).Error; err != nil {
		return nil, err
	}

	result := make([]models.TemplateListResponse, len(templates))
	for i, t := range templates {
		result[i] = models.TemplateListResponse{
			NodeID:       t.NodeID,
			Fingerprint:  t.Fingerprint,
			Alias:        t.Alias,
			Architecture: t.Architecture,
			Description:  t.Description,
			OS:           t.OS,
			Release:      t.Release,
			Size:         t.Size,
			SizeHuman:    formatSize(t.Size),
			Public:       t.Public,
			AutoUpdate:   t.AutoUpdate,
			UploadedAt:   t.UploadedAt,
			CreatedAt:    t.CreatedAt.Format("2006-01-02 15:04:05"),
		}
	}

	return result, nil
}

func (s *TemplateService) SyncFromLXD(ctx context.Context, nodeID uint) (int, int, int, error) {
	logger.Info("开始从LXD同步镜像模板")

	// 按节点选择 client：远程节点走 NewRemoteClient
	client := ClientForNode(nodeID)
	images, err := client.ListImages(ctx)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("获取LXD镜像列表失败: %v", err)
	}

	added := 0
	updated := 0

	lxdFingerprints := make(map[string]bool)

	for _, img := range images {
		lxdFingerprints[img.Fingerprint] = true

		alias := ""
		if len(img.Aliases) > 0 {
			alias = img.Aliases[0].Name
		}

		os := ""
		release := ""
		description := ""

		if img.Properties != nil {
			if v, ok := img.Properties["os"].(string); ok {
				os = v
			}
			if v, ok := img.Properties["release"].(string); ok {
				release = v
			}
			if v, ok := img.Properties["description"].(string); ok {
				description = v
			}
		}

		var existing models.Template
		// (node_id, fingerprint) 复合唯一：按节点+指纹查本节点的行；
		// 同一镜像在不同节点各存一行互不影响（旧版全局唯一会互相抢行）。
		err := db.DB.Where("node_id = ? AND fingerprint = ?", nodeID, img.Fingerprint).First(&existing).Error

		template := models.Template{
			NodeID:       nodeID,
			Fingerprint:  img.Fingerprint,
			Alias:        alias,
			Architecture: img.Architecture,
			Description:  description,
			OS:           os,
			Release:      release,
			Size:         img.Size,
			Public:       img.Public,
			AutoUpdate:   img.AutoUpdate,
			UploadedAt:   img.UploadedAt,
		}

		if err != nil {
			if err := db.DB.Create(&template).Error; err != nil {
				logger.Warn("添加模板失败 %s: %v", img.Fingerprint, err)
				continue
			}
			added++
			logger.Info("添加模板: %s (%s)", alias, img.Fingerprint[:12])
		} else {
			template.ID = existing.ID
			if err := db.DB.Model(&existing).Updates(template).Error; err != nil {
				logger.Warn("更新模板失败 %s: %v", img.Fingerprint, err)
				continue
			}
			updated++
			logger.Info("更新模板: %s (%s)", alias, img.Fingerprint[:12])
		}
	}

	var dbTemplates []models.Template
	db.DB.Where("node_id = ?", nodeID).Find(&dbTemplates)

	deleted := 0
	for _, dbTpl := range dbTemplates {
		if !lxdFingerprints[dbTpl.Fingerprint] {
			if err := db.DB.Unscoped().Delete(&dbTpl).Error; err != nil {
				logger.Warn("删除过期模板失败 %s: %v", dbTpl.Fingerprint, err)
				continue
			}
			deleted++
			logger.Info("删除过期模板: %s (%s)", dbTpl.Alias, dbTpl.Fingerprint[:12])
		}
	}

	logger.OK("同步完成: 新增 %d, 更新 %d, 删除 %d", added, updated, deleted)
	return added, updated, deleted, nil
}

func (s *TemplateService) Delete(ctx context.Context, fingerprint string) error {
	logger.Info("删除模板: %s", fingerprint)

	if err := s.lxcClient.DeleteImage(ctx, fingerprint); err != nil {
		return fmt.Errorf("从LXD删除镜像失败: %v", err)
	}

	if err := db.DB.Unscoped().Where("fingerprint = ?", fingerprint).Delete(&models.Template{}).Error; err != nil {
		logger.Warn("从数据库删除模板记录失败: %v", err)
	}

	logger.OK("模板删除成功: %s", fingerprint)
	return nil
}

func (s *TemplateService) BatchDelete(ctx context.Context, fingerprints []string) (int, int, error) {
	logger.Info("开始批量删除模板，数量: %d", len(fingerprints))

	success := 0
	failed := 0

	for _, fp := range fingerprints {
		if err := s.Delete(ctx, fp); err != nil {
			logger.Error("删除模板失败 %s: %v", fp, err)
			failed++
		} else {
			success++
		}
	}

	logger.OK("批量删除完成: 成功 %d, 失败 %d", success, failed)
	return success, failed, nil
}

// ListRemoteImages 列出指定节点可从远程镜像仓库下载的系统镜像
// （按节点路由：本机直接查、远程节点经其系统级 API 查；结果已按发行版去重）。
// remote 传了就只查该源；不传则合并 images 与 ubuntu 两源——Ubuntu 已从
// images.linuxcontainers.org 下架、只在 ubuntu: 源里，单查 images 会漏掉全部 Ubuntu。
// 合并在主控层做（分别向节点查两源再合并），节点端仍保持单源查询。
// 两源都失败才报错；只失败一源时用另一源的结果并记警告，避免单源异常把整个列表拖垮。
func (s *TemplateService) ListRemoteImages(ctx context.Context, nodeID uint, remote string) ([]lxc.RemoteImage, error) {
	client := ClientForNode(nodeID)
	if remote != "" {
		return client.ListRemoteImages(ctx, remote)
	}
	imagesList, errImages := client.ListRemoteImages(ctx, "images")
	ubuntuList, errUbuntu := client.ListRemoteImages(ctx, "ubuntu")
	if errImages != nil && errUbuntu != nil {
		return nil, errImages
	}
	if errImages != nil {
		logger.Warn("查询 images 远程源失败，仅展示 ubuntu 源: %v", errImages)
	}
	if errUbuntu != nil {
		logger.Warn("查询 ubuntu 远程源失败，仅展示 images 源: %v", errUbuntu)
	}
	return lxc.MergeRemoteImages(imagesList, ubuntuList), nil
}

// DownloadRemoteImage 把远程仓库的一个系统镜像下载到指定节点，
// 下载完成后同步模板库，使其立即成为该节点的模板。
// alias 为空时由远程别名推导（ubuntu/24.04 → ubuntu-24.04），与既有模板命名习惯一致。
func (s *TemplateService) DownloadRemoteImage(ctx context.Context, nodeID uint, remote, image, alias string) (int, int, int, error) {
	if alias == "" {
		alias = strings.ReplaceAll(image, "/", "-")
	}
	client := ClientForNode(nodeID)
	if err := client.CopyImage(ctx, remote, image, alias, false, false); err != nil {
		return 0, 0, 0, fmt.Errorf("下载镜像失败: %v", err)
	}
	added, updated, deleted, err := s.SyncFromLXD(ctx, nodeID)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("镜像已下载，但同步模板失败: %v", err)
	}
	return added, updated, deleted, nil
}

func formatSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
