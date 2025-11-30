# 配置文件说明

项目支持从 JSON 配置文件加载配置，替代命令行参数。

## 使用方法

### 服务器端

使用配置文件启动：

```bash
./bin/server --config=config/server.json
```

或者继续使用命令行参数：

```bash
./bin/server --control-listen=:7000 --public-listen=:8080
```

**注意**：如果指定了 `--config`，命令行参数将被忽略。

### 客户端

使用配置文件启动：

```bash
./bin/client --config=config/client.json
```

或者继续使用命令行参数：

```bash
./bin/client --server=127.0.0.1:7000 --local=127.0.0.1:80 --remote-port=8080
```

**注意**：如果指定了 `--config`，命令行参数将被忽略。

## 配置文件格式

### 服务器配置文件 (server.json)

```json
{
  "control_listen": ":7000",
  "public_listen": "",
  "tls": {
    "enabled": false,
    "cert": "/root/pq-certs/server.crt",
    "key": "/root/pq-certs/server.key",
    "ca": "/root/pq-certs/ca.crt"
  }
}
```

**字段说明**：
- `control_listen`：控制端口监听地址（默认 `:7000`）
- `public_listen`：公开端口监听地址（可选，留空则由客户端指定）
- `tls.enabled`：是否启用 PQC mTLS（默认 `false`）
- `tls.cert`：服务器证书文件路径
- `tls.key`：服务器私钥文件路径
- `tls.ca`：CA 证书文件路径（用于验证客户端证书）

### 客户端配置文件 (client.json)

```json
{
  "server": "127.0.0.1:7000",
  "local": "127.0.0.1:80",
  "remote_port": 8080,
  "tls": {
    "enabled": false,
    "cert": "/root/pq-certs/client.crt",
    "key": "/root/pq-certs/client.key",
    "ca": "/root/pq-certs/ca.crt",
    "server_name": ""
  }
}
```

**字段说明**：
- `server`：服务器地址（必填，例如 `1.2.3.4:7000`）
- `local`：本地服务地址（必填，例如 `127.0.0.1:80`）
- `remote_port`：远程端口（可选，0 表示由服务器指定）
- `tls.enabled`：是否启用 PQC mTLS（默认 `false`）
- `tls.cert`：客户端证书文件路径
- `tls.key`：客户端私钥文件路径
- `tls.ca`：CA 证书文件路径（用于验证服务器证书）
- `tls.server_name`：服务器名称（TLS SNI，留空则使用服务器地址）

## 示例配置文件

### 启用 PQC mTLS 的服务器配置

```json
{
  "control_listen": ":7000",
  "public_listen": "",
  "tls": {
    "enabled": true,
    "cert": "/root/pq-certs/server.crt",
    "key": "/root/pq-certs/server.key",
    "ca": "/root/pq-certs/ca.crt"
  }
}
```

### 启用 PQC mTLS 的客户端配置

```json
{
  "server": "192.168.1.100:7000",
  "local": "127.0.0.1:80",
  "remote_port": 8080,
  "tls": {
    "enabled": true,
    "cert": "/root/pq-certs/client.crt",
    "key": "/root/pq-certs/client.key",
    "ca": "/root/pq-certs/ca.crt",
    "server_name": "server.example.com"
  }
}
```

## 创建配置文件

1. 复制示例配置文件：

```bash
cp config/server.json.example config/server.json
cp config/client.json.example config/client.json
```

2. 根据实际需求修改配置文件中的值

3. 使用配置文件启动程序：

```bash
./bin/server --config=config/server.json
./bin/client --config=config/client.json
```

## 配置文件优先级

- 如果指定了 `--config`，配置文件中的值会覆盖所有命令行参数的默认值
- 如果未指定 `--config`，使用命令行参数（与之前的行为一致）

## 注意事项

1. 配置文件路径可以是相对路径或绝对路径
2. 配置文件必须是有效的 JSON 格式
3. 必填字段（如客户端的 `server` 和 `local`）必须在配置文件中提供
4. 如果配置文件不存在或格式错误，程序会退出并显示错误信息

---

**最后更新**：2025-11-30

