package tunnel

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// TestReverseTunnelFlow 测试完整的反向隧道流程
func TestReverseTunnelFlow(t *testing.T) {
	// 1. 启动一个模拟的本地服务（echo server）
	localPort := getFreePort(t)
	localAddr := fmt.Sprintf("127.0.0.1:%d", localPort)
	
	localServer := startEchoServer(t, localAddr)
	defer localServer.Close()
	t.Logf("本地 echo 服务已启动: %s", localAddr)

	// 2. 启动反向隧道服务器
	controlPort := getFreePort(t)
	publicPort := getFreePort(t)
	controlAddr := fmt.Sprintf("127.0.0.1:%d", controlPort)
	publicAddr := fmt.Sprintf("127.0.0.1:%d", publicPort)

	server := NewServer(controlAddr, publicAddr)
	serverCtx, serverCancel := context.WithCancel(context.Background())
	defer serverCancel()

	// 在 goroutine 中运行服务器
	serverErrChan := make(chan error, 1)
	go func() {
		err := server.Run(serverCtx)
		if err != nil && err != context.Canceled {
			serverErrChan <- err
		}
	}()

	// 等待服务器启动
	time.Sleep(100 * time.Millisecond)
	t.Logf("反向隧道服务器已启动: control=%s, public=%s", controlAddr, publicAddr)

	// 3. 启动客户端
	client := NewClient(controlAddr, localAddr)
	clientCtx, clientCancel := context.WithCancel(context.Background())
	defer clientCancel()

	// 在 goroutine 中运行客户端
	clientErrChan := make(chan error, 1)
	go func() {
		err := client.Run(clientCtx)
		if err != nil && err != context.Canceled {
			clientErrChan <- err
		}
	}()

	// 等待客户端连接
	time.Sleep(500 * time.Millisecond)
	t.Log("客户端已连接")

	// 4. 模拟外部客户端连接到服务器的公开端口
	publicConn, err := net.DialTimeout("tcp", publicAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("连接公开端口失败: %v", err)
	}
	defer publicConn.Close()
	t.Log("外部客户端已连接到公开端口")

	// 等待连接建立和转发
	time.Sleep(200 * time.Millisecond)

	// 5. 测试数据转发：外部 -> 服务器 -> 客户端 -> 本地服务
	testMessage := "Hello, Reverse Tunnel!"
	_, err = publicConn.Write([]byte(testMessage))
	if err != nil {
		t.Fatalf("写入数据失败: %v", err)
	}
	t.Logf("已发送数据到公开端口: %s", testMessage)

	// 读取响应（应该从本地 echo 服务返回）
	publicConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	response := make([]byte, len(testMessage))
	n, err := io.ReadFull(publicConn, response)
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}

	received := string(response[:n])
	if received != testMessage {
		t.Errorf("响应不匹配: 期望 %q, 得到 %q", testMessage, received)
	} else {
		t.Logf("收到响应: %s", received)
	}

	// 6. 测试反向数据流：本地服务 -> 客户端 -> 服务器 -> 外部
	// 由于是 echo 服务，我们发送另一条消息
	testMessage2 := "Test Message 2"
	_, err = publicConn.Write([]byte(testMessage2))
	if err != nil {
		t.Fatalf("写入第二条数据失败: %v", err)
	}

	publicConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	response2 := make([]byte, len(testMessage2))
	n2, err := io.ReadFull(publicConn, response2)
	if err != nil {
		t.Fatalf("读取第二条响应失败: %v", err)
	}

	received2 := string(response2[:n2])
	if received2 != testMessage2 {
		t.Errorf("第二条响应不匹配: 期望 %q, 得到 %q", testMessage2, received2)
	} else {
		t.Logf("收到第二条响应: %s", received2)
	}

	// 7. 测试连接关闭
	publicConn.Close()
	time.Sleep(200 * time.Millisecond)

	// 检查是否有错误
	select {
	case err := <-serverErrChan:
		t.Errorf("服务器错误: %v", err)
	case err := <-clientErrChan:
		t.Errorf("客户端错误: %v", err)
	default:
		// 没有错误，正常
	}

	t.Log("测试完成: 所有数据转发正常")
}

// TestMultipleConnections 测试多个并发连接
func TestMultipleConnections(t *testing.T) {
	// 启动本地服务
	localPort := getFreePort(t)
	localAddr := fmt.Sprintf("127.0.0.1:%d", localPort)
	localServer := startEchoServer(t, localAddr)
	defer localServer.Close()

	// 启动服务器
	controlPort := getFreePort(t)
	publicPort := getFreePort(t)
	controlAddr := fmt.Sprintf("127.0.0.1:%d", controlPort)
	publicAddr := fmt.Sprintf("127.0.0.1:%d", publicPort)

	server := NewServer(controlAddr, publicAddr)
	serverCtx, serverCancel := context.WithCancel(context.Background())
	defer serverCancel()

	go server.Run(serverCtx)
	time.Sleep(100 * time.Millisecond)

	// 启动客户端
	client := NewClient(controlAddr, localAddr)
	clientCtx, clientCancel := context.WithCancel(context.Background())
	defer clientCancel()

	go client.Run(clientCtx)
	time.Sleep(500 * time.Millisecond)

	// 创建多个并发连接
	numConnections := 5
	successChan := make(chan bool, numConnections)

	for i := 0; i < numConnections; i++ {
		go func(id int) {
			conn, err := net.DialTimeout("tcp", publicAddr, 2*time.Second)
			if err != nil {
				t.Errorf("连接 %d 失败: %v", id, err)
				successChan <- false
				return
			}
			defer conn.Close()

			msg := fmt.Sprintf("Message from connection %d", id)
			_, err = conn.Write([]byte(msg))
			if err != nil {
				t.Errorf("写入连接 %d 失败: %v", id, err)
				successChan <- false
				return
			}

			conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			response := make([]byte, len(msg))
			_, err = io.ReadFull(conn, response)
			if err != nil {
				t.Errorf("读取连接 %d 响应失败: %v", id, err)
				successChan <- false
				return
			}

			if string(response) != msg {
				t.Errorf("连接 %d 响应不匹配", id)
				successChan <- false
				return
			}

			successChan <- true
		}(i)
	}

	// 等待所有连接完成
	successCount := 0
	for i := 0; i < numConnections; i++ {
		if <-successChan {
			successCount++
		}
	}

	if successCount != numConnections {
		t.Errorf("只有 %d/%d 个连接成功", successCount, numConnections)
	} else {
		t.Logf("所有 %d 个并发连接测试通过", numConnections)
	}
}

// startEchoServer 启动一个简单的 echo 服务器用于测试
func startEchoServer(t *testing.T, addr string) net.Listener {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("启动 echo 服务器失败: %v", err)
	}

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				// 监听器已关闭
				return
			}

			go func(c net.Conn) {
				defer c.Close()
				io.Copy(c, c) // echo: 将收到的数据原样返回
			}(conn)
		}
	}()

	return listener
}

// getFreePort 获取一个可用的端口
func getFreePort(t *testing.T) int {
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("获取空闲端口失败: %v", err)
	}
	defer listener.Close()

	addr := listener.Addr().(*net.TCPAddr)
	return addr.Port
}

// TestClientReconnect 测试客户端重连功能
func TestClientReconnect(t *testing.T) {
	localPort := getFreePort(t)
	localAddr := fmt.Sprintf("127.0.0.1:%d", localPort)
	localServer := startEchoServer(t, localAddr)
	defer localServer.Close()

	controlPort := getFreePort(t)
	publicPort := getFreePort(t)
	controlAddr := fmt.Sprintf("127.0.0.1:%d", controlPort)
	publicAddr := fmt.Sprintf("127.0.0.1:%d", publicPort)

	server := NewServer(controlAddr, publicAddr)
	serverCtx, serverCancel := context.WithCancel(context.Background())
	defer serverCancel()

	go server.Run(serverCtx)
	time.Sleep(100 * time.Millisecond)

	// 启动客户端
	client := NewClient(controlAddr, localAddr)
	clientCtx, clientCancel := context.WithCancel(context.Background())
	defer clientCancel()

	go client.Run(clientCtx)
	time.Sleep(500 * time.Millisecond)

	// 测试连接
	conn, err := net.DialTimeout("tcp", publicAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	conn.Close()

	// 关闭服务器（模拟服务器断开）
	serverCancel()
	time.Sleep(500 * time.Millisecond)

	// 重新启动服务器
	server2 := NewServer(controlAddr, publicAddr)
	serverCtx2, serverCancel2 := context.WithCancel(context.Background())
	defer serverCancel2()

	go server2.Run(serverCtx2)
	time.Sleep(100 * time.Millisecond)

	// 等待客户端重连（客户端重连间隔是5秒）
	// 通过轮询检查连接是否建立，最多等待15秒
	maxWait := 15 * time.Second
	checkInterval := 500 * time.Millisecond
	connected := false
	var conn2 net.Conn
	var dialErr error
	
	for elapsed := time.Duration(0); elapsed < maxWait; elapsed += checkInterval {
		conn2, dialErr = net.DialTimeout("tcp", publicAddr, 1*time.Second)
		if dialErr == nil {
			// 尝试发送数据验证连接是否真的可用
			testMsg := "Reconnect Test"
			_, writeErr := conn2.Write([]byte(testMsg))
			if writeErr == nil {
				conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
				response := make([]byte, len(testMsg))
				_, readErr := io.ReadFull(conn2, response)
				if readErr == nil && string(response) == testMsg {
					connected = true
					break
				}
			}
			conn2.Close()
		}
		time.Sleep(checkInterval)
	}

	if !connected {
		t.Fatalf("客户端在 %v 内未能重连或连接不可用: %v", maxWait, dialErr)
	}
	defer conn2.Close()

	// 再次测试确保连接稳定
	testMsg := "Reconnect Test 2"
	_, err = conn2.Write([]byte(testMsg))
	if err != nil {
		t.Fatalf("写入失败: %v", err)
	}

	conn2.SetReadDeadline(time.Now().Add(2 * time.Second))
	response := make([]byte, len(testMsg))
	_, err = io.ReadFull(conn2, response)
	if err != nil {
		t.Fatalf("读取响应失败: %v", err)
	}

	if string(response) != testMsg {
		t.Errorf("响应不匹配: 期望 %q, 得到 %q", testMsg, string(response))
	} else {
		t.Log("客户端重连测试通过")
	}
}

// TestLargeDataTransfer 测试大数据传输
func TestLargeDataTransfer(t *testing.T) {
	localPort := getFreePort(t)
	localAddr := fmt.Sprintf("127.0.0.1:%d", localPort)
	localServer := startEchoServer(t, localAddr)
	defer localServer.Close()

	controlPort := getFreePort(t)
	publicPort := getFreePort(t)
	controlAddr := fmt.Sprintf("127.0.0.1:%d", controlPort)
	publicAddr := fmt.Sprintf("127.0.0.1:%d", publicPort)

	server := NewServer(controlAddr, publicAddr)
	serverCtx, serverCancel := context.WithCancel(context.Background())
	defer serverCancel()

	go server.Run(serverCtx)
	time.Sleep(100 * time.Millisecond)

	client := NewClient(controlAddr, localAddr)
	clientCtx, clientCancel := context.WithCancel(context.Background())
	defer clientCancel()

	go client.Run(clientCtx)
	time.Sleep(500 * time.Millisecond)

	conn, err := net.DialTimeout("tcp", publicAddr, 2*time.Second)
	if err != nil {
		t.Fatalf("连接失败: %v", err)
	}
	defer conn.Close()

	// 发送较大的数据（100KB）
	largeData := strings.Repeat("A", 100*1024)
	_, err = conn.Write([]byte(largeData))
	if err != nil {
		t.Fatalf("写入大数据失败: %v", err)
	}

	// 读取响应
	conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	response := make([]byte, len(largeData))
	n, err := io.ReadFull(conn, response)
	if err != nil {
		t.Fatalf("读取大数据响应失败: %v (读取了 %d 字节)", err, n)
	}

	if string(response) != largeData {
		t.Errorf("大数据响应不匹配: 长度 %d vs %d", len(response), len(largeData))
	} else {
		t.Logf("大数据传输测试通过: %d 字节", len(largeData))
	}
}
