# 反向映射隧道（PQC试验版）

这是一个建立反向映射隧道的程序，支持一个Client/Server对进行PQC加密握手，使用 Go 语言实现。

注意事项：这是一个实验版本，需要使用OpenSSL3.5进行编译。

## 功能特性

- **反向端口映射**：将内网服务映射到公网服务器端口
- **动态端口配置**：客户端可以指定服务器要监听的远程端口
- **自动重连**：客户端支持断线自动重连
- **多连接支持**：支持多个并发外部连接
- **双向数据传输**：完整支持双向数据转发
- **PQC mTLS 支持**：支持后量子密码（PQC）证书的双向 TLS 认证

## 架构说明

系统包含两个独立的程序：

1. **server**：运行在公网服务器上
   - 监听控制端口（默认 7000）：接收客户端连接
   - 监听公开端口（默认 8080 或由客户端指定）：接受外部访问

2. **client**：运行在内网机器上
   - 主动连接服务器的控制端口
   - 将本地服务（如 127.0.0.1:80）映射到服务器的公开端口

### 工作流程

```
外部用户 → server:8080 → 控制连接 → client → 本地服务:80
```

1. 客户端主动连接到服务器的控制端口（7000），建立持久连接
2. 当有外部用户连接服务器的公开端口时：
   - 服务器分配一个 connID
   - 通过控制连接发送 NEW_CONN 帧给客户端
   - 客户端创建到本地服务的连接
   - 建立双向数据转发通道
3. 数据通过自定义帧协议在控制连接上传输

### 协议格式

每个帧的格式：

```
| 1 byte frame_type | 4 bytes conn_id | 4 bytes payload_len | payload... |
```

帧类型：
- `0x01` - NEW_CONN：新连接请求（server → client）
- `0x02` - DATA：数据传输（双向）
- `0x03` - CLOSE_CONN：连接关闭（双向）
- `0x04` - INIT：初始化配置（client → server，用于指定远程端口）

## 编译

### 前置要求

- Go 1.21 或更高版本
- OpenSSL 3.5+ with oqs-provider（用于 PQC mTLS 支持）
- CGO 编译器（Linux/Unix 系统通常已安装）

### Linux/Unix 编译

```bash
cd reverse-tunnel
export CGO_ENABLED=1
export LD_LIBRARY_PATH=/opt/openssl-oqs/lib:$LD_LIBRARY_PATH
go build -o bin/server ./cmd/server
go build -o bin/client ./cmd/client
```

### Windows 编译

**重要**：Windows 上编译需要 **OpenSSL 3.5+ with oqs-provider** 环境，否则无法编译成功。

**注意**：由于项目依赖 CGO 和 OpenSSL，无法在 Linux 系统上交叉编译 Windows 版本。必须在 Windows 系统上编译，或使用 WSL2。

详细说明请参考：
- [Windows编译说明.md](./Windows编译说明.md) - Windows 系统上编译的详细步骤
- [从Linux编译Windows版本说明.md](./从Linux编译Windows版本说明.md) - 为什么无法交叉编译及替代方案

## 使用方法

### 服务器端

```bash
./bin/server [选项]
```

**选项：**
- `--control-listen`：控制端口监听地址（默认 `:7000`）
- `--public-listen`：公开端口监听地址（可选，留空则由客户端指定）
- `--tls`：启用 PQC mTLS（可选）
- `--tls-cert`：服务器证书文件路径（默认 `/root/pq-certs/server.crt`）
- `--tls-key`：服务器私钥文件路径（默认 `/root/pq-certs/server.key`）
- `--tls-ca`：CA 证书文件路径（默认 `/root/pq-certs/ca.crt`）

**示例：**

```bash
# 使用默认控制端口，公开端口由客户端指定
./bin/server

# 指定公开端口
./bin/server --control-listen=:7000 --public-listen=:8080

# 启用 PQC mTLS
./bin/server --control-listen=:7000 --tls \
  --tls-cert=/root/pq-certs/server.crt \
  --tls-key=/root/pq-certs/server.key \
  --tls-ca=/root/pq-certs/ca.crt
```

### 客户端

```bash
./bin/client [选项]
```

**选项：**
- `--server`：服务器地址（必填，例如 `1.2.3.4:7000`）
- `--local`：本地服务地址（必填，例如 `127.0.0.1:80`）
- `--remote-port`：远程端口（可选，服务器要监听的端口，0 表示由服务器指定）
- `--tls`：启用 PQC mTLS（可选）
- `--tls-cert`：客户端证书文件路径（默认 `/root/pq-certs/client.crt`）
- `--tls-key`：客户端私钥文件路径（默认 `/root/pq-certs/client.key`）
- `--tls-ca`：CA 证书文件路径（默认 `/root/pq-certs/ca.crt`）
- `--tls-server-name`：服务器名称（TLS SNI，留空则使用服务器地址）

**示例：**

```bash
# 客户端指定远程端口（推荐）
./bin/client --server=1.2.3.4:7000 --local=127.0.0.1:80 --remote-port=8080

# 由服务器指定端口
./bin/client --server=1.2.3.4:7000 --local=127.0.0.1:80

# 启用 PQC mTLS
./bin/client --server=127.0.0.1:7000 --local=127.0.0.1:80 --remote-port=8080 --tls \
  --tls-cert=/root/pq-certs/client.crt \
  --tls-key=/root/pq-certs/client.key \
  --tls-ca=/root/pq-certs/ca.crt
```

### 快速开始示例

1. **启动服务器：**
   ```bash
   ./bin/server --control-listen=:7000
   ```

2. **启动客户端：**
   ```bash
   ./bin/client --server=127.0.0.1:7000 --local=127.0.0.1:80 --remote-port=8080
   ```

3. **测试访问：**
   ```bash
   curl http://127.0.0.1:8080
   ```

## 测试

运行所有测试：

```bash
go test -v ./internal/tunnel
```

## 项目结构

```
reverse-tunnel/
├── cmd/
│   ├── server/main.go          # 服务器入口
│   └── client/main.go          # 客户端入口
├── internal/
│   ├── proto/proto.go          # 协议编解码
│   ├── tunnel/
│   │   ├── server.go           # 服务器核心逻辑
│   │   ├── client.go           # 客户端核心逻辑
│   │   └── server_test.go      # 集成测试
│   └── pqctls/                 # PQC mTLS 实现
├── go.mod
└── README.md
```

## 技术细节

- 使用 goroutine 处理每条连接
- 使用 `sync.Map` 管理 connID 映射（线程安全）
- 使用 `sync.RWMutex` 保护控制连接
- 使用 `atomic` 生成唯一 connID
- 支持优雅退出（Ctrl+C）

## 限制与注意事项

1. **多客户端支持**：服务器支持多个客户端同时连接，每个客户端可以指定自己的远程端口
2. **端口要求**：确保防火墙允许控制端口和公开端口的访问
3. **动态端口**：当客户端指定远程端口时，服务器会为该客户端创建独立的监听器；如果端口已被占用，连接会失败
4. **全局端口路由**：如果服务器指定了全局公开端口，公开连接会路由到第一个可用的客户端（未来可改进为更智能的路由策略）
5. **PQC mTLS**：使用 PQC mTLS 时，确保证书文件存在且路径正确

## 故障排查

### 客户端无法连接服务器

- 检查服务器 IP 和端口是否正确
- 检查防火墙规则
- 检查服务器是否正在运行

### 外部连接无法访问

- 确认客户端已成功连接到服务器
- 检查服务器的公开端口是否可访问
- 查看服务器和客户端的日志输出

### 数据传输中断

- 检查网络连接稳定性
- 查看日志中的错误信息
- 客户端会自动重连，等待几秒后重试

## 相关文档

- [WINDOWS_BUILD.md](./WINDOWS_BUILD.md) - Windows 编译详细指南
- [PQC_ANALYSIS.md](./PQC_ANALYSIS.md) - PQC 配置分析报告

## 许可证

本项目为示例/教育用途，可根据需要修改和使用。

---

**注意**：生产环境使用前请确保启用 PQC mTLS 以保障安全性。
