package pqctls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io/ioutil"
	"log"
	"net"
)

// 如果编译时启用了 cgo，优先使用 OpenSSL 实现
// 否则回退到 Go 标准库实现（不支持 PQC）

// PQCTLSConfig 表示 PQC mTLS 配置
type PQCTLSConfig struct {
	CertFile   string // 证书文件路径
	KeyFile    string // 私钥文件路径
	CAFile     string // CA 证书文件路径（用于验证对端）
	ServerName string // 服务器名称（客户端使用）
}

// NewServerTLSConfig 创建服务器端 TLS 配置（mTLS）
// 注意：Go 标准库不支持 PQC 算法，如果使用 PQC 证书，请使用 NewPQCListenerOpenSSL
func NewServerTLSConfig(cfg *PQCTLSConfig) (*tls.Config, error) {
	// 加载服务器证书和私钥
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("加载服务器证书失败: %v", err)
	}

	// 加载 CA 证书用于验证客户端证书
	caCert, err := ioutil.ReadFile(cfg.CAFile)
	if err != nil {
		return nil, fmt.Errorf("加载 CA 证书失败: %v", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("解析 CA 证书失败")
	}

	config := &tls.Config{
		Certificates: []tls.Certificate{cert},
		ClientAuth:   tls.RequireAndVerifyClientCert, // 要求客户端证书（mTLS）
		ClientCAs:    caCertPool,
		MinVersion:   tls.VersionTLS13, // 使用 TLS 1.3
		MaxVersion:   tls.VersionTLS13,
	}

	log.Printf("PQC mTLS 服务器配置已加载: 证书=%s, CA=%s", cfg.CertFile, cfg.CAFile)
	return config, nil
}

// NewClientTLSConfig 创建客户端 TLS 配置（mTLS）
// 注意：Go 标准库不支持 PQC 算法，如果使用 PQC 证书，请使用 NewPQCDialerOpenSSL
func NewClientTLSConfig(cfg *PQCTLSConfig) (*tls.Config, error) {
	// 加载客户端证书和私钥
	cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("加载客户端证书失败: %v", err)
	}

	// 加载 CA 证书用于验证服务器证书
	caCert, err := ioutil.ReadFile(cfg.CAFile)
	if err != nil {
		return nil, fmt.Errorf("加载 CA 证书失败: %v", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("解析 CA 证书失败")
	}

	config := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
		ServerName:   cfg.ServerName,
		MinVersion:   tls.VersionTLS13, // 使用 TLS 1.3
		MaxVersion:   tls.VersionTLS13,
	}

	log.Printf("PQC mTLS 客户端配置已加载: 证书=%s, CA=%s", cfg.CertFile, cfg.CAFile)
	return config, nil
}

// ListenTLS 创建一个 TLS 监听器
func ListenTLS(network, address string, config *tls.Config) (net.Listener, error) {
	listener, err := net.Listen(network, address)
	if err != nil {
		return nil, err
	}

	return tls.NewListener(listener, config), nil
}

// DialTLS 创建一个 TLS 客户端连接
func DialTLS(network, address string, config *tls.Config) (net.Conn, error) {
	return tls.Dial(network, address, config)
}

