package console

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"lxdapi/internal/db"
	"lxdapi/internal/lxc"
	"lxdapi/models"
	"lxdapi/pkg/logger"
)

// 远程节点控制台 / SFTP 中继：
// 容器在远程节点上时，主控不再在本机执行 lxc 命令（容器根本不在本机），
// 而是用节点系统级密钥向节点换发一枚节点侧一次性令牌，再把浏览器的
// WebSocket 双向中继到节点的 /ws/console 或 /ws/sftp，
// 会话由节点本机处理（与 lxdcloud 的节点代理为同一思路）。

// nodeForContainer 按主控容器记录查归属远程节点（本机容器返回 nil）
func nodeForContainer(containerName string) *models.Node {
	var container models.Container
	if err := db.DB.Where("name = ?", containerName).First(&container).Error; err != nil || container.NodeID == 0 {
		return nil
	}
	var node models.Node
	if err := db.DB.First(&node, container.NodeID).Error; err != nil || node.Host == "" {
		return nil
	}
	return &node
}

// mintNodeToken 用节点系统级密钥向节点申请一次性令牌。
// 节点侧控制台与 SFTP 令牌共用同一存储（console.GenerateTokenWithEndpoint），
// 系统级 create-token 签发的令牌对 /ws/console 与 /ws/sftp 均有效。
func mintNodeToken(node *models.Node, containerName string) (string, error) {
	base := lxc.RemoteBaseURL(node.Host, node.APIPort)
	body, _ := json.Marshal(map[string]string{"hostname": containerName})
	req, err := http.NewRequest("POST", base+"/api/system/console/create-token", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Hash", node.APIHash)
	hc := &http.Client{Timeout: 15 * time.Second}
	resp, err := hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("连接节点失败: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var env struct {
		Code int    `json:"code"`
		Msg  string `json:"msg"`
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return "", fmt.Errorf("节点令牌响应无法解析 (HTTP %d)", resp.StatusCode)
	}
	if env.Code != 200 || env.Data.Token == "" {
		return "", fmt.Errorf("节点签发令牌失败: %s", env.Msg)
	}
	return env.Data.Token, nil
}

// relayToNode 把当前浏览器的 WebSocket 请求中继到节点本机处理。
func relayToNode(c *gin.Context, node *models.Node, containerName, nodePath string) {
	nodeToken, err := mintNodeToken(node, containerName)
	if err != nil {
		logger.Error("为远程容器 %s 申请节点令牌失败: %v", containerName, err)
		c.JSON(http.StatusBadGateway, gin.H{"code": 502, "msg": "节点令牌申请失败: " + err.Error()})
		return
	}
	base := lxc.RemoteBaseURL(node.Host, node.APIPort)
	wsBase := "ws" + strings.TrimPrefix(base, "http") // http->ws，https->wss
	nodeConn, _, err := websocket.DefaultDialer.Dial(wsBase+nodePath+"?token="+nodeToken, nil)
	if err != nil {
		logger.Error("连接节点 %s 的 %s 失败: %v", node.Name, nodePath, err)
		c.JSON(http.StatusBadGateway, gin.H{"code": 502, "msg": "连接节点失败: " + err.Error()})
		return
	}
	defer nodeConn.Close()

	browserConn, err := upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		logger.Error("WebSocket升级失败: %v", err)
		return
	}
	defer browserConn.Close()

	logger.Info("远程节点会话中继建立: %s -> %s%s", containerName, node.Name, nodePath)
	var once sync.Once
	closeBoth := func() {
		browserConn.Close()
		nodeConn.Close()
	}
	done := make(chan struct{}, 2)
	pump := func(dst, src *websocket.Conn) {
		defer func() { done <- struct{}{} }()
		for {
			mt, msg, err := src.ReadMessage()
			if err != nil {
				once.Do(closeBoth)
				return
			}
			if err := dst.WriteMessage(mt, msg); err != nil {
				once.Do(closeBoth)
				return
			}
		}
	}
	go pump(nodeConn, browserConn)
	go pump(browserConn, nodeConn)
	<-done
	once.Do(closeBoth)
	logger.Info("远程节点会话中继结束: %s", containerName)
}
