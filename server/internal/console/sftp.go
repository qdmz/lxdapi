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
	ID      int64  `json:"id"`
	Action  string `json:"action"`
	Path    string `json:"path"`
	Offset  int64  `json:"offset"`
	Data    string `json:"data"`
	Content string `json:"content"`
	Size    int64  `json:"size"`
	Perm    string `json:"perm"`
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
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
	Perm    string `json:"perm"`
	Type    string `json:"type"`
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

	server, err := resolveSFTPServer(containerName, endpoint)
	if err != nil {
		logger.Warn("SFTP %s: %v", containerName, err)
		sendError(netConn, SFTPRequest{Action: "list"}, err)
		return err
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
			handleDelete(req, netConn, client, req.Path)
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
			Name:    e.Name(),
			Size:    e.Size(),
			ModTime: e.ModTime().Format(time.RFC3339),
			Perm:    mode.Perm().String(),
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
		Name:    info.Name(),
		Size:    info.Size(),
		ModTime: info.ModTime().Format(time.RFC3339),
		Perm:    mode.Perm().String(),
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

func handleDelete(req SFTPRequest, netConn net.Conn, client *sftp.Client, path string) {
	info, err := client.Stat(path)
	if err != nil {
		logger.Warn("SFTP 删除失败(stat): %v", err)
		sendError(netConn, req, fmt.Errorf("获取文件信息失败: %w", err))
		return
	}
	if info.IsDir() {
		err = client.RemoveDirectory(path)
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

func parsePerm(perm string) os.FileMode {
	var m os.FileMode
	if _, err := fmt.Sscanf(perm, "%o", &m); err != nil {
		return 0644
	}
	return m
}
