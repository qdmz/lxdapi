package service

import (
	"context"
	"fmt"
	"lxdapi/internal/db"
	"lxdapi/internal/ipv4"
	"lxdapi/internal/node"
	"lxdapi/models"
	"lxdapi/pkg/logger"
)

type IPv4Service struct {
}

func NewIPv4Service() *IPv4Service {
	return &IPv4Service{}
}

func (s *IPv4Service) AllocateIPv4(ctx context.Context, containerName, userID string, count int) ([]string, error) {
	var container models.Container
	if err := db.DB.Where("name = ?", containerName).First(&container).Error; err != nil {
		return nil, fmt.Errorf("容器不存在")
	}

	if container.IPv4PoolLimit == 0 {
		return nil, fmt.Errorf("容器未启用IPv4地址池功能")
	}

	// 远程节点容器：下发到节点分配（选池、绑定、nft 都在节点本机），主控只做限额校验与镜像记录
	if node.IsRemoteNode(container.NodeID) {
		var current int64
		db.DB.Model(&models.IPv4Binding{}).Where("container_name = ? AND status = ?", containerName, "allocated").Count(&current)
		if int(current)+count > container.IPv4PoolLimit {
			return nil, fmt.Errorf("超过IPv4地址池限制，最多%d个，已有%d个", container.IPv4PoolLimit, current)
		}
		var out struct {
			IPs []string `json:"ips"`
		}
		payload := map[string]interface{}{"container_name": containerName, "count": count}
		if err := node.ClientForNode(container.NodeID).RemoteDoJSON(ctx, "POST", "/api/system/ip-pool/allocate?version=v4", payload, &out); err != nil {
			return nil, fmt.Errorf("节点分配IPv4失败: %v", err)
		}
		for _, ip := range out.IPs {
			db.DB.Unscoped().Where("ip_address = ?", ip).Delete(&models.IPv4Binding{})
			db.DB.Create(&models.IPv4Binding{IPAddress: ip, ContainerName: containerName, UserID: userID, Status: "allocated"})
			db.DB.Model(&models.IPv4Pool{}).Where("ip_address = ?", ip).Update("status", "used")
		}
		logger.OK("IPv4分配成功(节点下发): %s -> %v", containerName, out.IPs)
		return out.IPs, nil
	}

	if ipv4.GlobalManager == nil {
		return nil, fmt.Errorf("IPv4功能未启用")
	}

	currentIPs, _ := ipv4.GlobalManager.GetContainerIPs(containerName)
	if len(currentIPs)+count > container.IPv4PoolLimit {
		return nil, fmt.Errorf("超过IPv4地址池限制，最多%d个，已有%d个", container.IPv4PoolLimit, len(currentIPs))
	}

	ips, err := ipv4.GlobalManager.AllocateIPs(containerName, userID, count, container.NodeID)
	if err != nil {
		return nil, err
	}

	containerIP := container.PrivateIP
	if containerIP == "" {
		for _, ip := range ips {
			db.DB.Unscoped().Where("ip_address = ?", ip).Delete(&models.IPv4Binding{})
		}
		return nil, fmt.Errorf("容器未配置内网IP")
	}

	for _, ip := range ips {
		if err := ipv4.GlobalManager.BindIP(containerName, ip, containerIP); err != nil {
			logger.Error("IPv4绑定失败: %v", err)
		}
	}

	return ips, nil
}

func (s *IPv4Service) ReleaseIPv4(containerName, ipAddress string) error {
	// 远程节点容器：先让节点释放，再删主控镜像记录
	if nodeID := node.ResolveNodeIDForContainer(containerName); node.IsRemoteNode(nodeID) {
		payload := map[string]interface{}{"container_name": containerName, "ip_address": ipAddress}
		if err := node.ClientForNode(nodeID).RemoteDoJSON(context.Background(), "POST", "/api/system/ip-pool/release?version=v4", payload, nil); err != nil {
			return fmt.Errorf("节点释放IPv4失败: %v", err)
		}
		db.DB.Unscoped().Where("ip_address = ?", ipAddress).Delete(&models.IPv4Binding{})
		db.DB.Model(&models.IPv4Pool{}).Where("ip_address = ?", ipAddress).Update("status", "available")
		logger.OK("释放IPv4成功(节点下发): %s -> %s", containerName, ipAddress)
		return nil
	}

	if ipv4.GlobalManager == nil {
		return fmt.Errorf("IPv4功能未启用")
	}

	if err := ipv4.GlobalManager.ReleaseIP(containerName, ipAddress); err != nil {
		return err
	}

	logger.OK("释放IPv4成功: %s -> %s", containerName, ipAddress)
	return nil
}
