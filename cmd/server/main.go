package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"reverse-tunnel/internal/config"
	"reverse-tunnel/internal/tunnel"
)

func main() {
	// 解析命令行参数
	configFile := flag.String("config", "", "配置文件路径（JSON 格式，如果指定则忽略其他命令行参数）")
	controlListen := flag.String("control-listen", ":7000", "控制/隧道端口监听地址（供 client 连接）")
	publicListen := flag.String("public-listen", "", "对外暴露的端口监听地址（供外部访问，留空则由客户端指定）")
	
	// PQC mTLS 参数
	useTLS := flag.Bool("tls", false, "启用 PQC mTLS")
	tlsCert := flag.String("tls-cert", "/root/pq-certs/server.crt", "服务器证书文件路径")
	tlsKey := flag.String("tls-key", "/root/pq-certs/server.key", "服务器私钥文件路径")
	tlsCA := flag.String("tls-ca", "/root/pq-certs/ca.crt", "CA 证书文件路径（用于验证客户端证书）")
	
	flag.Parse()

	// 如果指定了配置文件，从配置文件加载
	var cfg *config.ServerConfig
	if *configFile != "" {
		var err error
		cfg, err = config.LoadServerConfig(*configFile)
		if err != nil {
			log.Fatalf("加载配置文件失败: %v", err)
		}
		log.Printf("已从配置文件加载: %s", *configFile)
	} else {
		// 否则使用命令行参数
		cfg = &config.ServerConfig{
			ControlListen: *controlListen,
			PublicListen:  *publicListen,
		}
		cfg.TLS.Enabled = *useTLS
		cfg.TLS.Cert = *tlsCert
		cfg.TLS.Key = *tlsKey
		cfg.TLS.CA = *tlsCA
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

	// 打印启动信息
	log.Printf("反向隧道服务器启动中...")
	log.Printf("控制端口监听: %s", cfg.ControlListen)
	if cfg.PublicListen != "" {
		log.Printf("对外端口监听: %s", cfg.PublicListen)
	} else {
		log.Printf("对外端口: 由客户端指定")
	}
	if cfg.TLS.Enabled {
		log.Printf("PQC mTLS: 已启用")
		log.Printf("  证书: %s", cfg.TLS.Cert)
		log.Printf("  私钥: %s", cfg.TLS.Key)
		log.Printf("  CA: %s", cfg.TLS.CA)
	}

	// 创建并运行服务器
	var server *tunnel.Server
	if cfg.TLS.Enabled {
		server = tunnel.NewServerWithTLS(cfg.ControlListen, cfg.PublicListen, cfg.TLS.Cert, cfg.TLS.Key, cfg.TLS.CA)
	} else {
		server = tunnel.NewServer(cfg.ControlListen, cfg.PublicListen)
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
