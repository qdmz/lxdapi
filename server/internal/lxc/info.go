package lxc

import (
	"context"
	"encoding/json"
	"fmt"
)

type ContainerInfo struct {
	Name         string                 `json:"name"`
	Status       string                 `json:"status"`
	Type         string                 `json:"type"`
	Architecture string                 `json:"architecture"`
	PID          int                    `json:"pid"`
	Created      string                 `json:"created_at"`
	LastUsed     string                 `json:"last_used_at"`
	Config       map[string]interface{} `json:"config"`
	Devices      map[string]interface{} `json:"devices"`
	State        *ContainerState        `json:"state"`
}

type ContainerState struct {
	Status     string                 `json:"status"`
	StatusCode int                    `json:"status_code"`
	Disk       map[string]interface{} `json:"disk"`
	Memory     map[string]interface{} `json:"memory"`
	Network    map[string]interface{} `json:"network"`
	Pid        int                    `json:"pid"`
	Processes  int                    `json:"processes"`
	CPU        map[string]interface{} `json:"cpu"`
}

func (c *Client) GetContainerInfo(ctx context.Context, name string) (*ContainerInfo, error) {
	if c.ep.Remote {
		rc, err := c.remoteGetContainer(ctx, name)
		if err != nil {
			return nil, err
		}
		info := &ContainerInfo{
			Name:   rc.Name,
			Status: mapRemoteStatus(rc.Status),
			Config: map[string]interface{}{"image.description": rc.Image},
		}
		// 把节点回传的内网 IP 合成为 eth0 地址，供缓存刷新等按本地结构读取的代码使用
		if rc.PrivateIP != "" || rc.PrivateIPv6 != "" {
			addrs := make([]interface{}, 0, 2)
			if rc.PrivateIP != "" {
				addrs = append(addrs, map[string]interface{}{"family": "inet", "address": rc.PrivateIP})
			}
			if rc.PrivateIPv6 != "" {
				addrs = append(addrs, map[string]interface{}{"family": "inet6", "address": rc.PrivateIPv6})
			}
			info.State = &ContainerState{Network: map[string]interface{}{
				"eth0": map[string]interface{}{"addresses": addrs},
			}}
		}
		return info, nil
	}
	output, err := c.exec(ctx, "query", fmt.Sprintf("/1.0/instances/%s?recursion=2", name))
	if err != nil {
		return nil, fmt.Errorf("获取容器信息失败: %v", err)
	}

	var info ContainerInfo
	if err := json.Unmarshal([]byte(output), &info); err != nil {
		return nil, fmt.Errorf("解析容器信息失败: %v", err)
	}

	stateOutput, err := c.exec(ctx, "query", fmt.Sprintf("/1.0/instances/%s/state", name))
	if err == nil {
		var state ContainerState
		if err := json.Unmarshal([]byte(stateOutput), &state); err == nil {
			info.State = &state
		}
	}

	return &info, nil
}

func (c *Client) ListAllContainers(ctx context.Context) ([]string, error) {
	output, err := c.exec(ctx, "list", "--format=json")
	if err != nil {
		return nil, err
	}

	var containers []struct {
		Name string `json:"name"`
	}
	
	if err := json.Unmarshal([]byte(output), &containers); err != nil {
		return nil, err
	}

	names := make([]string, len(containers))
	for i, c := range containers {
		names[i] = c.Name
	}

	return names, nil
}

// Info 获取 LXD 服务器信息，用于节点连通性测试。
// 连接失败（含认证失败）时返回错误。
func (c *Client) Info(ctx context.Context) error {
	_, err := c.exec(ctx, "info")
	return err
}
