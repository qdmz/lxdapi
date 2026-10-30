package lxc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
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

// RemoteDoJSON 是 remoteDo 的导出包装，供 service/node 层调用节点上的
// 非容器类系统端点（端口映射、IP 池、网络 NAT、防火墙/反代插件等）。
// 本机客户端调用直接报错，避免误把远程下发当成本地执行。
func (c *Client) RemoteDoJSON(ctx context.Context, method, path string, body interface{}, out interface{}) error {
	if !c.ep.Remote {
		return fmt.Errorf("本机节点不走远程下发")
	}
	return c.remoteDo(ctx, method, path, body, out)
}

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
	// 客户端超时跟随 ctx 截止时间：镜像下载等长操作由调用方给足 ctx（如 900s），
	// 普通调用仍保持约 60 秒。
	timeout := 60 * time.Second
	if dl, ok := ctx.Deadline(); ok {
		if d := time.Until(dl) + 5*time.Second; d > timeout {
			timeout = d
		}
	}
	hc := &http.Client{Timeout: timeout}
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

// remoteTaskRef 节点侧任务提交/动作接口的返回（带任务 ID）。
type remoteTaskRef struct {
	TaskID uint `json:"task_id"`
}

// remoteTaskStatus 查询节点侧任务状态；ok=false 表示查不到（旧节点/端点异常），
// 调用方应退回容器可见性等待，不要据此判失败。
func (c *Client) remoteTaskStatus(ctx context.Context, taskID uint) (status, errMsg string, ok bool) {
	if taskID == 0 {
		return "", "", false
	}
	var data map[string]interface{}
	if err := c.remoteDo(ctx, "GET", fmt.Sprintf("/api/system/tasks/detail?id=%d", taskID), nil, &data); err != nil {
		return "", "", false
	}
	pick := func(keys ...string) string {
		for _, k := range keys {
			if v, isStr := data[k].(string); isStr && v != "" {
				return v
			}
		}
		return ""
	}
	status = pick("Status", "status")
	if status == "" {
		return "", "", false
	}
	return status, pick("ErrorMsg", "error_msg", "error"), true
}

// remoteWaitTask 等待节点侧异步任务（创建/重装）结束。
// 节点任务一旦失败立刻带节点错误返回——旧实现只等容器记录出现，节点中途失败时
// 主控要空等满 900 秒才判订单失败（2026-10-30 华为节点实测踩中）。
// 任务成功后以容器记录可见为完成标志；无任务 ID（旧节点）时退回纯可见性等待。
func (c *Client) remoteWaitTask(ctx context.Context, name string, taskID uint, timeout time.Duration) (*remoteContainer, error) {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for {
		st, em, ok := c.remoteTaskStatus(ctx, taskID)
		if ok && st == "failed" {
			if em == "" {
				em = "节点任务执行失败（无错误详情）"
			}
			return nil, fmt.Errorf("远程节点任务失败: %s", em)
		}
		rc, err := c.remoteGetContainer(ctx, name)
		if err == nil && rc != nil && rc.Name == name {
			if taskID == 0 || (ok && st == "success") {
				return rc, nil
			}
		} else {
			lastErr = err
		}
		if time.Now().After(deadline) {
			if lastErr != nil {
				return nil, fmt.Errorf("等待远程节点任务超时: %v", lastErr)
			}
			return nil, fmt.Errorf("等待远程节点任务超时")
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
	var sub remoteTaskRef
	if err := c.remoteDo(ctx, "POST", "/api/system/containers", p, &sub); err != nil {
		return err
	}
	// 等待窗须覆盖节点侧完整建容器耗时：慢节点（如 1 核小机器）实测可达 7 分钟，
	// 与节点 task timeout（900s）对齐，避免节点还在建、主控先判失败；
	// 节点任务失败则立刻带错误返回，不空等满窗。
	_, err := c.remoteWaitTask(ctx, p.Name, sub.TaskID, 900*time.Second)
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
	var sub remoteTaskRef
	if err := c.remoteDo(ctx, "POST", "/api/system/containers/"+name+"/action?action=reinstall", map[string]interface{}{"image": image}, &sub); err != nil {
		return err
	}
	// 节点重装为异步任务：给足时间并确认容器恢复可查询（慢节点同创建一样放宽到 900s）；
	// 任务失败立刻带节点错误返回。
	time.Sleep(10 * time.Second)
	_, err := c.remoteWaitTask(ctx, name, sub.TaskID, 900*time.Second)
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

// ---- 远程分支：镜像与存储池（节点系统级 API 见 internal/api/system/resource.go） ----

func (c *Client) remoteListImages(ctx context.Context) ([]ImageInfo, error) {
	var images []ImageInfo
	if err := c.remoteDo(ctx, "GET", "/api/system/images", nil, &images); err != nil {
		return nil, err
	}
	for i := range images {
		images[i].FillDisplayName()
	}
	return images, nil
}

// remoteListRemoteImages 经节点系统级 API 取该节点的远程可下载镜像列表
// （节点在本机执行 `lxc image list <remote>:` 并完成去重/易读名加工）。
func (c *Client) remoteListRemoteImages(ctx context.Context, remote string) ([]RemoteImage, error) {
	path := "/api/system/remote-images"
	if remote != "" {
		path += "?remote=" + url.QueryEscape(remote)
	}
	var images []RemoteImage
	if err := c.remoteDo(ctx, "GET", path, nil, &images); err != nil {
		return nil, err
	}
	return images, nil
}

func (c *Client) remoteGetImage(ctx context.Context, fingerprint string) (*ImageInfo, error) {
	var info ImageInfo
	if err := c.remoteDo(ctx, "GET", "/api/system/images/"+fingerprint, nil, &info); err != nil {
		return nil, err
	}
	info.FillDisplayName()
	return &info, nil
}

func (c *Client) remoteDeleteImage(ctx context.Context, fingerprint string) error {
	return c.remoteDo(ctx, "DELETE", "/api/system/images/"+fingerprint, nil, nil)
}

func (c *Client) remoteCopyImage(ctx context.Context, remote, image, alias string, copyAliases, public bool) error {
	body := map[string]interface{}{
		"remote": remote, "image": image, "alias": alias,
		"copy_aliases": copyAliases, "public": public,
	}
	return c.remoteDo(ctx, "POST", "/api/system/images/copy", body, nil)
}

func (c *Client) remoteImportImage(ctx context.Context, url, alias string, public bool) error {
	body := map[string]interface{}{"url": url, "alias": alias, "public": public}
	return c.remoteDo(ctx, "POST", "/api/system/images/import", body, nil)
}

func (c *Client) remotePublishImage(ctx context.Context, container, alias string, public bool, description string) error {
	body := map[string]interface{}{
		"container": container, "alias": alias, "public": public, "description": description,
	}
	return c.remoteDo(ctx, "POST", "/api/system/images/publish", body, nil)
}

func (c *Client) remoteCreateImageAlias(ctx context.Context, fingerprint, name string) error {
	body := map[string]interface{}{"fingerprint": fingerprint, "name": name}
	return c.remoteDo(ctx, "POST", "/api/system/images/alias", body, nil)
}

func (c *Client) remoteDeleteImageAlias(ctx context.Context, name string) error {
	body := map[string]interface{}{"name": name}
	return c.remoteDo(ctx, "POST", "/api/system/images/alias/delete", body, nil)
}

// remoteStoragePool 节点返回的存储池（含资源用量）
type remoteStoragePool struct {
	StoragePoolInfo
	Total int64 `json:"total"`
	Used  int64 `json:"used"`
}

func (c *Client) remoteListStoragePools(ctx context.Context) ([]StoragePoolInfo, error) {
	var pools []remoteStoragePool
	if err := c.remoteDo(ctx, "GET", "/api/system/storage-pools", nil, &pools); err != nil {
		return nil, err
	}
	out := make([]StoragePoolInfo, len(pools))
	for i, p := range pools {
		out[i] = p.StoragePoolInfo
	}
	return out, nil
}

func (c *Client) remoteGetStoragePoolResources(ctx context.Context, name string) (*StoragePoolResources, error) {
	var pools []remoteStoragePool
	if err := c.remoteDo(ctx, "GET", "/api/system/storage-pools", nil, &pools); err != nil {
		return nil, err
	}
	for _, p := range pools {
		if p.Name == name {
			var res StoragePoolResources
			res.Space.Total = p.Total
			res.Space.Used = p.Used
			return &res, nil
		}
	}
	return nil, fmt.Errorf("远程节点不存在存储池: %s", name)
}
