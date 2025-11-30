package config

import (
	"encoding/json"
	"fmt"
	"os"
)

// ServerConfig 服务器配置
type ServerConfig struct {
	ControlListen string `json:"control_listen"` // 控制端口监听地址（默认 :7000）
	PublicListen  string `json:"public_listen"`  // 公开端口监听地址（可选，留空则由客户端指定）
	
	// PQC mTLS 配置（可选）
	TLS struct {
		Enabled bool   `json:"enabled"` // 是否启用 PQC mTLS
		Cert    string `json:"cert"`    // 服务器证书文件路径
		Key     string `json:"key"`     // 服务器私钥文件路径
		CA      string `json:"ca"`      // CA 证书文件路径（用于验证客户端证书）
	} `json:"tls"`
}

// ClientConfig 客户端配置
type ClientConfig struct {
	Server     string `json:"server"`      // 服务器地址（例如 1.2.3.4:7000，必填）
	Local      string `json:"local"`       // 本地服务地址（例如 127.0.0.1:80，必填）
	RemotePort int    `json:"remote_port"` // 远程端口（服务器要监听的端口，0 表示由服务器指定）
	
	// PQC mTLS 配置（可选）
	TLS struct {
		Enabled    bool   `json:"enabled"`         // 是否启用 PQC mTLS
		Cert       string `json:"cert"`            // 客户端证书文件路径
		Key        string `json:"key"`            // 客户端私钥文件路径
		CA         string `json:"ca"`            // CA 证书文件路径（用于验证服务器证书）
		ServerName string `json:"server_name"`    // 服务器名称（TLS SNI，留空则使用服务器地址）
	} `json:"tls"`
}

// LoadServerConfig 从 JSON 文件加载服务器配置
func LoadServerConfig(configPath string) (*ServerConfig, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var config ServerConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 设置默认值
	if config.ControlListen == "" {
		config.ControlListen = ":7000"
	}

	return &config, nil
}

// LoadClientConfig 从 JSON 文件加载客户端配置
func LoadClientConfig(configPath string) (*ClientConfig, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("读取配置文件失败: %w", err)
	}

	var config ClientConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("解析配置文件失败: %w", err)
	}

	// 验证必填字段
	if config.Server == "" {
		return nil, fmt.Errorf("配置文件中 server 字段必填")
	}
	if config.Local == "" {
		return nil, fmt.Errorf("配置文件中 local 字段必填")
	}

	return &config, nil
}

