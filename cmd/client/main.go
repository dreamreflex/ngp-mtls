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
	serverAddr := flag.String("server", "", "服务器地址（例如 1.2.3.4:7000，必填）")
	localAddr := flag.String("local", "", "本地服务地址（例如 127.0.0.1:80，必填）")
	remotePort := flag.Int("remote-port", 0, "远程端口（服务器要监听的端口，0 表示由服务器指定，可选）")
	
	// PQC mTLS 参数
	useTLS := flag.Bool("tls", false, "启用 PQC mTLS")
	tlsCert := flag.String("tls-cert", "/root/pq-certs/client.crt", "客户端证书文件路径")
	tlsKey := flag.String("tls-key", "/root/pq-certs/client.key", "客户端私钥文件路径")
	tlsCA := flag.String("tls-ca", "/root/pq-certs/ca.crt", "CA 证书文件路径（用于验证服务器证书）")
	serverName := flag.String("tls-server-name", "", "服务器名称（TLS SNI，留空则使用服务器地址）")
	
	flag.Parse()

	// 验证必填参数
	if *serverAddr == "" {
		log.Fatal("错误: --server 参数必填，例如 --server=1.2.3.4:7000")
	}
	if *localAddr == "" {
		log.Fatal("错误: --local 参数必填，例如 --local=127.0.0.1:80")
	}

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

	// 打印启动信息和映射关系
	log.Printf("反向隧道客户端启动中...")
	if *remotePort > 0 {
		log.Printf("映射关系: server:%s:%d -> local:%s", *serverAddr, *remotePort, *localAddr)
	} else {
		log.Printf("映射关系: server:%s -> local:%s (远程端口由服务器指定)", *serverAddr, *localAddr)
	}
	if *useTLS {
		log.Printf("PQC mTLS: 已启用")
		log.Printf("  证书: %s", *tlsCert)
		log.Printf("  私钥: %s", *tlsKey)
		log.Printf("  CA: %s", *tlsCA)
	}

	// 创建并运行客户端
	var client *tunnel.Client
	if *useTLS {
		sn := *serverName
		if sn == "" {
			sn = *serverAddr
		}
		client = tunnel.NewClientWithTLS(*serverAddr, *localAddr, *remotePort, *tlsCert, *tlsKey, *tlsCA, sn)
	} else {
		client = tunnel.NewClient(*serverAddr, *localAddr, *remotePort)
	}
	if err := client.Run(ctx); err != nil {
		// context.Canceled 是正常的退出情况（如 Ctrl+C），不视为错误
		if err != context.Canceled {
			log.Printf("客户端运行错误: %v", err)
			os.Exit(1)
		}
	}

	log.Printf("客户端已退出")
}
