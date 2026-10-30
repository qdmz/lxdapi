package service

import (
	"context"
	"fmt"
	"lxdapi/internal/db"
	"lxdapi/internal/ipv6"
	"lxdapi/internal/node"
	"lxdapi/models"
	"lxdapi/pkg/logger"
)

type IPv6Service struct {
}

func NewIPv6Service() *IPv6Service {
	return &IPv6Service{}
}

func (s *IPv6Service) AllocateIPv6(ctx context.Context, containerName, userID string, count int) ([]string, error) {
	var container models.Container
	if err := db.DB.Where("name = ?", containerName).First(&container).Error; err != nil {
		return nil, fmt.Errorf("容器不存在")
	}

	if container.IPv6PoolLimit == 0 {
		return nil, fmt.Errorf("容器未启用IPv6地址池功能")
	}

	// 远程节点容器：下发到节点分配（选池、绑定、nft 都在节点本机），主控只做限额校验与镜像记录
	if node.IsRemoteNode(container.NodeID) {
		var current int64
		db.DB.Model(&models.IPv6Binding{}).Where("container_name = ? AND status = ?", containerName, "allocated").Count(&current)
		if int(current)+count > container.IPv6PoolLimit {
			return nil, fmt.Errorf("超过IPv6地址池限制，最多%d个，已有%d个", container.IPv6PoolLimit, current)
		}
		var out struct {
			IPs []string `json:"ips"`
		}
		payload := map[string]interface{}{"container_name": containerName, "count": count}
		if err := node.ClientForNode(container.NodeID).RemoteDoJSON(ctx, "POST", "/api/system/ip-pool/allocate?version=v6", payload, &out); err != nil {
			return nil, fmt.Errorf("节点分配IPv6失败: %v", err)
		}
		for _, ip := range out.IPs {
			db.DB.Unscoped().Where("ip_address = ?", ip).Delete(&models.IPv6Binding{})
			db.DB.Create(&models.IPv6Binding{IPAddress: ip, ContainerName: containerName, UserID: userID, Status: "allocated"})
			db.DB.Model(&models.IPv6Pool{}).Where("ip_address = ?", ip).Update("status", "used")
		}
		logger.OK("IPv6分配成功(节点下发): %s -> %v", containerName, out.IPs)
		return out.IPs, nil
	}

	if ipv6.GlobalManager == nil {
		return nil, fmt.Errorf("IPv6功能未启用")
	}

	currentIPs, _ := ipv6.GlobalManager.GetContainerIPs(containerName)
	if len(currentIPs)+count > container.IPv6PoolLimit {
		return nil, fmt.Errorf("超过IPv6地址池限制，最多%d个，已有%d个", container.IPv6PoolLimit, len(currentIPs))
	}

	ips, err := ipv6.GlobalManager.AllocateIPs(containerName, userID, count, container.NodeID)
	if err != nil {
		return nil, err
	}

	containerIP := container.PrivateIPv6
	if containerIP == "" {
		for _, ip := range ips {
			db.DB.Unscoped().Where("ip_address = ?", ip).Delete(&models.IPv6Binding{})
		}
		return nil, fmt.Errorf("容器未配置内网IPv6地址")
	}

	for _, ip := range ips {
		if err := ipv6.GlobalManager.BindIP(containerName, ip, containerIP); err != nil {
			logger.Error("IPv6绑定失败: %v", err)
		}
	}

	return ips, nil
}

func (s *IPv6Service) ReleaseIPv6(containerName, ipAddress string) error {
	// 远程节点容器：先让节点释放，再删主控镜像记录
	if nodeID := node.ResolveNodeIDForContainer(containerName); node.IsRemoteNode(nodeID) {
		payload := map[string]interface{}{"container_name": containerName, "ip_address": ipAddress}
		if err := node.ClientForNode(nodeID).RemoteDoJSON(context.Background(), "POST", "/api/system/ip-pool/release?version=v6", payload, nil); err != nil {
			return fmt.Errorf("节点释放IPv6失败: %v", err)
		}
		db.DB.Unscoped().Where("ip_address = ?", ipAddress).Delete(&models.IPv6Binding{})
		db.DB.Model(&models.IPv6Pool{}).Where("ip_address = ?", ipAddress).Update("status", "available")
		logger.OK("释放IPv6成功(节点下发): %s -> %s", containerName, ipAddress)
		return nil
	}

	if ipv6.GlobalManager == nil {
		return fmt.Errorf("IPv6功能未启用")
	}

	if err := ipv6.GlobalManager.ReleaseIP(containerName, ipAddress); err != nil {
		return err
	}

	logger.OK("释放IPv6成功: %s -> %s", containerName, ipAddress)
	return nil
}
