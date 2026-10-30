package system

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"lxdapi/internal/db"
	"lxdapi/internal/lxc"
	"lxdapi/internal/netnat"
	"lxdapi/internal/service"
	"lxdapi/models"
	"lxdapi/pkg/response"
)

// 本文件是节点侧的「功能下发」端点：主控把 DNS、网络 NAT、IP 池这类
// 原本只能在本机执行的操作，按容器/节点归属下发到目标节点，由节点在本机执行。
// 鉴权与其它 /api/system/* 一致（X-API-Hash）。

// ExecInContainer 在本节点容器内执行命令（主控的文件管理、CPU 采样等经此下发）
func ExecInContainer(c *gin.Context) {
	name := c.Param("name")
	var req struct {
		Command []string `json:"command" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.Command) == 0 {
		response.Error(c, 400, "参数错误: command 不能为空")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 120*time.Second)
	defer cancel()
	output, err := lxc.NewClient().ExecInContainer(ctx, name, req.Command)
	if err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	response.Success(c, gin.H{"container": name, "output": output})
}

// GetContainerDNS 读取容器当前 DNS（节点本机执行）
func GetContainerDNS(c *gin.Context) {
	name := c.Param("name")
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	dnsServers, err := lxc.NewClient().GetContainerDNS(ctx, name)
	if err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	response.Success(c, gin.H{"container": name, "dns": dnsServers})
}

// SetContainerDNS 设置容器 DNS（节点本机执行）
func SetContainerDNS(c *gin.Context) {
	name := c.Param("name")
	var req struct {
		DNS []string `json:"dns" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || len(req.DNS) == 0 {
		response.Error(c, 400, "参数错误: DNS服务器列表不能为空")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Second)
	defer cancel()
	if err := lxc.NewClient().SetContainerDNS(ctx, name, req.DNS); err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	response.Success(c, gin.H{"container": name, "dns": req.DNS})
}

// GetNetworkNAT 读取本节点 lxdbr0 的 NAT 开关
func GetNetworkNAT(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	response.Success(c, netnat.Status(ctx))
}

// SetNetworkNAT 设置本节点 lxdbr0 的 NAT 开关
func SetNetworkNAT(c *gin.Context) {
	var req struct {
		IPv4NAT *bool `json:"ipv4_nat"`
		IPv6NAT *bool `json:"ipv6_nat"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误: "+err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	if err := netnat.Set(ctx, req.IPv4NAT, req.IPv6NAT); err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	response.Success(c, "设置成功")
}

// FixEgress 在本节点执行出网一键修复
func FixEgress(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 90*time.Second)
	defer cancel()
	result, err := netnat.FixEgress(ctx)
	if err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	response.Success(c, result)
}

// AllocateIPPool 在本节点为容器分配公网 IP（v4/v6 由 version 参数区分）
func AllocateIPPool(c *gin.Context) {
	version := c.Query("version")
	var req struct {
		ContainerName string `json:"container_name" binding:"required"`
		Count         int    `json:"count"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误: "+err.Error())
		return
	}
	if req.Count <= 0 {
		req.Count = 1
	}
	var container models.Container
	if err := db.DB.Where("name = ?", req.ContainerName).First(&container).Error; err != nil {
		response.Error(c, 404, "容器不存在")
		return
	}
	ctx := c.Request.Context()
	var ips []string
	var err error
	if version == "v6" {
		ips, err = service.NewIPv6Service().AllocateIPv6(ctx, req.ContainerName, container.UserID, req.Count)
	} else {
		ips, err = service.NewIPv4Service().AllocateIPv4(ctx, req.ContainerName, container.UserID, req.Count)
	}
	if err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	response.Success(c, gin.H{"container": req.ContainerName, "ips": ips, "count": len(ips)})
}

// ReleaseIPPool 在本节点释放容器的一个公网 IP
func ReleaseIPPool(c *gin.Context) {
	version := c.Query("version")
	var req struct {
		ContainerName string `json:"container_name" binding:"required"`
		IPAddress     string `json:"ip_address" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.Error(c, 400, "参数错误: "+err.Error())
		return
	}
	var err error
	if version == "v6" {
		err = service.NewIPv6Service().ReleaseIPv6(req.ContainerName, req.IPAddress)
	} else {
		err = service.NewIPv4Service().ReleaseIPv4(req.ContainerName, req.IPAddress)
	}
	if err != nil {
		response.Error(c, 500, err.Error())
		return
	}
	response.Success(c, gin.H{"container": req.ContainerName, "ip": req.IPAddress})
}
