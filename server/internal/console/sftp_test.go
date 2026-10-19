package console

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"lxdapi/internal/core"
	"lxdapi/pkg/logger"
)

const testSFTPServer = "/usr/lib/openssh/sftp-server"

// sftpResp mirrors the server's SFTPResponse so the test can decode the wire.
type sftpResp struct {
	ID      int64          `json:"id"`
	Action  string         `json:"action"`
	OK      bool           `json:"ok"`
	Error   string         `json:"error"`
	Path    string         `json:"path"`
	Offset  int64          `json:"offset"`
	Data    string         `json:"data"`
	Size    int64          `json:"size"`
	Percent int            `json:"percent"`
	Files   []sftpFileInfo `json:"files,omitempty"`
}

type sftpFileInfo struct {
	Name    string `json:"name"`
	Size    int64  `json:"size"`
	ModTime string `json:"modTime"`
	Perm    string `json:"perm"`
	Type    string `json:"type"`
}

// wsClient drives the JSON-over-WebSocket SFTP protocol from the client side.
// Replies to list/stat/mkdir/delete/edit/ping carry the request's own id;
// upload/download stream their progress and chunks under the same id, so the
// client matches on id for request/reply and on action for pushes.
type wsClient struct {
	conn *websocket.Conn
	seq  int64
	mu   sync.Mutex
}

func newWSPair(t *testing.T) (*wsClient, *websocket.Conn) {
	t.Helper()

	accepted := make(chan *websocket.Conn, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		accepted <- conn
	}))
	t.Cleanup(srv.Close)

	u, _ := url.Parse(srv.URL + "/ws")
	u.Scheme = "ws"
	netConn, err := net.Dial("tcp", u.Host)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	client, resp, err := websocket.NewClient(netConn, u, nil, 0, 0)
	if err != nil {
		t.Fatalf("client: %v", err)
	}
	resp.Body.Close()
	return &wsClient{conn: client}, <-accepted
}

func (c *wsClient) send(t *testing.T, action string, payload map[string]any) int64 {
	t.Helper()
	c.mu.Lock()
	seq := c.seq
	c.seq++
	c.mu.Unlock()

	msg := map[string]any{"id": seq, "action": action}
	for k, v := range payload {
		msg[k] = v
	}
	body, err := json.Marshal(msg)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if err := c.conn.WriteMessage(websocket.TextMessage, body); err != nil {
		t.Fatalf("send %s: %v", action, err)
	}
	return seq
}

// readMessage returns the next server message, or fails the test on timeout.
func (c *wsClient) readMessage(t *testing.T, timeout time.Duration) sftpResp {
	t.Helper()
	if err := c.conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		t.Fatalf("set deadline: %v", err)
	}
	_, data, err := c.conn.ReadMessage()
	if err != nil {
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			t.Fatalf("timeout after %v", timeout)
		}
		t.Fatalf("read: %v", err)
	}
	var msg sftpResp
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal %s: %v", data, err)
	}
	return msg
}

func (c *wsClient) waitForID(t *testing.T, id int64, timeout time.Duration) sftpResp {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		msg := c.readMessage(t, time.Until(deadline))
		if msg.ID == id {
			return msg
		}
	}
}

func (c *wsClient) close(t *testing.T) {
	t.Helper()
	if c.conn != nil {
		c.conn.Close()
	}
}

// writeLxcShim creates the `lxc` binary the console package invokes. It skips
// LXC entirely. Reads (test -x, sftp-server itself) run on the host so the
// panel's resolveSFTPServer probe still succeeds; execs of the sftp-server are
// funnelled through /usr/sbin/chroot into a scratch root, so the session can
// never read or write the real filesystem.
func writeLxcShim(t *testing.T, root string) string {
	t.Helper()
	dir := t.TempDir()
	script := []byte("#!/bin/sh\n" +
		"ROOT=" + root + "\n" +
		"cmd=\"$1\"; shift\n" +
		"case \"$cmd\" in\n" +
		"version) echo \"5.0.2\" ;;\n" +
		"exec)\n" +
		"  shift 2\n" +
		"  [ \"$1\" = \"--\" ] && shift\n" +
		"  case \"$1\" in\n" +
		"    /usr/lib/openssh/sftp-server|/usr/libexec/openssh/sftp-server|/usr/lib/ssh/sftp-server)\n" +
		"      shift\n" +
		"      exec /usr/sbin/chroot \"$ROOT\" /usr/lib/openssh/sftp-server \"$@\"\n" +
		"      ;;\n" +
		"    *)\n" +
		"      exec \"$@\"\n" +
		"      ;;\n" +
		"  esac\n" +
		"  ;;\n" +
		"list) echo \"test,RUNNING\" ;;\n" +
		"network) ;;\n" +
		"query) echo '{\"metadata\":{\"status\":\"Running\",\"status_code\":103}}' ;;\n" +
		"*) exit 1 ;;\n" +
		"esac\n")
	path := filepath.Join(dir, "lxc")
	if err := os.WriteFile(path, script, 0755); err != nil {
		t.Fatalf("write shim: %v", err)
	}
	return path
}

// stageRoot populates the chroot root with the sftp-server binary, its shared
// library dependencies, and an empty work directory the tests exercise.
//
// sftp-server is dynamically linked, so a bare chroot fails with "No such file
// or directory" unless the loader and libc are present inside the root.
func stageRoot(t *testing.T, root string) {
	t.Helper()
	binDir := filepath.Join(root, "usr", "lib", "openssh")
	if err := os.MkdirAll(binDir, 0755); err != nil {
		t.Fatalf("mkdir bin dir: %v", err)
	}
	src, err := os.ReadFile(testSFTPServer)
	if err != nil {
		t.Fatalf("read sftp-server: %v", err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "sftp-server"), src, 0755); err != nil {
		t.Fatalf("stage binary: %v", err)
	}
	for _, hostPath := range lddDependencies(t, testSFTPServer) {
		copyIntoRoot(t, root, hostPath)
	}
	// sftp-server opens /dev/null for logging; without it the process exits 1.
	dev := filepath.Join(root, "dev")
	if err := os.MkdirAll(dev, 0755); err != nil {
		t.Fatalf("mkdir dev: %v", err)
	}
	if err := exec.Command("mknod", "-m", "666", filepath.Join(dev, "null"), "c", "1", "3").Run(); err != nil {
		t.Fatalf("mknod /dev/null: %v", err)
	}
	// sftp-server resolves the session user with getpwuid.
	etc := filepath.Join(root, "etc")
	if err := os.MkdirAll(etc, 0755); err != nil {
		t.Fatalf("mkdir etc: %v", err)
	}
	if err := os.WriteFile(filepath.Join(etc, "passwd"), []byte("root:x:0:0:root:/root:/bin/bash\n"), 0644); err != nil {
		t.Fatalf("write /etc/passwd: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "root"), 0755); err != nil {
		t.Fatalf("mkdir /root: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "work"), 0755); err != nil {
		t.Fatalf("mkdir work: %v", err)
	}
}

// lddDependencies returns the resolved host paths of every shared library the
// given binary needs, skipping the vdso and anything the loader can't resolve.
func lddDependencies(t *testing.T, binary string) []string {
	t.Helper()
	out, err := exec.Command("ldd", binary).Output()
	if err != nil {
		t.Fatalf("ldd %s: %v", binary, err)
	}
	var deps []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// "libc.so.6 => /lib/x86_64-linux-gnu/libc.so.6 (0x...)"
		// or "/lib64/ld-linux-x86-64.so.2 (0x...)"
		var path string
		if idx := strings.Index(line, "=> "); idx >= 0 {
			path = strings.Fields(line[idx+3:])[0]
		} else {
			path = strings.Fields(line)[0]
		}
		if strings.HasPrefix(path, "/") && !strings.Contains(path, "linux-vdso") {
			deps = append(deps, path)
		}
	}
	return deps
}

// copyIntoRoot copies a host library into the chroot, dereferencing symlinks so
// that glibc's loader scripts (libc.so.6) resolve inside the new root.
func copyIntoRoot(t *testing.T, root, hostPath string) {
	t.Helper()
	realPathRaw, err := exec.Command("readlink", "-f", hostPath).Output()
	if err != nil {
		t.Fatalf("resolve %s: %v", hostPath, err)
	}
	realPath := strings.TrimSpace(string(realPathRaw))
	src, err := os.ReadFile(realPath)
	if err != nil {
		t.Fatalf("read %s: %v", realPath, err)
	}
	dest := filepath.Join(root, strings.TrimPrefix(hostPath, "/"))
	if err := os.MkdirAll(filepath.Dir(dest), 0755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(dest), err)
	}
	if err := os.WriteFile(dest, src, 0755); err != nil {
		t.Fatalf("write %s: %v", dest, err)
	}
}

func TestHandleSFTP(t *testing.T) {
	if _, err := os.Stat(testSFTPServer); err != nil {
		t.Skipf("no sftp-server at %s", testSFTPServer)
	}
	if _, err := os.Stat("/usr/sbin/chroot"); err != nil {
		t.Skipf("no chroot binary: %v", err)
	}

	logger.Init("release", false)

	root := filepath.Join(t.TempDir(), "root")
	stageRoot(t, root)

	// Route the panel's `lxc` invocations at the shim.
	shim := writeLxcShim(t, root)
	oldConfig := core.GlobalConfig
	t.Cleanup(func() { core.GlobalConfig = oldConfig })
	core.GlobalConfig = &core.Config{LXC: core.LXCConfig{Bin: shim}}

	client, server := newWSPair(t)
	t.Cleanup(func() { client.close(t) })

	done := make(chan error, 1)
	go func() { done <- HandleSFTP(server, "testcontainer") }()
	defer func() {
		client.conn.SetReadDeadline(time.Now().Add(time.Second))
		client.conn.Close()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, os.ErrDeadlineExceeded) && !errors.Is(err, errUnknownAction) {
				t.Errorf("HandleSFTP returned: %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("HandleSFTP did not exit")
		}
	}()

	work := "/work" // inside the chroot; resolveSFTPServer picks the host binary path

	t.Run("list root", func(t *testing.T) {
		id := client.send(t, "list", map[string]any{"path": "/"})
		msg := client.waitForID(t, id, 5*time.Second)
		if !msg.OK {
			t.Fatalf("list: %s", msg.Error)
		}
		names := make([]string, 0, len(msg.Files))
		for _, f := range msg.Files {
			names = append(names, f.Name)
		}
		if len(msg.Files) == 0 {
			t.Error("expected entries in /")
		}
	})

	t.Run("stat", func(t *testing.T) {
		id := client.send(t, "stat", map[string]any{"path": "/"})
		msg := client.waitForID(t, id, 5*time.Second)
		if !msg.OK || len(msg.Files) != 1 || msg.Files[0].Type != "directory" {
			t.Fatalf("stat: ok=%v files=%v err=%s", msg.OK, msg.Files, msg.Error)
		}
	})

	t.Run("mkdir and list", func(t *testing.T) {
		dir := work + "/subdir"
		id := client.send(t, "mkdir", map[string]any{"path": dir, "perm": "0755"})
		msg := client.waitForID(t, id, 5*time.Second)
		if !msg.OK || msg.Path != dir {
			t.Fatalf("mkdir: ok=%v path=%s err=%s", msg.OK, msg.Path, msg.Error)
		}
		id = client.send(t, "list", map[string]any{"path": dir})
		msg = client.waitForID(t, id, 5*time.Second)
		if !msg.OK || len(msg.Files) != 0 {
			t.Fatalf("list empty dir: ok=%v files=%d err=%s", msg.OK, len(msg.Files), msg.Error)
		}
	})

	t.Run("upload then download", func(t *testing.T) {
		path := work + "/upload.bin"
		data := randBytes(190000) // larger than one 64 KiB chunk

		id := client.send(t, "upload", map[string]any{"path": path, "size": int64(len(data))})
		msg := client.waitForID(t, id, 5*time.Second)
		if !msg.OK {
			t.Fatalf("upload start: %s", msg.Error)
		}

		const chunkSize = 64 * 1024
		var progress []int
		for offset := 0; offset < len(data); offset += chunkSize {
			chunk := data[offset:min(offset+chunkSize, len(data))]
			dataID := client.send(t, "upload_data", map[string]any{
				"offset": int64(offset),
				"data":   base64.StdEncoding.EncodeToString(chunk),
			})
			resp := client.waitForID(t, dataID, 5*time.Second)
			if resp.Action != "upload_progress" {
				t.Fatalf("expected upload_progress, got %s: %s", resp.Action, resp.Error)
			}
			progress = append(progress, resp.Percent)
		}

		doneID := client.send(t, "upload_done", map[string]any{"path": path})
		msg = client.waitForID(t, doneID, 5*time.Second)
		if !msg.OK || msg.Size != int64(len(data)) {
			t.Fatalf("upload done: ok=%v size=%d err=%s", msg.OK, msg.Size, msg.Error)
		}
		if len(progress) == 0 || progress[len(progress)-1] != 100 {
			t.Errorf("expected progress ending at 100, got %v", progress)
		}

		got := downloadFile(t, client, path)
		if len(got) != len(data) {
			t.Fatalf("download size %d != %d", len(got), len(data))
		}
		for i := range data {
			if got[i] != data[i] {
				t.Fatalf("download mismatch at offset %d", i)
			}
		}
	})

	t.Run("edit then download", func(t *testing.T) {
		path := work + "/edit.txt"
		encode := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

		id := client.send(t, "edit", map[string]any{"path": path, "content": encode("hello sftp world\n"), "size": int64(17)})
		msg := client.waitForID(t, id, 5*time.Second)
		if !msg.OK {
			t.Fatalf("edit: %s", msg.Error)
		}
		if string(downloadFile(t, client, path)) != "hello sftp world\n" {
			t.Error("edit content mismatch")
		}

		id = client.send(t, "edit", map[string]any{"path": path, "content": encode("replaced\n"), "size": int64(9)})
		msg = client.waitForID(t, id, 5*time.Second)
		if !msg.OK {
			t.Fatalf("edit again: %s", msg.Error)
		}
		if string(downloadFile(t, client, path)) != "replaced\n" {
			t.Error("second edit did not overwrite")
		}
	})

	t.Run("delete", func(t *testing.T) {
		path := work + "/gone.txt"
		// create through the panel so the file genuinely exists in the chroot
		id := client.send(t, "edit", map[string]any{"path": path, "content": base64.StdEncoding.EncodeToString([]byte("bye")), "size": int64(3)})
		msg := client.waitForID(t, id, 5*time.Second)
		if !msg.OK {
			t.Fatalf("precreate: %s", msg.Error)
		}
		id = client.send(t, "delete", map[string]any{"path": path})
		msg = client.waitForID(t, id, 5*time.Second)
		if !msg.OK {
			t.Fatalf("delete: %s", msg.Error)
		}
		id = client.send(t, "stat", map[string]any{"path": path})
		msg = client.waitForID(t, id, 5*time.Second)
		if msg.OK {
			t.Error("stat succeeded on deleted file")
		}
	})

	t.Run("unknown action tears down the session", func(t *testing.T) {
		id := client.send(t, "bogus", map[string]any{"path": "/"})
		msg := client.waitForID(t, id, 5*time.Second)
		if msg.OK {
			t.Fatalf("expected failure for unknown action, got ok=%v", msg.OK)
		}
		client.conn.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
		if _, _, err := client.conn.ReadMessage(); err == nil {
			t.Error("session stayed open after an unknown action")
		}
	})
}

func downloadFile(t *testing.T, client *wsClient, path string) []byte {
	t.Helper()
	id := client.send(t, "download", map[string]any{"path": path})
	var chunks [][]byte
	for {
		msg := client.waitForID(t, id, 10*time.Second)
		switch msg.Action {
		case "download_data":
			decoded, err := base64.StdEncoding.DecodeString(msg.Data)
			if err != nil {
				t.Fatalf("decode chunk: %v", err)
			}
			chunks = append(chunks, decoded)
		case "download_done":
			if !msg.OK {
				t.Fatalf("download: %s", msg.Error)
			}
			t.Logf("download: %d chunks", len(chunks))
			return bytes.Join(chunks, nil)
		default:
			t.Fatalf("unexpected action %s during download", msg.Action)
		}
	}
}

func randBytes(n int) []byte {
	b := make([]byte, n)
	rand.New(rand.NewSource(1)).Read(b)
	return b
}
