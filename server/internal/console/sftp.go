package console

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	pathpkg "path"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pkg/sftp"
	"lxdapi/internal/lxc"
	"lxdapi/pkg/logger"
)

const (
	sftpChunkSize     = 64 * 1024
	sftpProgressEvery = 64 * 1024
	sftpIdleTimeout   = 5 * time.Minute
)

// sftpServerPaths are the usual locations of the sftp-server binary inside
// containers. They are checked in order and the first executable one wins.
var sftpServerPaths = []string{
	"/usr/lib/openssh/sftp-server",
	"/usr/libexec/openssh/sftp-server",
	"/usr/lib/ssh/sftp-server",
}

// wsConn bridges a *websocket.Conn to net.Conn so that an SFTP client can talk
// over a WebSocket. Each WebSocket text message carries exactly one JSON packet.
type wsConn struct {
	conn *websocket.Conn
	mu   sync.Mutex
	buf  *bytes.Buffer
}

func newWsConn(conn *websocket.Conn) *wsConn {
	return &wsConn{conn: conn, buf: bytes.NewBuffer(nil)}
}

func (c *wsConn) Read(b []byte) (int, error) {
	for c.buf.Len() == 0 {
		_, msg, err := c.conn.ReadMessage()
		if err != nil {
			return 0, err
		}
		c.buf.Write(msg)
	}
	return c.buf.Read(b)
}

func (c *wsConn) Write(b []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.conn.WriteMessage(websocket.TextMessage, b); err != nil {
		return 0, err
	}
	return len(b), nil
}

// errUnknownAction marks a session teardown caused by an unrecognized action.
// HandleSFTP returns it wrapped with the offending action name.
var errUnknownAction = errors.New("未知的操作")

func (c *wsConn) Close() error                     { return c.conn.Close() }
func (c *wsConn) LocalAddr() net.Addr              { return wsAddr{"local"} }
func (c *wsConn) RemoteAddr() net.Addr             { return wsAddr{"remote"} }
func (c *wsConn) SetDeadline(time.Time) error      { return nil }
func (c *wsConn) SetReadDeadline(time.Time) error  { return nil }
func (c *wsConn) SetWriteDeadline(time.Time) error { return nil }

type wsAddr struct{ tag string }

func (a wsAddr) Network() string { return "ws" }
func (a wsAddr) String() string  { return a.tag }

// SFTPRequest is a client -> server message.
type SFTPRequest struct {
	ID      int64    `json:"id"`
	Action  string   `json:"action"`
	Path    string   `json:"path"`
	Offset  int64    `json:"offset"`
	Data    string   `json:"data"`
	Content string   `json:"content"`
	Size    int64    `json:"size"`
	Perm    string   `json:"perm"`
	NewPath string   `json:"new_path"` // rename/copy/move 目标路径
	Name    string   `json:"name"`     // archive 输出文件名
	Names   []string `json:"names"`    // archive 待打包的条目名（相对 Path 目录）
	Format  string   `json:"format"`   // archive 格式: tar.gz / zip
	UID     int      `json:"uid"`      // chown
	GID     int      `json:"gid"`      // chown
}

// SFTPResponse is a server -> client message. Mirrors the request id so that
// the frontend can match replies to the requests that drove them.
type SFTPResponse struct {
	ID      int64          `json:"id"`
	Action  string         `json:"action"`
	OK      bool           `json:"ok"`
	Error   string         `json:"error"`
	Files   []SFTPFileInfo `json:"files,omitempty"`
	Path    string         `json:"path"`
	Offset  int64          `json:"offset"`
	Data    string         `json:"data"`
	Size    int64          `json:"size"`
	Percent int            `json:"percent"`
}

// SFTPFileInfo describes a single remote entry.
type SFTPFileInfo struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	ModTime   string `json:"modTime"`
	Perm      string `json:"perm"`
	PermOctal string `json:"permOctal"`
	Type      string `json:"type"`
}

func send(netConn net.Conn, resp SFTPResponse) error {
	return json.NewEncoder(netConn).Encode(resp)
}

// sendError replies with ok=false and echoes the request id so that clients
// can match the failure to the request that produced it.
func sendError(netConn net.Conn, req SFTPRequest, err error) {
	_ = send(netConn, SFTPResponse{ID: req.ID, Action: req.Action, Error: err.Error()})
}

func respond(netConn net.Conn, req SFTPRequest, resp SFTPResponse) error {
	resp.ID = req.ID
	return send(netConn, resp)
}

// HandleSFTP runs the SFTP file-management protocol over a WebSocket.
//
// Rather than requiring an SSH daemon inside the container it execs the
// container's own sftp-server binary through LXC and drives it through
// github.com/pkg/sftp's NewClientPipe. This avoids having to store or transmit
// container root passwords and works for every container that ships OpenSSH's
// sftp-server (Ubuntu and Debian images ship it out of the box).
func HandleSFTP(conn *websocket.Conn, containerName string) error {
	return HandleSFTPWithEndpoint(conn, containerName, lxc.Endpoint{})
}

// HandleSFTPWithEndpoint 在指定节点的容器上建立 SFTP 会话
func HandleSFTPWithEndpoint(conn *websocket.Conn, containerName string, endpoint lxc.Endpoint) error {
	netConn := newWsConn(conn)
	defer netConn.Close()

	// 很多精简镜像（如官方 Alpine）默认不带 sftp-server，找不到时尝试
	// 按容器包管理器自动安装，装完再定位一次。
	server, err := resolveSFTPServer(containerName, endpoint)
	if err != nil {
		logger.Warn("SFTP %s: %v，尝试自动安装", containerName, err)
		installCtx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
		if ierr := installSFTPServer(installCtx, containerName, endpoint); ierr != nil {
			cancel()
			err = fmt.Errorf("容器 SFTP 不可用: 未找到 sftp-server，且自动安装失败: %v", ierr)
			logger.Warn("SFTP %s: %v", containerName, err)
			sendError(netConn, SFTPRequest{Action: "list"}, err)
			return err
		}
		cancel()
		if server, err = resolveSFTPServer(containerName, endpoint); err != nil {
			err = fmt.Errorf("容器 SFTP 不可用: 安装后仍未找到 sftp-server: %v", err)
			logger.Warn("SFTP %s: %v", containerName, err)
			sendError(netConn, SFTPRequest{Action: "list"}, err)
			return err
		}
	}

	cmd := lxc.ExecCommand(context.Background(), endpoint, containerName, "--", server)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		err = fmt.Errorf("创建 stdin 管道失败: %v", err)
		sendError(netConn, SFTPRequest{Action: "list"}, err)
		return err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		stdin.Close()
		err = fmt.Errorf("创建 stdout 管道失败: %v", err)
		sendError(netConn, SFTPRequest{Action: "list"}, err)
		stdout.Close()
		return err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		stdin.Close()
		stdout.Close()
		err = fmt.Errorf("创建 stderr 管道失败: %v", err)
		sendError(netConn, SFTPRequest{Action: "list"}, err)
		stderr.Close()
		return err
	}

	if err := cmd.Start(); err != nil {
		stdin.Close()
		stdout.Close()
		stderr.Close()
		err = fmt.Errorf("启动容器 SFTP 服务失败: %v", err)
		logger.Warn("SFTP %s: 启动 sftp-server 失败: %v", containerName, err)
		sendError(netConn, SFTPRequest{Action: "list"}, err)
		return err
	}

	// sftp.NewClientPipe never exposes the writer it holds, so we close the
	// stdin pipe ourselves once the client disconnects. That is what gives the
	// subprocess an EOF and makes it exit, so the reaping goroutine does not
	// leak when the session ends.
	waited := make(chan struct{})
	go func() {
		defer close(waited)
		if err := cmd.Wait(); err != nil {
			logger.Warn("SFTP %s: sftp-server 退出: %v", containerName, err)
		}
	}()

	client, err := sftp.NewClientPipe(stdout, stdin, sftp.CopyStderrTo(io.Discard))
	if err != nil {
		stdin.Close()
		stdout.Close()
		stderr.Close()
		err = fmt.Errorf("建立 SFTP 会话失败: %v", err)
		logger.Warn("SFTP %s: 创建 SFTP 客户端失败: %v", containerName, err)
		sendError(netConn, SFTPRequest{Action: "list"}, err)
		return err
	}

	logger.Info("SFTP 会话建立成功: %s", containerName)

	dec := json.NewDecoder(netConn)
	deadline := time.Now().Add(sftpIdleTimeout)
	for {
		if err := netConn.SetReadDeadline(deadline); err != nil {
			logger.Warn("SFTP %s: 设置读超时失败: %v", containerName, err)
		}

		var req SFTPRequest
		if err := dec.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				logger.Info("SFTP %s: 客户端断开", containerName)
				stdin.Close()
				<-waited
				return nil
			}
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				logger.Info("SFTP %s: 会话空闲超时", containerName)
				stdin.Close()
				<-waited
				return nil
			}
			logger.Warn("SFTP %s: 解析请求失败: %v", containerName, err)
			stdin.Close()
			<-waited
			return fmt.Errorf("解析请求失败: %w", err)
		}
		deadline = time.Now().Add(sftpIdleTimeout)

		switch req.Action {
		case "list":
			handleList(req, netConn, client, req.Path)
		case "stat":
			handleStat(req, netConn, client, req.Path)
		case "mkdir":
			handleMkdir(req, netConn, client, req.Path, req.Perm)
		case "delete":
			handleDelete(req, netConn, client, containerName, endpoint, req.Path)
		case "create_file":
			handleCreateFile(req, netConn, client, req.Path)
		case "rename":
			handleRename(req, netConn, client, req.Path, req.NewPath)
		case "chmod":
			handleChmod(req, netConn, client, req.Path, req.Perm)
		case "chown":
			handleChown(req, netConn, client, req.Path, req.UID, req.GID)
		case "copy":
			handleCopy(req, netConn, containerName, endpoint, req.Path, req.NewPath)
		case "move":
			handleMove(req, netConn, containerName, endpoint, req.Path, req.NewPath)
		case "archive":
			handleArchive(req, netConn, containerName, endpoint, req)
		case "extract":
			handleExtract(req, netConn, containerName, endpoint, req.Path, req.NewPath)
		case "upload":
			handleUpload(req, dec, netConn, client, req.Path, req.Size)
		case "download":
			handleDownload(req, netConn, client, req.Path)
		case "edit":
			handleEdit(req, netConn, client, req.Path, req.Content)
		case "ping":
			_ = respond(netConn, req, SFTPResponse{Action: "pong", OK: true})
		default:
			logger.Warn("SFTP %s: 未知操作 %s", containerName, req.Action)
			sendError(netConn, req, errors.New("未知的操作: "+req.Action))
			return fmt.Errorf("%w: %s", errUnknownAction, req.Action)
		}
	}
}

func resolveSFTPServer(containerName string, endpoint lxc.Endpoint) (string, error) {
	for _, path := range sftpServerPaths {
		cmd := lxc.ExecCommand(context.Background(), endpoint, containerName, "--", "test", "-x", path)
		if cmd.Run() == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("容器 SFTP 不可用: 未找到 sftp-server 二进制文件")
}

// installSFTPServer 在容器内自动安装 OpenSSH 的 sftp-server。
// 按包管理器选择命令；失败则返回错误（调用方转成友好提示）。
func installSFTPServer(ctx context.Context, containerName string, endpoint lxc.Endpoint) error {
	pmCmd := []struct {
		pm      string
		install string
	}{
		{"apk", "apk add --no-cache openssh-sftp-server"},
		{"apt-get", "apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq openssh-sftp-server"},
		{"dnf", "dnf install -y -q openssh-server"},
		{"yum", "yum install -y -q openssh-server"},
	}
	for _, p := range pmCmd {
		probe := lxc.ExecCommand(ctx, endpoint, containerName, "--", "sh", "-c", "command -v "+p.pm+" >/dev/null 2>&1")
		if probe.Run() != nil {
			continue
		}
		logger.Info("SFTP %s: 用 %s 自动安装 sftp-server", containerName, p.pm)
		cmd := lxc.ExecCommand(ctx, endpoint, containerName, "--", "sh", "-c", p.install)
		out, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("用 %s 安装失败: %v(%s)", p.pm, err, strings.TrimSpace(string(out)))
		}
		logger.Info("SFTP %s: sftp-server 安装成功(%s)", containerName, p.pm)
		return nil
	}
	return fmt.Errorf("容器内没有已知的包管理器(apk/apt-get/dnf/yum)")
}

func handleList(req SFTPRequest, netConn net.Conn, client *sftp.Client, path string) {
	if path == "" {
		path = "/"
	}
	entries, err := client.ReadDir(path)
	if err != nil {
		logger.Warn("SFTP 列举目录失败: %v", err)
		sendError(netConn, req, fmt.Errorf("列举目录失败: %w", err))
		return
	}

	files := make([]SFTPFileInfo, 0, len(entries))
	for _, e := range entries {
		mode := e.Mode()
		f := SFTPFileInfo{
			Name:      e.Name(),
			Size:      e.Size(),
			ModTime:   e.ModTime().Format(time.RFC3339),
			Perm:      mode.Perm().String(),
			PermOctal: fmt.Sprintf("%o", mode.Perm()),
		}
		switch {
		case mode.IsDir():
			f.Type = "directory"
		case mode&os.ModeSymlink != 0:
			f.Type = "link"
		default:
			f.Type = "file"
		}
		files = append(files, f)
	}

	_ = respond(netConn, req, SFTPResponse{Action: "list", OK: true, Files: files})
}

func handleStat(req SFTPRequest, netConn net.Conn, client *sftp.Client, path string) {
	info, err := client.Stat(path)
	if err != nil {
		logger.Warn("SFTP stat 失败: %v", err)
		sendError(netConn, req, fmt.Errorf("获取文件信息失败: %w", err))
		return
	}
	mode := info.Mode()
	f := SFTPFileInfo{
		Name:      info.Name(),
		Size:      info.Size(),
		ModTime:   info.ModTime().Format(time.RFC3339),
		Perm:      mode.Perm().String(),
		PermOctal: fmt.Sprintf("%o", mode.Perm()),
	}
	switch {
	case mode.IsDir():
		f.Type = "directory"
	case mode&os.ModeSymlink != 0:
		f.Type = "link"
	default:
		f.Type = "file"
	}
	_ = respond(netConn, req, SFTPResponse{Action: "stat", OK: true, Files: []SFTPFileInfo{f}})
}

func handleMkdir(req SFTPRequest, netConn net.Conn, client *sftp.Client, path, perm string) {
	if err := client.Mkdir(path); err != nil {
		logger.Warn("SFTP 创建目录失败: %v", err)
		sendError(netConn, req, fmt.Errorf("创建目录失败: %w", err))
		return
	}
	if perm != "" {
		_ = client.Chmod(path, parsePerm(perm))
	}
	_ = respond(netConn, req, SFTPResponse{Action: "mkdir", OK: true, Path: path})
}

func handleDelete(req SFTPRequest, netConn net.Conn, client *sftp.Client, containerName string, endpoint lxc.Endpoint, path string) {
	info, err := client.Stat(path)
	if err != nil {
		logger.Warn("SFTP 删除失败(stat): %v", err)
		sendError(netConn, req, fmt.Errorf("获取文件信息失败: %w", err))
		return
	}
	if info.IsDir() {
		// RemoveDirectory 对非空目录直接失败（rmdir 语义）；
		// 递归删除改走容器内 rm -rf（argv 形式），且拒绝删根目录。
		cp, cerr := cleanRemotePath(path)
		if cerr != nil || cp == "/" {
			sendError(netConn, req, fmt.Errorf("拒绝删除该路径"))
			return
		}
		ctx, cancel := execCtx()
		defer cancel()
		err = runInContainer(ctx, endpoint, containerName, "rm", "-rf", "--", cp)
	} else {
		err = client.Remove(path)
	}
	if err != nil {
		logger.Warn("SFTP 删除失败: %v", err)
		sendError(netConn, req, fmt.Errorf("删除失败: %w", err))
		return
	}
	_ = respond(netConn, req, SFTPResponse{Action: "delete", OK: true, Path: path})
}

func handleUpload(req SFTPRequest, dec *json.Decoder, netConn net.Conn, client *sftp.Client, path string, size int64) {
	file, err := client.Create(path)
	if err != nil {
		logger.Warn("SFTP 上传创建文件失败: %v", err)
		sendError(netConn, req, fmt.Errorf("创建文件失败: %w", err))
		return
	}

	// 确认上传就绪，之后客户端会逐个发送数据块。
	_ = respond(netConn, req, SFTPResponse{Action: "upload", OK: true, Path: path})

	var written int64
	var chunkReq SFTPRequest
	for dec.Decode(&chunkReq) == nil && chunkReq.Action == "upload_data" {
		data, err := base64.StdEncoding.DecodeString(chunkReq.Data)
		if err != nil {
			logger.Warn("SFTP 上传解码失败: %v", err)
			_ = file.Close()
			sendError(netConn, req, fmt.Errorf("数据解码失败: %w", err))
			return
		}

		n, werr := file.Write(data)
		written += int64(n)
		if werr != nil {
			logger.Warn("SFTP 上传写入失败: %v", werr)
			_ = file.Close()
			sendError(netConn, req, fmt.Errorf("写入文件失败: %w", werr))
			return
		}

		// 每收到一个数据块都回传进度，这样客户端不必自算百分比，
		// 而且最后一个不足 64 KiB 的数据块也能得到正确的收尾进度。
		percent := 100
		if size > 0 {
			percent = int(written * 100 / size)
		}
		_ = respond(netConn, chunkReq, SFTPResponse{Action: "upload_progress", Percent: percent})
	}

	// 循环结束时 chunkReq 是终止包（upload_done）或一次意外的请求。
	if chunkReq.Action != "upload_done" {
		if chunkReq.Action != "" {
			logger.Warn("SFTP 上传被 %s 中断", chunkReq.Action)
			_ = respond(netConn, chunkReq, SFTPResponse{Action: "upload", OK: false, Error: "上传流被中断"})
		}
		_ = file.Close()
		return
	}

	if err := file.Close(); err != nil {
		logger.Warn("SFTP 上传关闭文件失败: %v", err)
		sendError(netConn, req, fmt.Errorf("关闭文件失败: %w", err))
		return
	}

	_ = respond(netConn, chunkReq, SFTPResponse{Action: "upload_done", OK: true, Path: path, Size: written})
}

func handleDownload(req SFTPRequest, netConn net.Conn, client *sftp.Client, path string) {
	file, err := client.Open(path)
	if err != nil {
		logger.Warn("SFTP 下载打开文件失败: %v", err)
		sendError(netConn, req, fmt.Errorf("打开文件失败: %w", err))
		return
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		logger.Warn("SFTP 下载 stat 失败: %v", err)
		sendError(netConn, req, fmt.Errorf("获取文件信息失败: %w", err))
		return
	}

	buf := make([]byte, sftpChunkSize)
	var offset int64
	for {
		n, err := file.Read(buf)
		if n > 0 {
			_ = respond(netConn, req, SFTPResponse{
				Action: "download_data",
				Offset: offset,
				Data:   base64.StdEncoding.EncodeToString(buf[:n]),
				Size:   info.Size(),
			})
			offset += int64(n)
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			logger.Warn("SFTP 下载读取失败: %v", err)
			sendError(netConn, req, fmt.Errorf("读取文件失败: %w", err))
			return
		}
	}

	_ = respond(netConn, req, SFTPResponse{Action: "download_done", OK: true, Path: path, Size: offset})
}

func handleEdit(req SFTPRequest, netConn net.Conn, client *sftp.Client, path, content string) {
	data, err := base64.StdEncoding.DecodeString(content)
	if err != nil {
		logger.Warn("SFTP 编辑解码失败: %v", err)
		sendError(netConn, req, fmt.Errorf("数据解码失败: %w", err))
		return
	}

	file, err := client.Create(path)
	if err != nil {
		logger.Warn("SFTP 编辑创建文件失败: %v", err)
		sendError(netConn, req, fmt.Errorf("创建文件失败: %w", err))
		return
	}

	if _, werr := file.Write(data); werr != nil {
		_ = file.Close()
		logger.Warn("SFTP 编辑写入失败: %v", werr)
		sendError(netConn, req, fmt.Errorf("写入文件失败: %w", werr))
		return
	}

	if err := file.Close(); err != nil {
		logger.Warn("SFTP 编辑关闭文件失败: %v", err)
		sendError(netConn, req, fmt.Errorf("关闭文件失败: %w", err))
		return
	}

	_ = respond(netConn, req, SFTPResponse{Action: "edit", OK: true, Path: path, Size: int64(len(data))})
}

// cleanRemotePath 规范化容器内路径：必须是绝对路径，Clean 后不允许
// 逃出容器根（绝对路径 Clean 天然收敛在 / 内），同时拒绝空路径。
func cleanRemotePath(p string) (string, error) {
	if p == "" {
		return "", fmt.Errorf("路径为空")
	}
	if !pathpkg.IsAbs(p) {
		return "", fmt.Errorf("路径必须是绝对路径: %s", p)
	}
	return pathpkg.Clean(p), nil
}

// runInContainer 在容器内直接执行命令（argv 形式，不经 shell，无注入面）。
func runInContainer(ctx context.Context, endpoint lxc.Endpoint, containerName string, argv ...string) error {
	args := append([]string{"--"}, argv...)
	cmd := lxc.ExecCommand(ctx, endpoint, containerName, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			msg = err.Error()
		}
		return fmt.Errorf("%s", msg)
	}
	return nil
}

func execCtx() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), 300*time.Second)
}

func handleCreateFile(req SFTPRequest, netConn net.Conn, client *sftp.Client, p string) {
	cp, err := cleanRemotePath(p)
	if err != nil {
		sendError(netConn, req, err)
		return
	}
	// O_EXCL 保证同名文件存在时明确报错而不是截断它。
	f, err := client.OpenFile(cp, os.O_RDWR|os.O_CREATE|os.O_EXCL)
	if err != nil {
		logger.Warn("SFTP 新建文件失败: %v", err)
		sendError(netConn, req, fmt.Errorf("新建文件失败: %w", err))
		return
	}
	_ = f.Close()
	_ = respond(netConn, req, SFTPResponse{Action: "create_file", OK: true, Path: cp})
}

func handleRename(req SFTPRequest, netConn net.Conn, client *sftp.Client, oldPath, newPath string) {
	co, err := cleanRemotePath(oldPath)
	if err != nil {
		sendError(netConn, req, err)
		return
	}
	cn, err := cleanRemotePath(newPath)
	if err != nil {
		sendError(netConn, req, err)
		return
	}
	if err := client.Rename(co, cn); err != nil {
		logger.Warn("SFTP 重命名失败: %v", err)
		sendError(netConn, req, fmt.Errorf("重命名失败: %w", err))
		return
	}
	_ = respond(netConn, req, SFTPResponse{Action: "rename", OK: true, Path: cn})
}

func handleChmod(req SFTPRequest, netConn net.Conn, client *sftp.Client, p, perm string) {
	cp, err := cleanRemotePath(p)
	if err != nil {
		sendError(netConn, req, err)
		return
	}
	if err := client.Chmod(cp, parsePerm(perm)); err != nil {
		logger.Warn("SFTP chmod 失败: %v", err)
		sendError(netConn, req, fmt.Errorf("修改权限失败: %w", err))
		return
	}
	_ = respond(netConn, req, SFTPResponse{Action: "chmod", OK: true, Path: cp})
}

func handleChown(req SFTPRequest, netConn net.Conn, client *sftp.Client, p string, uid, gid int) {
	cp, err := cleanRemotePath(p)
	if err != nil {
		sendError(netConn, req, err)
		return
	}
	// 约定 -1 表示"不修改该项"（POSIX chown 语义）。
	if uid > 65535 || gid > 65535 {
		sendError(netConn, req, fmt.Errorf("uid/gid 超出范围"))
		return
	}
	if err := client.Chown(cp, uid, gid); err != nil {
		logger.Warn("SFTP chown 失败: %v", err)
		sendError(netConn, req, fmt.Errorf("修改属主失败: %w", err))
		return
	}
	_ = respond(netConn, req, SFTPResponse{Action: "chown", OK: true, Path: cp})
}

func handleCopy(req SFTPRequest, netConn net.Conn, containerName string, endpoint lxc.Endpoint, src, dst string) {
	cs, err := cleanRemotePath(src)
	if err != nil {
		sendError(netConn, req, err)
		return
	}
	cd, err := cleanRemotePath(dst)
	if err != nil {
		sendError(netConn, req, err)
		return
	}
	ctx, cancel := execCtx()
	defer cancel()
	if err := runInContainer(ctx, endpoint, containerName, "cp", "-r", "--", cs, cd); err != nil {
		logger.Warn("SFTP 复制失败: %v", err)
		sendError(netConn, req, fmt.Errorf("复制失败: %w", err))
		return
	}
	_ = respond(netConn, req, SFTPResponse{Action: "copy", OK: true, Path: cd})
}

func handleMove(req SFTPRequest, netConn net.Conn, containerName string, endpoint lxc.Endpoint, src, dst string) {
	cs, err := cleanRemotePath(src)
	if err != nil {
		sendError(netConn, req, err)
		return
	}
	cd, err := cleanRemotePath(dst)
	if err != nil {
		sendError(netConn, req, err)
		return
	}
	ctx, cancel := execCtx()
	defer cancel()
	if err := runInContainer(ctx, endpoint, containerName, "mv", "--", cs, cd); err != nil {
		logger.Warn("SFTP 移动失败: %v", err)
		sendError(netConn, req, fmt.Errorf("移动失败: %w", err))
		return
	}
	_ = respond(netConn, req, SFTPResponse{Action: "move", OK: true, Path: cd})
}

// handleArchive 把同目录下的多个条目打包。tar.gz 走 tar -C；zip 走
// sh -c + 位置参数在目录内执行 zip，避免把整路径塞进包里。
func handleArchive(req SFTPRequest, netConn net.Conn, containerName string, endpoint lxc.Endpoint, full SFTPRequest) {
	dir, err := cleanRemotePath(full.Path)
	if err != nil {
		sendError(netConn, req, err)
		return
	}
	if len(full.Names) == 0 {
		sendError(netConn, req, fmt.Errorf("没有要打包的文件"))
		return
	}
	for _, n := range full.Names {
		// 条目必须是当前目录内的纯文件名，禁止带 / 或 .. 逃出目录
		if n == "" || strings.Contains(n, "/") || n == "." || n == ".." {
			sendError(netConn, req, fmt.Errorf("非法的打包条目: %s", n))
			return
		}
	}
	name := full.Name
	if name == "" {
		sendError(netConn, req, fmt.Errorf("缺少压缩包文件名"))
		return
	}
	if strings.Contains(name, "/") {
		sendError(netConn, req, fmt.Errorf("非法的压缩包文件名"))
		return
	}
	format := full.Format
	if format == "" {
		format = "tar.gz"
	}
	out := dir + "/" + name
	if dir == "/" {
		out = "/" + name
	}
	ctx, cancel := execCtx()
	defer cancel()
	var runErr error
	switch format {
	case "tar.gz":
		argv := []string{"tar", "-czf", out, "-C", dir}
		argv = append(argv, full.Names...)
		runErr = runInContainer(ctx, endpoint, containerName, argv...)
	case "zip":
		argv := []string{"sh", "-c", "cd \"$1\" && shift && out=\"$1\" && shift && exec zip -q -r \"$out\" \"$@\"", "sh", dir, out}
		argv = append(argv, full.Names...)
		runErr = runInContainer(ctx, endpoint, containerName, argv...)
	default:
		sendError(netConn, req, fmt.Errorf("不支持的格式: %s", format))
		return
	}
	if runErr != nil {
		logger.Warn("SFTP 打包失败: %v", runErr)
		sendError(netConn, req, fmt.Errorf("打包失败(容器内需有 tar/zip): %w", runErr))
		return
	}
	_ = respond(netConn, req, SFTPResponse{Action: "archive", OK: true, Path: out})
}

// handleExtract 把压缩包解压到目标目录（以 NewPath 指定，空则解压到当前目录）。
func handleExtract(req SFTPRequest, netConn net.Conn, containerName string, endpoint lxc.Endpoint, pkgPath, target string) {
	cp, err := cleanRemotePath(pkgPath)
	if err != nil {
		sendError(netConn, req, err)
		return
	}
	if target == "" {
		target = pathpkg.Dir(cp)
	}
	ct, err := cleanRemotePath(target)
	if err != nil {
		sendError(netConn, req, err)
		return
	}
	ctx, cancel := execCtx()
	defer cancel()
	lower := strings.ToLower(cp)
	var runErr error
	switch {
	case strings.HasSuffix(lower, ".tar.gz") || strings.HasSuffix(lower, ".tgz") || strings.HasSuffix(lower, ".tar"):
		runErr = runInContainer(ctx, endpoint, containerName, "tar", "-xf", cp, "-C", ct)
	case strings.HasSuffix(lower, ".zip"):
		runErr = runInContainer(ctx, endpoint, containerName, "unzip", "-o", "-q", cp, "-d", ct)
	default:
		sendError(netConn, req, fmt.Errorf("不支持的压缩格式(仅 tar.gz/tgz/tar/zip)"))
		return
	}
	if runErr != nil {
		logger.Warn("SFTP 解压失败: %v", runErr)
		sendError(netConn, req, fmt.Errorf("解压失败(容器内需有 tar/unzip): %w", runErr))
		return
	}
	_ = respond(netConn, req, SFTPResponse{Action: "extract", OK: true, Path: ct})
}

func parsePerm(perm string) os.FileMode {
	var m os.FileMode
	if _, err := fmt.Sscanf(perm, "%o", &m); err != nil {
		return 0644
	}
	return m
}
