package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"reverse-tunnel/internal/tunnel"
)

func main() {
	// 解析命令行参数
	controlListen := flag.String("control-listen", ":7000", "控制/隧道端口监听地址（供 client 连接）")
	publicListen := flag.String("public-listen", "", "对外暴露的端口监听地址（供外部访问，留空则由客户端指定）")
	
	// PQC mTLS 参数
	useTLS := flag.Bool("tls", false, "启用 PQC mTLS")
	tlsCert := flag.String("tls-cert", "/root/pq-certs/server.crt", "服务器证书文件路径")
	tlsKey := flag.String("tls-key", "/root/pq-certs/server.key", "服务器私钥文件路径")
	tlsCA := flag.String("tls-ca", "/root/pq-certs/ca.crt", "CA 证书文件路径（用于验证客户端证书）")
	
	flag.Parse()

	// 创建支持优雅退出的 context
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 监听系统信号（Ctrl+C, SIGTERM）
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// 在 goroutine 中处理信号，触发 context 取消
	go func() {
		sig := <-sigChan
		log.Printf("收到信号: %v，开始优雅退出...", sig)
		cancel()
	}()

	// 打印启动信息
	log.Printf("反向隧道服务器启动中...")
	log.Printf("控制端口监听: %s", *controlListen)
	if *publicListen != "" {
		log.Printf("对外端口监听: %s", *publicListen)
	} else {
		log.Printf("对外端口: 由客户端指定")
	}
	if *useTLS {
		log.Printf("PQC mTLS: 已启用")
		log.Printf("  证书: %s", *tlsCert)
		log.Printf("  私钥: %s", *tlsKey)
		log.Printf("  CA: %s", *tlsCA)
	}

	// 创建并运行服务器
	var server *tunnel.Server
	if *useTLS {
		server = tunnel.NewServerWithTLS(*controlListen, *publicListen, *tlsCert, *tlsKey, *tlsCA)
	} else {
		server = tunnel.NewServer(*controlListen, *publicListen)
	}
	if err := server.Run(ctx); err != nil {
		// context.Canceled 是正常的退出情况（如 Ctrl+C），不视为错误
		if err != context.Canceled {
			log.Printf("服务器运行错误: %v", err)
			os.Exit(1)
		}
	}

	log.Printf("服务器已退出")
}
