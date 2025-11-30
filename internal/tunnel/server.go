package tunnel

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"sync/atomic"

	"reverse-tunnel/internal/proto"
	"reverse-tunnel/internal/pqctls"
)

// ClientInfo 表示一个客户端的信息
type ClientInfo struct {
	ID           string      // 客户端唯一标识
	Conn         net.Conn    // 控制连接
	ConnMap      sync.Map    // map[uint32]net.Conn - 该客户端的连接映射
	NextConnID   uint32      // 该客户端的下一个连接ID
	LocalAddr    string      // 客户端本地地址（从INIT帧获取）
	RemotePort   int         // 客户端指定的远程端口
	PublicListener net.Listener // 该客户端专用的公开端口监听器（如果指定了远程端口）
}

// Server 表示反向隧道服务器
type Server struct {
	controlListenAddr string // 控制端口监听地址
	publicListenAddr  string // 公开端口监听地址（可选，如果为空则由客户端指定）

	// PQC mTLS 配置（可选）
	useTLS     bool
	tlsCertFile string
	tlsKeyFile  string
	tlsCAFile   string

	// 多客户端支持：管理所有客户端连接
	clients     map[string]*ClientInfo // map[clientID]*ClientInfo
	clientsMu   sync.RWMutex
	
	// 全局公开端口监听器（如果服务器指定了公开端口，所有客户端共享）
	publicListener net.Listener
	publicListenerMu sync.RWMutex
	
	// 公开连接通道（用于全局监听器）
	publicConnChan chan net.Conn
	
	// 下一个客户端ID
	nextClientID uint32
}

// NewServer 创建一个新的服务器实例
func NewServer(controlListenAddr, publicListenAddr string) *Server {
	return &Server{
		controlListenAddr: controlListenAddr,
		publicListenAddr:  publicListenAddr,
		useTLS:            false,
		clients:           make(map[string]*ClientInfo),
		publicConnChan:    make(chan net.Conn, 100), // 缓冲通道，支持多个连接
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
		clients:           make(map[string]*ClientInfo),
		publicConnChan:    make(chan net.Conn, 100), // 缓冲通道，支持多个连接
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

	// 处理公开端口连接的 goroutine（如果已启动全局监听器）
	if publicListener != nil {
		s.publicListenerMu.Lock()
		s.publicListener = publicListener
		s.publicListenerMu.Unlock()
		go s.acceptPublicConnections(ctx, publicListener)
	}

	// 持续接受客户端连接的 goroutine
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			default:
				log.Printf("等待 client 连接...")
				conn, err := controlListener.Accept()
				if err != nil {
					if ctx.Err() != nil {
						return
					}
					log.Printf("接受控制连接错误: %v", err)
					continue
				}
				
				// 为新客户端分配ID并注册
				clientID := s.registerClient(conn)
				log.Printf("客户端已连接: %s (clientID=%s)", conn.RemoteAddr(), clientID)
				
				// 为每个客户端启动独立的帧处理 goroutine
				go s.handleClientConnection(ctx, clientID, conn)
			}
		}
	}()

	// 等待上下文取消
	<-ctx.Done()
	log.Printf("服务器正在关闭...")
	s.cleanup()
	return ctx.Err()
}

// registerClient 注册新客户端并返回clientID
func (s *Server) registerClient(conn net.Conn) string {
	clientID := fmt.Sprintf("client-%d", atomic.AddUint32(&s.nextClientID, 1))
	
	clientInfo := &ClientInfo{
		ID:         clientID,
		Conn:       conn,
		NextConnID: 0,
	}
	
	s.clientsMu.Lock()
	s.clients[clientID] = clientInfo
	s.clientsMu.Unlock()
	
	return clientID
}

// unregisterClient 注销客户端
func (s *Server) unregisterClient(clientID string) {
	s.clientsMu.Lock()
	defer s.clientsMu.Unlock()
	
	clientInfo, ok := s.clients[clientID]
	if !ok {
		return
	}
	
	// 清理该客户端的所有连接
	clientInfo.ConnMap.Range(func(key, value interface{}) bool {
		if conn, ok := value.(net.Conn); ok {
			conn.Close()
		}
		clientInfo.ConnMap.Delete(key)
		return true
	})
	
	// 关闭该客户端的公开端口监听器
	if clientInfo.PublicListener != nil {
		clientInfo.PublicListener.Close()
	}
	
	// 关闭控制连接
	if clientInfo.Conn != nil {
		clientInfo.Conn.Close()
	}
	
	delete(s.clients, clientID)
	log.Printf("客户端已注销: %s", clientID)
}

// handleClientConnection 处理单个客户端连接
func (s *Server) handleClientConnection(ctx context.Context, clientID string, conn net.Conn) {
	defer func() {
		s.unregisterClient(clientID)
	}()
	
	// 启动从客户端读取帧的 goroutine
	s.handleFramesFromClient(ctx, clientID, conn)
}

// handlePublicConnection 处理新的公开连接
// 注意：这个方法需要知道应该转发到哪个客户端
// 当前实现：如果只有一个客户端，转发给它；如果有多个，需要根据端口或其他方式路由
func (s *Server) handlePublicConnection(ctx context.Context, publicConn net.Conn, clientID string) {
	// 获取客户端信息
	s.clientsMu.RLock()
	clientInfo, ok := s.clients[clientID]
	s.clientsMu.RUnlock()
	
	if !ok {
		log.Printf("错误: 客户端不存在 (clientID=%s)，关闭外部连接", clientID)
		publicConn.Close()
		return
	}
	
	// 为该客户端生成新的 connID
	connID := atomic.AddUint32(&clientInfo.NextConnID, 1)
	log.Printf("新外部连接: %s, clientID=%s, connID=%d", publicConn.RemoteAddr(), clientID, connID)

	// 先发送 NEW_CONN 帧，等待客户端建立本地连接
	// 注意：此时先不将连接存入 map，等客户端确认建立成功后再存入
	frame := &proto.Frame{
		Type:    proto.FrameTypeNEW_CONN,
		ConnID:  connID,
		Payload: nil,
	}

	frameData, err := proto.EncodeFrame(frame)
	if err != nil {
		log.Printf("编码 NEW_CONN 帧错误 (clientID=%s, connID=%d): %v", clientID, connID, err)
		publicConn.Close()
		return
	}

	if _, err := clientInfo.Conn.Write(frameData); err != nil {
		log.Printf("发送 NEW_CONN 帧错误 (clientID=%s, connID=%d): %v", clientID, connID, err)
		publicConn.Close()
		return
	}

	// 将连接存入该客户端的 map（在发送 NEW_CONN 之后）
	// 这样即使客户端连接本地服务失败，我们也能正确处理 CLOSE_CONN
	clientInfo.ConnMap.Store(connID, publicConn)

	// 启动两个方向的转发：
	// 1. 从公开连接读取数据，发送 DATA 帧给 client
	// 2. 从 client 接收 DATA 帧（在 handleFramesFromClient 中处理）

	// 从公开连接读取并转发给 client
	// 注意：这里立即开始读取，但如果客户端连接本地服务失败，可能会收到 CLOSE_CONN
	// 此时连接会被客户端关闭，导致 "use of closed network connection" 错误
	go func() {
		defer func() {
			// 检查连接是否还在 map 中（可能已经被 handleCloseFrame 删除了）
			if _, exists := clientInfo.ConnMap.Load(connID); exists {
				publicConn.Close()
				clientInfo.ConnMap.Delete(connID)
				log.Printf("外部连接已关闭: clientID=%s, connID=%d", clientID, connID)
			}
		}()

		buf := make([]byte, 4096)
		for {
			select {
			case <-ctx.Done():
				return
			default:
				// 检查连接是否还在 map 中
				if _, exists := clientInfo.ConnMap.Load(connID); !exists {
					// 连接已经被删除（可能是客户端发送了 CLOSE_CONN）
					return
				}
				
				n, err := publicConn.Read(buf)
				if err != nil {
					// 检查是否是连接关闭错误
					if err != io.EOF {
						// 检查是否是 "use of closed network connection" 错误
						// 这通常发生在客户端已经关闭了本地连接并发送了 CLOSE_CONN
						errStr := err.Error()
						if strings.Contains(errStr, "use of closed network connection") {
							// 连接已经被关闭，可能是客户端主动关闭的（连接本地服务失败）
							// 不需要再发送 CLOSE_CONN，因为客户端已经发送了
							log.Printf("公开连接已关闭 (clientID=%s, connID=%d)，可能是客户端连接本地服务失败", clientID, connID)
						} else {
							log.Printf("读取公开连接数据错误 (clientID=%s, connID=%d): %v", clientID, connID, err)
							// 发送 CLOSE_CONN 帧通知客户端
							s.sendCloseFrame(clientID, connID)
						}
					} else {
						// EOF，正常关闭
						s.sendCloseFrame(clientID, connID)
					}
					return
				}

				if n > 0 {
					// 检查连接是否还在 map 中（可能在读取期间被关闭了）
					if _, exists := clientInfo.ConnMap.Load(connID); !exists {
						return
					}
					
					// 发送 DATA 帧给 client
					dataFrame := &proto.Frame{
						Type:    proto.FrameTypeDATA,
						ConnID:  connID,
						Payload: buf[:n],
					}

					frameData, err := proto.EncodeFrame(dataFrame)
					if err != nil {
						log.Printf("编码 DATA 帧错误 (clientID=%s, connID=%d): %v", clientID, connID, err)
						return
					}

					if _, err := clientInfo.Conn.Write(frameData); err != nil {
						log.Printf("发送 DATA 帧错误 (clientID=%s, connID=%d): %v", clientID, connID, err)
						return
					}
				}
			}
		}
	}()
}

// handleFramesFromClient 处理来自 client 的帧
func (s *Server) handleFramesFromClient(ctx context.Context, clientID string, conn net.Conn) {
	defer func() {
		conn.Close()
		log.Printf("控制连接已关闭: clientID=%s", clientID)
	}()

	for {
		select {
		case <-ctx.Done():
			return
		default:
			frame, err := proto.DecodeFrame(conn)
			if err != nil {
				if err != io.EOF {
					log.Printf("解码帧错误 (clientID=%s): %v", clientID, err)
				}
				return
			}

			switch frame.Type {
			case proto.FrameTypeINIT:
				// 处理初始化配置（客户端指定远程端口）
				s.handleInitFrame(ctx, clientID, frame)
			case proto.FrameTypeDATA:
				// 将数据写入对应的外部连接
				s.handleDataFrame(clientID, frame)
			case proto.FrameTypeCLOSE:
				// 关闭对应的外部连接
				s.handleCloseFrame(clientID, frame)
			default:
				log.Printf("未知帧类型: %d, clientID=%s, connID=%d", frame.Type, clientID, frame.ConnID)
			}
		}
	}
}

// handleDataFrame 处理来自 client 的 DATA 帧
func (s *Server) handleDataFrame(clientID string, frame *proto.Frame) {
	// 获取客户端信息
	s.clientsMu.RLock()
	clientInfo, ok := s.clients[clientID]
	s.clientsMu.RUnlock()
	
	if !ok {
		log.Printf("警告: 客户端不存在 (clientID=%s)", clientID)
		return
	}
	
	conn, ok := clientInfo.ConnMap.Load(frame.ConnID)
	if !ok {
		log.Printf("警告: 未找到连接 (clientID=%s, connID=%d)", clientID, frame.ConnID)
		return
	}

	publicConn, ok := conn.(net.Conn)
	if !ok {
		log.Printf("错误: 连接类型错误 (clientID=%s, connID=%d)", clientID, frame.ConnID)
		return
	}

	// 将数据写入外部连接
	if len(frame.Payload) > 0 {
		if _, err := publicConn.Write(frame.Payload); err != nil {
			log.Printf("写入外部连接错误 (clientID=%s, connID=%d): %v", clientID, frame.ConnID, err)
			// 连接可能已关闭，清理并发送 CLOSE_CONN
			publicConn.Close()
			clientInfo.ConnMap.Delete(frame.ConnID)
			s.sendCloseFrame(clientID, frame.ConnID)
		}
	}
}

// handleCloseFrame 处理来自 client 的 CLOSE_CONN 帧
func (s *Server) handleCloseFrame(clientID string, frame *proto.Frame) {
	// 获取客户端信息
	s.clientsMu.RLock()
	clientInfo, ok := s.clients[clientID]
	s.clientsMu.RUnlock()
	
	if !ok {
		log.Printf("警告: 收到 CLOSE_CONN 帧但客户端不存在 (clientID=%s, connID=%d)", clientID, frame.ConnID)
		return
	}
	
	// 尝试删除连接（可能已经被读取 goroutine 删除了）
	conn, ok := clientInfo.ConnMap.LoadAndDelete(frame.ConnID)
	if !ok {
		// 连接可能已经关闭，这是正常的（可能客户端连接本地服务失败，或读取 goroutine 已经关闭）
		// 不记录日志，避免日志噪音
		return
	}

	publicConn, ok := conn.(net.Conn)
	if !ok {
		return
	}

	// 关闭外部连接
	publicConn.Close()
	log.Printf("收到 CLOSE_CONN 帧，已关闭外部连接: clientID=%s, connID=%d", clientID, frame.ConnID)
}

// sendCloseFrame 发送 CLOSE_CONN 帧给 client
func (s *Server) sendCloseFrame(clientID string, connID uint32) {
	// 获取客户端信息
	s.clientsMu.RLock()
	clientInfo, ok := s.clients[clientID]
	s.clientsMu.RUnlock()
	
	if !ok || clientInfo.Conn == nil {
		return
	}

	frame := &proto.Frame{
		Type:    proto.FrameTypeCLOSE,
		ConnID:  connID,
		Payload: nil,
	}

	frameData, err := proto.EncodeFrame(frame)
	if err != nil {
		log.Printf("编码 CLOSE_CONN 帧错误 (clientID=%s, connID=%d): %v", clientID, connID, err)
		return
	}

	if _, err := clientInfo.Conn.Write(frameData); err != nil {
		log.Printf("发送 CLOSE_CONN 帧错误 (clientID=%s, connID=%d): %v", clientID, connID, err)
	}
}

// acceptPublicConnections 接受公开端口连接（全局监听器）
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
		
		// 对于全局监听器，需要路由到某个客户端
		// 当前实现：路由到第一个可用的客户端（简单策略）
		// 未来可以改进：通过某种标识（如SNI、路径等）路由到特定客户端
		s.clientsMu.RLock()
		var targetClientID string
		for id := range s.clients {
			targetClientID = id
			break // 使用第一个客户端
		}
		s.clientsMu.RUnlock()
		
		if targetClientID == "" {
			log.Printf("警告: 没有可用的客户端，关闭公开连接: %s", conn.RemoteAddr())
			conn.Close()
			continue
		}
		
		// 转发到目标客户端
		s.handlePublicConnection(ctx, conn, targetClientID)
	}
}

// acceptPublicConnectionsForClient 为特定客户端接受公开端口连接
func (s *Server) acceptPublicConnectionsForClient(ctx context.Context, clientID string, listener net.Listener) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				log.Printf("接受公开连接错误 (clientID=%s): %v", clientID, err)
				continue
			}
		}
		
		// 直接转发到指定客户端
		s.handlePublicConnection(ctx, conn, clientID)
	}
}

// handleInitFrame 处理初始化配置帧
func (s *Server) handleInitFrame(ctx context.Context, clientID string, frame *proto.Frame) {
	// 获取客户端信息
	s.clientsMu.Lock()
	clientInfo, ok := s.clients[clientID]
	s.clientsMu.Unlock()
	
	if !ok {
		log.Printf("错误: 客户端不存在 (clientID=%s)", clientID)
		return
	}
	
	// 如果服务器已经指定了公开端口，客户端使用全局监听器
	if s.publicListenAddr != "" {
		log.Printf("服务器已指定公开端口，客户端 %s 使用全局监听器", clientID)
		clientInfo.LocalAddr = ""
		clientInfo.RemotePort = 0
		return
	}

	// 解析配置
	config, err := proto.DecodeInitConfig(frame.Payload)
	if err != nil {
		log.Printf("解析 INIT 配置错误 (clientID=%s): %v", clientID, err)
		return
	}

	// 更新客户端信息
	clientInfo.LocalAddr = config.LocalAddr
	clientInfo.RemotePort = config.RemotePort

	// 如果客户端指定了远程端口，为该客户端创建独立的监听器
	if config.RemotePort > 0 {
		// 检查该客户端是否已经有监听器
		if clientInfo.PublicListener != nil {
			log.Printf("客户端 %s 的公开端口监听器已存在，忽略新配置", clientID)
			return
		}

		// 创建该客户端专用的公开端口监听器
		publicAddr := fmt.Sprintf(":%d", config.RemotePort)
		listener, err := net.Listen("tcp", publicAddr)
		if err != nil {
			log.Printf("创建公开端口监听器失败 (clientID=%s, 端口 %d): %v", clientID, config.RemotePort, err)
			return
		}

		clientInfo.PublicListener = listener
		log.Printf("根据客户端 %s 配置，公开端口监听器已启动: %s", clientID, publicAddr)

		// 启动接受连接的 goroutine（专门为该客户端）
		go s.acceptPublicConnectionsForClient(ctx, clientID, listener)
	}
}

// cleanup 清理所有资源
func (s *Server) cleanup() {
	// 清理所有客户端
	s.clientsMu.Lock()
	for clientID := range s.clients {
		s.unregisterClient(clientID)
	}
	s.clients = make(map[string]*ClientInfo)
	s.clientsMu.Unlock()

	// 关闭全局公开端口监听器
	s.publicListenerMu.Lock()
	if s.publicListener != nil {
		s.publicListener.Close()
		s.publicListener = nil
	}
	s.publicListenerMu.Unlock()

	log.Printf("服务器资源已清理")
}
