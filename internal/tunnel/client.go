package tunnel

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"reverse-tunnel/internal/proto"
	"reverse-tunnel/internal/pqctls"
)

// Client 表示反向隧道客户端
type Client struct {
	serverAddr string // 服务器地址（例如 1.2.3.4:7000）
	localAddr  string // 本地服务地址（例如 127.0.0.1:80）
	remotePort int    // 远程端口（服务器要监听的端口，0 表示由服务器指定）

	// PQC mTLS 配置（可选）
	useTLS     bool
	tlsCertFile string
	tlsKeyFile  string
	tlsCAFile   string
	serverName  string

	controlConn net.Conn // 控制连接（与 server 的连接）
	controlMu   sync.RWMutex

	// connMap 管理 connID 到本地连接的映射
	connMap sync.Map // map[uint32]net.Conn
}

// NewClient 创建一个新的客户端实例
func NewClient(serverAddr, localAddr string, remotePort int) *Client {
	return &Client{
		serverAddr: serverAddr,
		localAddr:  localAddr,
		remotePort: remotePort,
		useTLS:     false,
	}
}

// NewClientWithTLS 创建一个启用 PQC mTLS 的客户端实例
func NewClientWithTLS(serverAddr, localAddr string, remotePort int, certFile, keyFile, caFile, serverName string) *Client {
	return &Client{
		serverAddr:  serverAddr,
		localAddr:   localAddr,
		remotePort:  remotePort,
		useTLS:      true,
		tlsCertFile: certFile,
		tlsKeyFile:  keyFile,
		tlsCAFile:   caFile,
		serverName:  serverName,
	}
}

// Run 启动客户端，连接服务器并保持连接
func (c *Client) Run(ctx context.Context) error {
	// 重连循环
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
			// 尝试连接服务器
			if err := c.connectToServer(ctx); err != nil {
				log.Printf("连接服务器失败: %v，5秒后重试...", err)
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(5 * time.Second):
					continue
				}
			}

			// 连接成功，发送初始化配置（如果指定了远程端口）
			log.Printf("已连接到服务器: %s", c.serverAddr)
			if c.remotePort > 0 {
				if err := c.sendInitConfig(); err != nil {
					log.Printf("发送初始化配置失败: %v", err)
					c.closeControlConn()
					continue
				}
			}
			
			// 处理连接
			if err := c.handleConnection(ctx); err != nil {
				log.Printf("处理连接错误: %v", err)
				c.closeControlConn()
			}

			// 连接断开，等待后重连
			log.Printf("与服务器断开连接，5秒后重试...")
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(5 * time.Second):
				continue
			}
		}
	}
}

// connectToServer 连接到服务器
func (c *Client) connectToServer(ctx context.Context) error {
	var conn net.Conn
	var err error

	if c.useTLS {
		// 使用 PQC mTLS（通过 OpenSSL）
		dialer, err := pqctls.NewPQCDialerOpenSSL(c.tlsCertFile, c.tlsKeyFile, c.tlsCAFile)
		if err != nil {
			return fmt.Errorf("创建 PQC TLS 拨号器失败: %v", err)
		}
		defer dialer.Close()

		conn, err = dialer.Dial("tcp", c.serverAddr)
		if err != nil {
			return fmt.Errorf("PQC TLS 连接失败: %v", err)
		}
		log.Printf("已建立 PQC mTLS 连接 (via OpenSSL): %s", c.serverAddr)
	} else {
		// 使用纯 TCP
		dialer := &net.Dialer{
			Timeout: 10 * time.Second,
		}

		conn, err = dialer.DialContext(ctx, "tcp", c.serverAddr)
		if err != nil {
			return err
		}
	}

	c.controlMu.Lock()
	c.controlConn = conn
	c.controlMu.Unlock()

	return nil
}

// closeControlConn 关闭控制连接
func (c *Client) closeControlConn() {
	c.controlMu.Lock()
	if c.controlConn != nil {
		c.controlConn.Close()
		c.controlConn = nil
	}
	c.controlMu.Unlock()
}

// handleConnection 处理与服务器的连接
func (c *Client) handleConnection(ctx context.Context) error {
	// 启动从服务器读取帧的 goroutine
	frameChan := make(chan *proto.Frame, 10)
	errChan := make(chan error, 1)

	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				c.controlMu.RLock()
				conn := c.controlConn
				c.controlMu.RUnlock()

				if conn == nil {
					errChan <- io.EOF
					return
				}

				frame, err := proto.DecodeFrame(conn)
				if err != nil {
					errChan <- err
					return
				}
				frameChan <- frame
			}
		}
	}()

	// 主循环：处理来自服务器的帧
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errChan:
			if err != io.EOF {
				log.Printf("读取帧错误: %v", err)
			}
			return err
		case frame := <-frameChan:
			if err := c.handleFrame(ctx, frame); err != nil {
				log.Printf("处理帧错误 (connID=%d): %v", frame.ConnID, err)
			}
		}
	}
}

// handleFrame 处理来自服务器的帧
func (c *Client) handleFrame(ctx context.Context, frame *proto.Frame) error {
	switch frame.Type {
	case proto.FrameTypeNEW_CONN:
		return c.handleNewConn(ctx, frame)
	case proto.FrameTypeDATA:
		return c.handleDataFrame(frame)
	case proto.FrameTypeCLOSE:
		return c.handleCloseFrame(frame)
	default:
		log.Printf("未知帧类型: %d, connID=%d", frame.Type, frame.ConnID)
		return nil
	}
}

// handleNewConn 处理 NEW_CONN 帧，创建到本地服务的连接
func (c *Client) handleNewConn(ctx context.Context, frame *proto.Frame) error {
	log.Printf("收到 NEW_CONN 帧，connID=%d，正在连接本地服务: %s", frame.ConnID, c.localAddr)

	// 连接到本地服务
	localConn, err := net.DialTimeout("tcp", c.localAddr, 5*time.Second)
	if err != nil {
		log.Printf("连接本地服务失败 (connID=%d): %v", frame.ConnID, err)
		// 发送 CLOSE_CONN 帧通知服务器
		c.sendCloseFrame(frame.ConnID)
		return err
	}

	// 将连接存入 map
	c.connMap.Store(frame.ConnID, localConn)
	log.Printf("已建立本地连接: connID=%d, local=%s", frame.ConnID, c.localAddr)

	// 启动从本地连接读取数据并转发给服务器的 goroutine
	go c.forwardLocalToServer(ctx, frame.ConnID, localConn)

	return nil
}

// forwardLocalToServer 从本地连接读取数据并转发给服务器
func (c *Client) forwardLocalToServer(ctx context.Context, connID uint32, localConn net.Conn) {
	defer func() {
		localConn.Close()
		c.connMap.Delete(connID)
		log.Printf("本地连接已关闭: connID=%d", connID)
	}()

	buf := make([]byte, 4096)
	for {
		select {
		case <-ctx.Done():
			// 发送 CLOSE_CONN 帧
			c.sendCloseFrame(connID)
			return
		default:
			n, err := localConn.Read(buf)
			if err != nil {
				if err != io.EOF {
					log.Printf("读取本地连接数据错误 (connID=%d): %v", connID, err)
				}
				// 发送 CLOSE_CONN 帧通知服务器
				c.sendCloseFrame(connID)
				return
			}

			if n > 0 {
				// 发送 DATA 帧给服务器
				dataFrame := &proto.Frame{
					Type:    proto.FrameTypeDATA,
					ConnID:  connID,
					Payload: buf[:n],
				}

				frameData, err := proto.EncodeFrame(dataFrame)
				if err != nil {
					log.Printf("编码 DATA 帧错误 (connID=%d): %v", connID, err)
					return
				}

				c.controlMu.RLock()
				controlConn := c.controlConn
				c.controlMu.RUnlock()

				if controlConn == nil {
					return
				}

				if _, err := controlConn.Write(frameData); err != nil {
					log.Printf("发送 DATA 帧错误 (connID=%d): %v", connID, err)
					return
				}
			}
		}
	}
}

// handleDataFrame 处理来自服务器的 DATA 帧，写入本地连接
func (c *Client) handleDataFrame(frame *proto.Frame) error {
	conn, ok := c.connMap.Load(frame.ConnID)
	if !ok {
		log.Printf("警告: 未找到 connID=%d 对应的本地连接", frame.ConnID)
		return nil
	}

	localConn, ok := conn.(net.Conn)
	if !ok {
		log.Printf("错误: connID=%d 对应的连接类型错误", frame.ConnID)
		return nil
	}

	// 将数据写入本地连接
	if len(frame.Payload) > 0 {
		if _, err := localConn.Write(frame.Payload); err != nil {
			log.Printf("写入本地连接错误 (connID=%d): %v", frame.ConnID, err)
			// 连接可能已关闭，清理并发送 CLOSE_CONN
			localConn.Close()
			c.connMap.Delete(frame.ConnID)
			c.sendCloseFrame(frame.ConnID)
			return err
		}
	}

	return nil
}

// handleCloseFrame 处理来自服务器的 CLOSE_CONN 帧
func (c *Client) handleCloseFrame(frame *proto.Frame) error {
	conn, ok := c.connMap.LoadAndDelete(frame.ConnID)
	if !ok {
		// 连接可能已经关闭
		return nil
	}

	localConn, ok := conn.(net.Conn)
	if !ok {
		return nil
	}

	localConn.Close()
	log.Printf("收到 CLOSE_CONN 帧，已关闭本地连接: connID=%d", frame.ConnID)

	// 回发 CLOSE_CONN 帧（防止半开连接）
	c.sendCloseFrame(frame.ConnID)

	return nil
}

// sendCloseFrame 发送 CLOSE_CONN 帧给服务器
func (c *Client) sendCloseFrame(connID uint32) {
	c.controlMu.RLock()
	controlConn := c.controlConn
	c.controlMu.RUnlock()

	if controlConn == nil {
		return
	}

	frame := &proto.Frame{
		Type:    proto.FrameTypeCLOSE,
		ConnID:  connID,
		Payload: nil,
	}

	frameData, err := proto.EncodeFrame(frame)
	if err != nil {
		log.Printf("编码 CLOSE_CONN 帧错误 (connID=%d): %v", connID, err)
		return
	}

	if _, err := controlConn.Write(frameData); err != nil {
		log.Printf("发送 CLOSE_CONN 帧错误 (connID=%d): %v", connID, err)
	}
}

// sendInitConfig 发送初始化配置帧
func (c *Client) sendInitConfig() error {
	if c.remotePort <= 0 {
		return nil
	}

	c.controlMu.RLock()
	controlConn := c.controlConn
	c.controlMu.RUnlock()

	if controlConn == nil {
		return fmt.Errorf("控制连接不存在")
	}

	config := &proto.InitConfig{
		RemotePort: c.remotePort,
		LocalAddr:  c.localAddr,
	}

	configData := proto.EncodeInitConfig(config)
	frame := &proto.Frame{
		Type:    proto.FrameTypeINIT,
		ConnID:  0, // INIT 帧使用 connID=0
		Payload: configData,
	}

	frameData, err := proto.EncodeFrame(frame)
	if err != nil {
		return fmt.Errorf("编码 INIT 帧失败: %v", err)
	}

	if _, err := controlConn.Write(frameData); err != nil {
		return fmt.Errorf("发送 INIT 帧失败: %v", err)
	}

	log.Printf("已发送初始化配置: 远程端口=%d, 本地地址=%s", c.remotePort, c.localAddr)
	return nil
}

// cleanup 清理所有资源
func (c *Client) cleanup() {
	// 关闭控制连接
	c.closeControlConn()

	// 关闭所有本地连接
	c.connMap.Range(func(key, value interface{}) bool {
		if conn, ok := value.(net.Conn); ok {
			conn.Close()
		}
		c.connMap.Delete(key)
		return true
	})

	log.Printf("客户端资源已清理")
}
