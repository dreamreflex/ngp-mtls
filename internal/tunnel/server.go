package tunnel

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"sync/atomic"

	"reverse-tunnel/internal/proto"
	"reverse-tunnel/internal/pqctls"
)

// Server 表示反向隧道服务器
type Server struct {
	controlListenAddr string // 控制端口监听地址
	publicListenAddr  string // 公开端口监听地址（可选，如果为空则由客户端指定）

	// PQC mTLS 配置（可选）
	useTLS     bool
	tlsCertFile string
	tlsKeyFile  string
	tlsCAFile   string

	controlConn net.Conn // 控制连接（与 client 的连接）
	controlMu   sync.RWMutex

	// connMap 管理 connID 到外部连接的映射
	connMap sync.Map // map[uint32]net.Conn

	// nextConnID 用于生成唯一的连接 ID
	nextConnID uint32

	// 动态公开端口监听器（由客户端配置时创建）
	publicListener net.Listener
	publicListenerMu sync.RWMutex
	
	// 公开连接通道
	publicConnChan chan net.Conn
}

// NewServer 创建一个新的服务器实例
func NewServer(controlListenAddr, publicListenAddr string) *Server {
	return &Server{
		controlListenAddr: controlListenAddr,
		publicListenAddr:  publicListenAddr,
		useTLS:            false,
	}
}

// NewServerWithTLS 创建一个启用 PQC mTLS 的服务器实例
func NewServerWithTLS(controlListenAddr, publicListenAddr, certFile, keyFile, caFile string) *Server {
	return &Server{
		controlListenAddr: controlListenAddr,
		publicListenAddr:  publicListenAddr,
		useTLS:            true,
		tlsCertFile:       certFile,
		tlsKeyFile:        keyFile,
		tlsCAFile:         caFile,
	}
}

// Run 启动服务器，监听控制端口和公开端口
func (s *Server) Run(ctx context.Context) error {
	// 启动控制端口监听器（支持 TLS）
	var controlListener net.Listener
	var err error

	if s.useTLS {
		// 使用 PQC mTLS（通过 OpenSSL）
		baseListener, err := net.Listen("tcp", s.controlListenAddr)
		if err != nil {
			return err
		}

		controlListener, err = pqctls.NewPQCListenerOpenSSL(baseListener, s.tlsCertFile, s.tlsKeyFile, s.tlsCAFile)
		if err != nil {
			baseListener.Close()
			return fmt.Errorf("创建 PQC TLS 监听器失败: %v", err)
		}
		log.Printf("控制端口监听器已启动 (PQC mTLS via OpenSSL): %s", s.controlListenAddr)
	} else {
		// 使用纯 TCP
		controlListener, err = net.Listen("tcp", s.controlListenAddr)
		if err != nil {
			return err
		}
		log.Printf("控制端口监听器已启动: %s", s.controlListenAddr)
	}
	defer controlListener.Close()

	// 启动公开端口监听器（如果已指定）
	var publicListener net.Listener
	if s.publicListenAddr != "" {
		publicListener, err = net.Listen("tcp", s.publicListenAddr)
		if err != nil {
			return err
		}
		defer publicListener.Close()
		log.Printf("公开端口监听器已启动: %s", s.publicListenAddr)
	} else {
		log.Printf("公开端口未指定，等待客户端配置...")
	}

	// 等待控制连接（client 连接）
	controlConnChan := make(chan net.Conn, 1)
	controlErrChan := make(chan error, 1)

	go func() {
		log.Printf("等待 client 连接...")
		conn, err := controlListener.Accept()
		if err != nil {
			controlErrChan <- err
			return
		}
		controlConnChan <- conn
	}()

	// 初始化公开连接通道
	s.publicConnChan = make(chan net.Conn)
	
	// 处理公开端口连接的 goroutine（如果已启动）
	if publicListener != nil {
		s.publicListenerMu.Lock()
		s.publicListener = publicListener
		s.publicListenerMu.Unlock()
		go s.acceptPublicConnections(ctx, publicListener)
	}

	// 主循环：等待控制连接建立，支持重连
	for {
		// 重新初始化控制连接等待通道
		controlConnChan = make(chan net.Conn, 1)
		controlErrChan = make(chan error, 1)

		go func() {
			log.Printf("等待 client 连接...")
			conn, err := controlListener.Accept()
			if err != nil {
				select {
				case <-ctx.Done():
					return
				default:
					controlErrChan <- err
				}
				return
			}
			controlConnChan <- conn
		}()

		// 等待控制连接建立
		select {
		case <-ctx.Done():
			log.Printf("服务器正在关闭...")
			s.cleanup()
			return ctx.Err()
		case err := <-controlErrChan:
			if err != nil {
				log.Printf("接受控制连接错误: %v", err)
				// 继续循环，等待重连
				continue
			}
		case controlConn := <-controlConnChan:
			s.controlMu.Lock()
			s.controlConn = controlConn
			s.controlMu.Unlock()
			log.Printf("client 已连接: %s", controlConn.RemoteAddr())

			// 启动从 client 读取帧的 goroutine
			clientDone := make(chan struct{})
			go func() {
				s.handleFramesFromClient(ctx, controlConn)
				close(clientDone)
			}()

			// 处理公开连接和上下文取消
			connectionActive := true
			for connectionActive {
				select {
				case <-ctx.Done():
					log.Printf("服务器正在关闭...")
					s.cleanup()
					return ctx.Err()
				case <-clientDone:
					// 控制连接断开，清理并等待重连
					log.Printf("控制连接已断开，等待客户端重连...")
					s.controlMu.Lock()
					s.controlConn = nil
					s.controlMu.Unlock()
					// 清理所有外部连接
					s.connMap.Range(func(key, value interface{}) bool {
						if conn, ok := value.(net.Conn); ok {
							conn.Close()
						}
						s.connMap.Delete(key)
						return true
					})
					connectionActive = false
					// 跳出内层循环，重新等待控制连接
				case publicConn := <-s.publicConnChan:
					// 有新的外部连接，创建 connID 并通知 client
					s.handlePublicConnection(ctx, publicConn)
				}
			}
		}
	}
}

// handlePublicConnection 处理新的公开连接
func (s *Server) handlePublicConnection(ctx context.Context, publicConn net.Conn) {
	// 生成新的 connID
	connID := atomic.AddUint32(&s.nextConnID, 1)
	log.Printf("新外部连接: %s, connID=%d", publicConn.RemoteAddr(), connID)

	// 将连接存入 map
	s.connMap.Store(connID, publicConn)

	// 发送 NEW_CONN 帧给 client
	s.controlMu.RLock()
	controlConn := s.controlConn
	s.controlMu.RUnlock()

	if controlConn == nil {
		log.Printf("错误: 控制连接不存在，关闭外部连接 connID=%d", connID)
		publicConn.Close()
		s.connMap.Delete(connID)
		return
	}

	frame := &proto.Frame{
		Type:    proto.FrameTypeNEW_CONN,
		ConnID:  connID,
		Payload: nil,
	}

	frameData, err := proto.EncodeFrame(frame)
	if err != nil {
		log.Printf("编码 NEW_CONN 帧错误 (connID=%d): %v", connID, err)
		publicConn.Close()
		s.connMap.Delete(connID)
		return
	}

	if _, err := controlConn.Write(frameData); err != nil {
		log.Printf("发送 NEW_CONN 帧错误 (connID=%d): %v", connID, err)
		publicConn.Close()
		s.connMap.Delete(connID)
		return
	}

	// 启动两个方向的转发：
	// 1. 从公开连接读取数据，发送 DATA 帧给 client
	// 2. 从 client 接收 DATA 帧（在 handleFramesFromClient 中处理）

	// 从公开连接读取并转发给 client
	go func() {
		defer func() {
			publicConn.Close()
			s.connMap.Delete(connID)
			log.Printf("外部连接已关闭: connID=%d", connID)
		}()

		buf := make([]byte, 4096)
		for {
			select {
			case <-ctx.Done():
				return
			default:
				n, err := publicConn.Read(buf)
				if err != nil {
					if err != io.EOF {
						log.Printf("读取公开连接数据错误 (connID=%d): %v", connID, err)
					}
					// 发送 CLOSE_CONN 帧
					s.sendCloseFrame(connID)
					return
				}

				if n > 0 {
					// 发送 DATA 帧给 client
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

					s.controlMu.RLock()
					ctrlConn := s.controlConn
					s.controlMu.RUnlock()

					if ctrlConn == nil {
						return
					}

					if _, err := ctrlConn.Write(frameData); err != nil {
						log.Printf("发送 DATA 帧错误 (connID=%d): %v", connID, err)
						return
					}
				}
			}
		}
	}()
}

// handleFramesFromClient 处理来自 client 的帧
func (s *Server) handleFramesFromClient(ctx context.Context, controlConn net.Conn) {
	defer func() {
		controlConn.Close()
		s.controlMu.Lock()
		s.controlConn = nil
		s.controlMu.Unlock()
		log.Printf("控制连接已关闭")
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
			frame, err := proto.DecodeFrame(controlConn)
			if err != nil {
				if err != io.EOF {
					log.Printf("解码帧错误: %v", err)
				}
				return
			}

			switch frame.Type {
			case proto.FrameTypeINIT:
				// 处理初始化配置（客户端指定远程端口）
				s.handleInitFrame(ctx, frame)
			case proto.FrameTypeDATA:
				// 将数据写入对应的外部连接
				s.handleDataFrame(frame)
			case proto.FrameTypeCLOSE:
				// 关闭对应的外部连接
				s.handleCloseFrame(frame)
			default:
				log.Printf("未知帧类型: %d, connID=%d", frame.Type, frame.ConnID)
			}
		}
	}
}

// handleDataFrame 处理来自 client 的 DATA 帧
func (s *Server) handleDataFrame(frame *proto.Frame) {
	conn, ok := s.connMap.Load(frame.ConnID)
	if !ok {
		log.Printf("警告: 未找到 connID=%d 对应的连接", frame.ConnID)
		return
	}

	publicConn, ok := conn.(net.Conn)
	if !ok {
		log.Printf("错误: connID=%d 对应的连接类型错误", frame.ConnID)
		return
	}

	// 将数据写入外部连接
	if len(frame.Payload) > 0 {
		if _, err := publicConn.Write(frame.Payload); err != nil {
			log.Printf("写入外部连接错误 (connID=%d): %v", frame.ConnID, err)
			// 连接可能已关闭，清理并发送 CLOSE_CONN
			publicConn.Close()
			s.connMap.Delete(frame.ConnID)
			s.sendCloseFrame(frame.ConnID)
		}
	}
}

// handleCloseFrame 处理来自 client 的 CLOSE_CONN 帧
func (s *Server) handleCloseFrame(frame *proto.Frame) {
	conn, ok := s.connMap.LoadAndDelete(frame.ConnID)
	if !ok {
		// 连接可能已经关闭
		return
	}

	publicConn, ok := conn.(net.Conn)
	if !ok {
		return
	}

	publicConn.Close()
	log.Printf("收到 CLOSE_CONN 帧，已关闭外部连接: connID=%d", frame.ConnID)
}

// sendCloseFrame 发送 CLOSE_CONN 帧给 client
func (s *Server) sendCloseFrame(connID uint32) {
	s.controlMu.RLock()
	controlConn := s.controlConn
	s.controlMu.RUnlock()

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

// acceptPublicConnections 接受公开端口连接
func (s *Server) acceptPublicConnections(ctx context.Context, listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				log.Printf("接受公开连接错误: %v", err)
				continue
			}
		}
		s.publicConnChan <- conn
	}
}

// handleInitFrame 处理初始化配置帧
func (s *Server) handleInitFrame(ctx context.Context, frame *proto.Frame) {
	// 如果服务器已经指定了公开端口，忽略客户端的配置
	if s.publicListenAddr != "" {
		log.Printf("服务器已指定公开端口，忽略客户端配置")
		return
	}

	// 解析配置
	config, err := proto.DecodeInitConfig(frame.Payload)
	if err != nil {
		log.Printf("解析 INIT 配置错误: %v", err)
		return
	}

	// 检查是否已经创建了监听器
	s.publicListenerMu.RLock()
	existingListener := s.publicListener
	s.publicListenerMu.RUnlock()

	if existingListener != nil {
		log.Printf("公开端口监听器已存在，忽略新的配置")
		return
	}

	// 创建新的公开端口监听器
	publicAddr := fmt.Sprintf(":%d", config.RemotePort)
	listener, err := net.Listen("tcp", publicAddr)
	if err != nil {
		log.Printf("创建公开端口监听器失败 (端口 %d): %v", config.RemotePort, err)
		return
	}

	s.publicListenerMu.Lock()
	s.publicListener = listener
	s.publicListenerMu.Unlock()

	log.Printf("根据客户端配置，公开端口监听器已启动: %s", publicAddr)

	// 启动接受连接的 goroutine
	go s.acceptPublicConnections(ctx, listener)
}

// cleanup 清理所有资源
func (s *Server) cleanup() {
	// 关闭控制连接
	s.controlMu.Lock()
	if s.controlConn != nil {
		s.controlConn.Close()
		s.controlConn = nil
	}
	s.controlMu.Unlock()

	// 关闭动态创建的公开端口监听器
	s.publicListenerMu.Lock()
	if s.publicListener != nil {
		s.publicListener.Close()
		s.publicListener = nil
	}
	s.publicListenerMu.Unlock()

	// 关闭所有外部连接
	s.connMap.Range(func(key, value interface{}) bool {
		if conn, ok := value.(net.Conn); ok {
			conn.Close()
		}
		s.connMap.Delete(key)
		return true
	})

	log.Printf("服务器资源已清理")
}
