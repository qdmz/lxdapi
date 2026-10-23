package lxc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 远程节点传输层：当节点为远程节点时，容器操作不再走本地 lxc CLI
// （lxc CLI 不识别 LXD_HOST/LXD_PORT 环境变量，历史上会静默落到本机执行），
// 而是调用节点自身 lxdapi 的系统级 API（/api/system/*，X-API-Hash 鉴权），
// 由节点在本机完成实际的 LXD 操作。

// RemoteCreateParams 远程创建参数，与节点 /api/system/containers 的请求体对应
// （即 models.CreateContainerRequest 的字段，NodeID 恒为 0 不下发）。
type RemoteCreateParams struct {
	Name              string `json:"name"`
	Password          string `json:"password"`
	Image             string `json:"image"`
	CPU               int    `json:"cpu"`
	Memory            int    `json:"memory"` // MB
	Disk              int    `json:"disk"`   // MB
	Ingress           int    `json:"ingress"`
	Egress            int    `json:"egress"`
	TrafficLimit      int    `json:"traffic_limit"`
	AllowNesting      bool   `json:"allow_nesting"`
	MemorySwap        bool   `json:"memory_swap"`
	Privileged        bool   `json:"privileged"`
	Username          string `json:"username"`
	Remark            string `json:"remark"`
	IPv4PoolLimit     int    `json:"ipv4_pool_limit"`
	IPv4MappingLimit  int    `json:"ipv4_mapping_limit"`
	IPv6PoolLimit     int    `json:"ipv6_pool_limit"`
	IPv6MappingLimit  int    `json:"ipv6_mapping_limit"`
	ReverseProxyLimit int    `json:"reverse_proxy_limit"`
	CPUAllowance      int    `json:"cpu_allowance"`
	IORead            int    `json:"io_read"`
	IOWrite           int    `json:"io_write"`
	ProcessesLimit    int    `json:"processes_limit"`
}

// remoteContainer 节点 API 返回的容器记录（节点侧 models.Container 的序列化子集）
type remoteContainer struct {
	Name        string `json:"Name"`
	Status      string `json:"Status"`
	Image       string `json:"Image"`
	PrivateIP   string `json:"PrivateIP"`
	PrivateIPv6 string `json:"PrivateIPv6"`
	MacAddress  string `json:"MacAddress"`
}

type remoteEnvelope struct {
	Code int             `json:"code"`
	Msg  string          `json:"msg"`
	Data json.RawMessage `json:"data"`
}

// RemoteBaseURL 由节点 host/port 构造节点 API 基础地址。
// host 可填域名（如 lxdapinode1.example.com，默认 https 443）或带 scheme 的完整地址。
func RemoteBaseURL(host string, port int) string {
	h := strings.TrimSuffix(strings.TrimSpace(host), "/")
	if strings.HasPrefix(h, "http://") || strings.HasPrefix(h, "https://") {
		if port > 0 && !strings.Contains(strings.TrimPrefix(strings.TrimPrefix(h, "http://"), "https://"), ":") {
			// host 带 scheme 但没写端口时补上非默认端口
			if !(strings.HasPrefix(h, "https://") && port == 443) && !(strings.HasPrefix(h, "http://") && port == 80) {
				h = fmt.Sprintf("%s:%d", h, port)
			}
		}
		return h
	}
	if port > 0 && port != 443 {
		return fmt.Sprintf("https://%s:%d", h, port)
	}
	return "https://" + h
}

func (c *Client) remoteBaseURL() string { return RemoteBaseURL(c.ep.Host, c.ep.Port) }

// remoteDo 向节点系统级 API 发起请求并解开统一响应信封。
func (c *Client) remoteDo(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	var reader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.remoteBaseURL()+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.ep.APIHash != "" {
		req.Header.Set("X-API-Hash", c.ep.APIHash)
	}
	hc := &http.Client{Timeout: 60 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return fmt.Errorf("远程节点请求失败: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	var env remoteEnvelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("远程节点响应无法解析 (HTTP %d): %s", resp.StatusCode, string(raw))
	}
	if env.Code != 200 {
		if env.Msg != "" {
			return fmt.Errorf("远程节点返回错误: %s", env.Msg)
		}
		return fmt.Errorf("远程节点返回错误码: %d", env.Code)
	}
	if out != nil && len(env.Data) > 0 && string(env.Data) != "null" {
		if err := json.Unmarshal(env.Data, out); err != nil {
			return fmt.Errorf("远程节点数据解析失败: %v", err)
		}
	}
	return nil
}

// PingRemoteNode 探测远程节点系统级 API 是否可达且密钥正确。
func PingRemoteNode(ctx context.Context, host string, port int, apiHash string) error {
	probe := &Client{ep: Endpoint{Host: host, Port: port, Remote: true, APIHash: apiHash}}
	return probe.remoteDo(ctx, "GET", "/api/system/containers?limit=1", nil, nil)
}

func (c *Client) remoteGetContainer(ctx context.Context, name string) (*remoteContainer, error) {
	var data struct {
		Container remoteContainer `json:"container"`
	}
	if err := c.remoteDo(ctx, "GET", "/api/system/containers/"+name, nil, &data); err != nil {
		return nil, err
	}
	return &data.Container, nil
}

func (c *Client) remoteAction(ctx context.Context, name, action string, body interface{}) error {
	return c.remoteDo(ctx, "POST", "/api/system/containers/"+name+"/action?action="+action, body, nil)
}

// mapRemoteStatus 把节点侧状态（running/stopped）映射成本地 lxc 的状态写法（Running/Stopped）。
func mapRemoteStatus(s string) string {
	switch strings.ToLower(s) {
	case "running":
		return "Running"
	case "stopped":
		return "Stopped"
	case "frozen":
		return "Frozen"
	default:
		return s
	}
}

// remoteWaitContainer 轮询等待节点侧容器记录出现（节点创建是异步任务，记录在创建末尾落库）。
func (c *Client) remoteWaitContainer(ctx context.Context, name string, timeout time.Duration) (*remoteContainer, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		rc, err := c.remoteGetContainer(ctx, name)
		if err == nil && rc != nil && rc.Name == name {
			return rc, nil
		}
		lastErr = err
		if time.Now().After(deadline) {
			if lastErr != nil {
				return nil, fmt.Errorf("等待远程节点创建容器超时: %v", lastErr)
			}
			return nil, fmt.Errorf("等待远程节点创建容器超时")
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
}

// parseUnitInt 把 "512MB" / "100Mbit" / "50%" / "10240" 这类本地格式解析回整数。
func parseUnitInt(s string) int {
	s = strings.TrimSpace(s)
	s = strings.TrimSuffix(s, "Mbit")
	s = strings.TrimSuffix(s, "MB")
	s = strings.TrimSuffix(s, "%")
	if s == "" {
		return 0
	}
	v, _ := strconv.Atoi(s)
	return v
}

// ---- 远程分支实现（container.go / info.go 的方法在 ep.Remote 时调用） ----

// remoteCreateWithConfig 把创建请求下发到节点执行，并等待节点侧记录落库。
// 优先使用 service 层注入的完整参数（含用户名/密码）；缺失时用分解参数合成。
func (c *Client) remoteCreateWithConfig(ctx context.Context, name, image string, cpu int, memory, disk, ingress, egress, cpuAllowance, ioRead, ioWrite string, allowNesting, memorySwap, privileged bool, processesLimit int) error {
	p := c.remoteCreate
	if p == nil {
		p = &RemoteCreateParams{
			Name: name, Image: image, CPU: cpu,
			Memory: parseUnitInt(memory), Disk: parseUnitInt(disk),
			Ingress: parseUnitInt(ingress), Egress: parseUnitInt(egress),
			CPUAllowance: parseUnitInt(cpuAllowance),
			IORead:       parseUnitInt(ioRead), IOWrite: parseUnitInt(ioWrite),
			AllowNesting: allowNesting, MemorySwap: memorySwap, Privileged: privileged,
			ProcessesLimit: processesLimit,
		}
	}
	if err := c.remoteDo(ctx, "POST", "/api/system/containers", p, nil); err != nil {
		return err
	}
	// 等待窗须覆盖节点侧完整建容器耗时：慢节点（如 1 核小机器）实测可达 7 分钟，
	// 与节点 task timeout（900s）对齐，避免节点还在建、主控先判失败。
	_, err := c.remoteWaitContainer(ctx, p.Name, 900*time.Second)
	return err
}

func (c *Client) remoteStart(ctx context.Context, name string) error {
	if rc, err := c.remoteGetContainer(ctx, name); err == nil && strings.EqualFold(rc.Status, "running") {
		return nil
	}
	err := c.remoteAction(ctx, name, "start", nil)
	if err != nil && (strings.Contains(err.Error(), "already") || strings.Contains(err.Error(), "已在运行") || strings.Contains(err.Error(), "已经运行")) {
		return nil
	}
	return err
}

func (c *Client) remoteStop(ctx context.Context, name string) error {
	if rc, err := c.remoteGetContainer(ctx, name); err == nil && strings.EqualFold(rc.Status, "stopped") {
		return nil
	}
	return c.remoteAction(ctx, name, "stop", nil)
}

func (c *Client) remoteDelete(ctx context.Context, name string) error {
	if err := c.remoteDo(ctx, "DELETE", "/api/system/containers/"+name, nil, nil); err != nil {
		if _, gerr := c.remoteGetContainer(ctx, name); gerr != nil {
			return nil // 已不存在，删除幂等
		}
		return err
	}
	// 节点删除是异步任务：等待节点侧记录消失（最多约 2 分钟，超时不报错，节点会继续完成）
	deadline := time.Now().Add(120 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := c.remoteGetContainer(ctx, name); err != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(3 * time.Second):
		}
	}
	return nil
}

func (c *Client) remoteRebuild(ctx context.Context, name, image string) error {
	if err := c.remoteAction(ctx, name, "reinstall", map[string]interface{}{"image": image}); err != nil {
		return err
	}
	// 节点重装为异步任务：给足时间并确认容器恢复可查询（慢节点同创建一样放宽到 900s）
	time.Sleep(10 * time.Second)
	_, err := c.remoteWaitContainer(ctx, name, 900*time.Second)
	return err
}

// remoteUpdateConfigBody 与节点侧 UpdateContainerConfigRequest 对应的请求体（指针为 nil 表示不修改）。
type remoteUpdateConfigBody struct {
	CPU            *int `json:"cpu"`
	Memory         *int `json:"memory"`
	Disk           *int `json:"disk"`
	Ingress        *int `json:"ingress"`
	Egress         *int `json:"egress"`
	CPUAllowance   *int `json:"cpu_allowance"`
	IORead         *int `json:"io_read"`
	IOWrite        *int `json:"io_write"`
	ProcessesLimit *int `json:"processes_limit"`
}

func intPtr(v int) *int { return &v }

func (c *Client) remoteUpdateConfig(ctx context.Context, name string, cpu int, memory, disk, ingress, egress, cpuAllowance, ioRead, ioWrite string, processesLimit int) error {
	body := &remoteUpdateConfigBody{}
	if cpu > 0 {
		body.CPU = intPtr(cpu)
	}
	if v := parseUnitInt(memory); v > 0 {
		body.Memory = intPtr(v)
	}
	if v := parseUnitInt(disk); v > 0 {
		body.Disk = intPtr(v)
	}
	if v := parseUnitInt(ingress); v > 0 {
		body.Ingress = intPtr(v)
	}
	if v := parseUnitInt(egress); v > 0 {
		body.Egress = intPtr(v)
	}
	if v := parseUnitInt(cpuAllowance); v > 0 {
		body.CPUAllowance = intPtr(v)
	}
	if v := parseUnitInt(ioRead); v > 0 {
		body.IORead = intPtr(v)
	}
	if v := parseUnitInt(ioWrite); v > 0 {
		body.IOWrite = intPtr(v)
	}
	if processesLimit > 0 {
		body.ProcessesLimit = intPtr(processesLimit)
	}
	return c.remoteDo(ctx, "PUT", "/api/system/containers/"+name+"/config", body, nil)
}

func (c *Client) remoteClearNICLimits(ctx context.Context, name string) error {
	body := &remoteUpdateConfigBody{Ingress: intPtr(0), Egress: intPtr(0)}
	return c.remoteDo(ctx, "PUT", "/api/system/containers/"+name+"/config", body, nil)
}
