package console

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"

	"lxdapi/internal/lxc"
)

type TokenInfo struct {
	ContainerName string
	Endpoint      lxc.Endpoint // 容器所属节点的 LXD 端点（本机为空 Endpoint）
	CreatedAt     time.Time
	Used          bool
}

var (
	tokens = make(map[string]*TokenInfo)
	mu     sync.RWMutex
)

// GenerateToken 为本机节点容器生成令牌
func GenerateToken(containerName string) (string, error) {
	return GenerateTokenWithEndpoint(containerName, lxc.Endpoint{})
}

// GenerateTokenWithEndpoint 为指定节点上的容器生成令牌（控制台/SFTP 会话按此端点路由）
func GenerateTokenWithEndpoint(containerName string, endpoint lxc.Endpoint) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	token := hex.EncodeToString(b)

	mu.Lock()
	tokens[token] = &TokenInfo{
		ContainerName: containerName,
		Endpoint:      endpoint,
		CreatedAt:     time.Now(),
		Used:          false,
	}
	mu.Unlock()

	go func() {
		time.Sleep(10 * time.Minute)
		mu.Lock()
		delete(tokens, token)
		mu.Unlock()
	}()

	return token, nil
}

// ValidateToken 校验令牌并返回容器名与其节点端点
func ValidateToken(token string) (string, lxc.Endpoint, bool, string) {
	mu.RLock()
	defer mu.RUnlock()

	info, exists := tokens[token]
	if !exists {
		return "", lxc.Endpoint{}, false, "令牌无效或已过期"
	}

	if info.Used {
		return "", lxc.Endpoint{}, false, "令牌已被使用，无法重复访问"
	}

	if time.Since(info.CreatedAt) > 10*time.Minute {
		return "", lxc.Endpoint{}, false, "令牌已过期（有效期10分钟）"
	}

	return info.ContainerName, info.Endpoint, true, ""
}

// ValidateAndConsume 校验并消费令牌（一次性），返回容器名与其节点端点
func ValidateAndConsume(token string) (string, lxc.Endpoint, bool) {
	mu.Lock()
	defer mu.Unlock()

	info, exists := tokens[token]
	if !exists {
		return "", lxc.Endpoint{}, false
	}

	if info.Used {
		return "", lxc.Endpoint{}, false
	}

	if time.Since(info.CreatedAt) > 10*time.Minute {
		delete(tokens, token)
		return "", lxc.Endpoint{}, false
	}

	// 一次性令牌使用后删除
	delete(tokens, token)

	return info.ContainerName, info.Endpoint, true
}
