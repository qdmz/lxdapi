package lxc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"lxdapi/internal/core"
	"lxdapi/pkg/logger"
	"os"
	"os/exec"
	"strings"
	"time"
)

// Endpoint 描述一个节点的接入端点。
// 本机节点 host 为空，走本地 socket；远程节点通过 host:port 指定节点 lxdapi 的
// 系统级 API 端点（见 remote.go），容器操作经 HTTP 下发到节点本机执行。
// 注意：lxc CLI 不识别 LXD_HOST/LXD_PORT 环境变量，远程节点禁止再走本地 CLI。
type Endpoint struct {
	Host    string // 节点 API 主机（域名），本机节点为空
	Port    int    // 节点 API 端口，本机节点为 0（使用本地 socket）
	Remote  bool   // 是否远程节点
	APIHash string // 远程节点系统级 API 密钥（X-API-Hash）
}

// NodeEndpoint 从节点信息构造 LXD 接入端点。nodeID==0 或 host 为空表示本机默认节点。
func NodeEndpoint(host string, apiPort int) Endpoint {
	if host == "" {
		return Endpoint{}
	}
	return Endpoint{Host: host, Port: apiPort, Remote: true}
}

type Client struct {
	bin     string
	socket  string
	timeout time.Duration
	ep      Endpoint

	// remoteCreate 远程创建参数（仅远程节点使用）：本地 CreateContainerWithConfig
	// 的分解参数不含用户名/密码，下发节点前由 service 层注入完整创建请求。
	remoteCreate *RemoteCreateParams
}

// SetRemoteCreate 注入远程节点的完整创建参数。
func (c *Client) SetRemoteCreate(p *RemoteCreateParams) { c.remoteCreate = p }

// NewClient 创建默认（本机）LXD 客户端，走本地 socket。
func NewClient() *Client {
	cfg := core.GlobalConfig.LXC
	bin := cfg.Bin
	if bin == "" {
		bin = "lxc"
	}
	return &Client{
		bin:     bin,
		socket:  cfg.Socket,
		timeout: time.Duration(cfg.Timeout) * time.Second,
	}
}

// NewRemoteClient 创建指向特定远程节点的客户端，操作经节点系统级 API 下发。
// host 为空时退化为本地 socket 客户端。
func NewRemoteClient(host string, port int, apiHash string) *Client {
	base := NewClient()
	base.ep = NodeEndpoint(host, port)
	base.ep.APIHash = apiHash
	return base
}

// Endpoint 返回该客户端的目标端点。
func (c *Client) Endpoint() Endpoint { return c.ep }

// IsRemote 是否远程节点。
func (c *Client) IsRemote() bool { return c.ep.Remote }

// execEnv 根据端点构造 lxc CLI 所需的环境变量，用于按次路由到远程 LXD。
func (c *Client) execEnv() []string {
	env := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
	}
	if c.ep.Remote {
		env = append(env,
			"LXD_HOST="+c.ep.Host,
			fmt.Sprintf("LXD_PORT=%d", c.ep.Port),
			"LXD_SSL=true",
			"LXD_CERT=",
		)
	}
	// 本地节点显式指定 socket，避免使用 ~/.config/lxc 中的默认 remote
	if !c.ep.Remote && c.socket != "" {
		env = append(env, "LXD_SOCKET="+c.socket)
	}
	return env
}

func (c *Client) exec(ctx context.Context, args ...string) (string, error) {
	if c.ep.Remote {
		// 安全兜底：远程节点未实现远程分支的方法必须显式失败，
		// 绝不允许退回本地 CLI 在主控本机误执行。
		return "", fmt.Errorf("远程节点不支持本地 LXD 命令: %s", strings.Join(args, " "))
	}
	cmd := exec.CommandContext(ctx, c.bin, args...)
	cmd.Env = c.execEnv()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	tag := "lxc"
	if c.ep.Remote {
		tag = fmt.Sprintf("lxc(节点 %s:%d)", c.ep.Host, c.ep.Port)
	}
	logger.Info("执行LXC命令: %s %s", tag, strings.Join(args, " "))

	err := cmd.Run()
	if err != nil {
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = err.Error()
		}
		logger.Error("LXC命令执行失败 [%s]: %s", tag, errMsg)
		return "", fmt.Errorf("%s", errMsg)
	}

	return stdout.String(), nil
}

func (c *Client) execJSON(ctx context.Context, result interface{}, args ...string) error {
	output, err := c.exec(ctx, append(args, "--format=json")...)
	if err != nil {
		return err
	}
	return json.Unmarshal([]byte(output), result)
}

// ExecCommand 构建一条 `lxc exec <container> <args...>` 命令，按给定端点路由。
// 用于 SFTP / 终端等需要直接拿 *exec.Cmd 的场景。endpoint 为 zero value 时走本机。
// 注意：不再自动插入 "--"，需要 lxc 选项（如 -t/--env）的调用方自行在 args 里加 "--"。
func ExecCommand(ctx context.Context, endpoint Endpoint, containerName string, args ...string) *exec.Cmd {
	bin := core.GlobalConfig.LXC.Bin
	if bin == "" {
		bin = "lxc"
	}
	// 临时构造一个 client 以复用 execEnv 路由逻辑
	// 这里用 client 的 execEnv 生成环境变量
	cli := &Client{
		bin:     bin,
		socket:  core.GlobalConfig.LXC.Socket,
		timeout: time.Duration(core.GlobalConfig.LXC.Timeout) * time.Second,
		ep:      endpoint,
	}
	cmdArgs := append([]string{"exec", containerName}, args...)
	cmd := exec.CommandContext(ctx, bin, cmdArgs...)
	cmd.Env = cli.execEnv()
	return cmd
}
