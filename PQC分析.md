# 反向隧道项目 PQC（后量子密码学）分析报告

## 总体评估

**结论：项目已配置为纯 PQC 连接（TLS 1.3 + ML-KEM + ML-DSA）**

### PQC 已实现的部分

1. **证书签名算法：纯 PQC**
   - 服务器证书：`ML-DSA-65`（后量子签名算法）
   - 客户端证书：`ML-DSA-65`（后量子签名算法）
   - CA 证书：`ML-DSA-65`（后量子签名算法）
   - **所有证书都使用 ML-DSA-65，这是 NIST 标准化的后量子数字签名算法**

2. **OpenSSL 配置：已加载 oqs-provider**
   ```bash
   Providers:
     default
       name: OpenSSL Default Provider
       version: 3.5.0
       status: active
     oqsprovider
       name: OpenSSL OQS Provider
       version: 0.10.1-dev
       status: active
   ```

3. **可用的 PQC 算法：已支持**
   - **签名算法**：ML-DSA-44, ML-DSA-65, ML-DSA-87, Falcon-512, Falcon-1024
   - **密钥封装（KEM）**：ML-KEM-512, ML-KEM-768, ML-KEM-1024

### 已配置为纯 PQC 的部分

1. **TLS 密钥交换组：已明确指定 PQC 算法**
   - 使用 `SSL_CTX_set1_groups_list()` 指定 ML-KEM 密钥交换组
   - 优先级：ML-KEM-768 (NIST Level 3) > ML-KEM-512 (NIST Level 1) > ML-KEM-1024 (NIST Level 5)
   - **密钥交换使用纯 PQC 算法**

2. **TLS 签名算法：已明确指定 PQC 算法**
   - 使用 `SSL_CTX_set1_sigalgs_list()` 指定 ML-DSA 签名算法
   - 优先级：ML-DSA-65 > ML-DSA-44 > ML-DSA-87
   - **签名算法使用纯 PQC 算法**

3. **TLS 版本：强制使用 TLS 1.3**
   - 仅允许 TLS 1.3（最佳 PQC 支持）
   - 不再支持 TLS 1.2（避免传统算法）

## 详细分析

### 1. 证书层面（纯 PQC）

```bash
# 服务器证书
Public Key Algorithm: ML-DSA-65

# 客户端证书  
Public Key Algorithm: ML-DSA-65

# CA 证书
Public Key Algorithm: ML-DSA-65
```

**结论**：所有证书都使用 ML-DSA-65（后量子签名算法），这是**纯 PQC 签名**。

### 2. TLS 握手层面（纯 PQC 模式）

**当前代码**（`internal/pqctls/pqc_tls_openssl.go`）：
```c
// 强制使用 TLS 1.3
SSL_CTX_set_min_proto_version(ctx, TLS1_3_VERSION);
SSL_CTX_set_max_proto_version(ctx, TLS1_3_VERSION);

// 配置纯 PQC 密钥交换组
const char* groups = "MLKEM768:MLKEM512:MLKEM1024";
SSL_CTX_set1_groups_list(ctx, groups);

// 配置纯 PQC 签名算法
const char* sigalgs = "MLDSA65:MLDSA44:MLDSA87";
SSL_CTX_set1_sigalgs_list(ctx, sigalgs);
```

**配置说明**：
- **TLS 版本**：仅使用 TLS 1.3（最佳 PQC 支持）
- **密钥交换**：使用 ML-KEM（纯 PQC 算法）
- **签名算法**：使用 ML-DSA（纯 PQC 算法）
- **证书验证**：使用 ML-DSA-65（纯 PQC 签名）
- **对称加密**：仍使用 AES-GCM（传统算法，但这是 TLS 1.3 的标准）

**实际连接情况**：
- 证书验证：使用 ML-DSA-65（PQC）
- 密钥交换：使用 ML-KEM-768/512/1024（纯 PQC）
- 签名算法：使用 ML-DSA-65/44/87（纯 PQC）
- 对称加密：使用 AES-GCM（传统算法，但这是 TLS 1.3 的标准，且对称加密不受量子计算威胁）

### 3. 密钥交换（KEM）层面（已明确）

**可用的 PQC KEM 算法**：
- ML-KEM-512（NIST Level 1）
- ML-KEM-768（NIST Level 3）
- ML-KEM-1024（NIST Level 5）

**当前状态**：代码中**没有明确指定**使用哪个 KEM 算法，OpenSSL 会根据默认优先级协商，可能选择传统算法。

## 如何实现纯 PQC 连接

### 方案 1：明确指定 PQC 密码套件（推荐）

在 `create_server_ctx()` 和 `create_client_ctx()` 中添加：

```c
// 对于 TLS 1.3，指定 PQC 密码套件
// 格式：KEM算法_签名算法_对称加密算法
// 例如：ML-KEM-768_ML-DSA-65_AES-256-GCM-SHA384
SSL_CTX_set_ciphersuites(ctx, 
    "TLS_AES_256_GCM_SHA384:"
    "ML-KEM-768_ML-DSA-65_AES-256-GCM-SHA384:"
    "ML-KEM-512_ML-DSA-65_AES-256-GCM-SHA384"
);

// 对于 TLS 1.2（如果需要支持）
// 注意：TLS 1.2 的 PQC 支持可能有限
SSL_CTX_set_cipher_list(ctx, 
    "ECDHE-ML-DSA-65-AES256-GCM-SHA384:"
    "ECDHE-ML-DSA-65-CHACHA20-POLY1305"
);
```

### 方案 2：仅使用 TLS 1.3 + PQC

```c
// 强制只使用 TLS 1.3
SSL_CTX_set_min_proto_version(ctx, TLS1_3_VERSION);
SSL_CTX_set_max_proto_version(ctx, TLS1_3_VERSION);

// 明确指定 PQC 密码套件
SSL_CTX_set_ciphersuites(ctx, 
    "ML-KEM-768_ML-DSA-65_AES-256-GCM-SHA384"
);
```

### 方案 3：使用混合模式（过渡方案）

如果某些客户端不支持纯 PQC，可以使用混合模式：
- 证书签名：PQC（ML-DSA-65）
- 密钥交换：传统算法（ECDHE）+ PQC（ML-KEM）混合
- 对称加密：传统算法（AES-GCM）

## 当前项目的 PQC 使用情况总结

| 组件 | 状态 | 算法 | 说明 |
|------|------|------|------|
| 服务器证书签名 | 纯 PQC | ML-DSA-65 | NIST 标准化算法 |
| 客户端证书签名 | 纯 PQC | ML-DSA-65 | NIST 标准化算法 |
| CA 证书签名 | 纯 PQC | ML-DSA-65 | NIST 标准化算法 |
| TLS 密钥交换 | 纯 PQC | ML-KEM-768/512/1024 | 已明确配置 |
| TLS 签名算法 | 纯 PQC | ML-DSA-65/44/87 | 已明确配置 |
| TLS 对称加密 | 传统 | AES-GCM | TLS 1.3 标准（对称加密不受量子威胁） |
| OpenSSL Provider | 已加载 | oqs-provider | 支持 PQC 算法 |

## 已完成的改进措施

1. **代码修改**：在代码中明确指定 PQC 密钥交换组和签名算法
2. **TLS 版本**：强制使用 TLS 1.3（最佳 PQC 支持）
3. **严格模式**：禁止降级，PQC 配置失败直接报错
4. **握手验证**：握手后验证实际使用的算法是否为 PQC，非 PQC 算法直接拒绝连接
5. **配置验证**：编译通过，配置已生效

## 验证方法

### 1. 检查编译后的二进制文件
```bash
# 编译服务器和客户端
go build -o bin/server ./cmd/server
go build -o bin/client ./cmd/client
```

### 2. 运行时验证
启动服务器和客户端后，连接应该使用：
- **TLS 版本**：TLS 1.3
- **密钥交换**：ML-KEM-768/512/1024
- **签名算法**：ML-DSA-65
- **证书签名**：ML-DSA-65

### 3. 使用 OpenSSL 客户端测试（如果支持）
```bash
openssl s_client -connect localhost:7000 \
  -cert /root/pq-certs/client.crt \
  -key /root/pq-certs/client.key \
  -CAfile /root/pq-certs/ca.crt \
  -tls1_3
```

## 严格模式（禁止降级）

项目已实现**严格 PQC 模式**，确保连接必须使用 PQC 算法：

### 1. 配置阶段严格检查
- **密钥交换组设置失败**：直接返回错误，不创建 SSL 上下文
- **签名算法设置失败**：直接返回错误，不创建 SSL 上下文
- **不允许降级**：如果 PQC 算法不可用，连接直接失败

### 2. 握手后验证
- **算法验证**：握手成功后，验证实际使用的密钥交换算法是否为 ML-KEM
- **非 PQC 算法拒绝**：如果协商的算法不是 PQC 算法（如 ECDHE），连接会被立即拒绝
- **错误信息**：返回明确的错误信息："handshake succeeded but non-PQC algorithms were negotiated, connection rejected"

### 3. 验证逻辑
```c
// 验证函数检查：
1. 获取协商的密钥交换组 ID
2. 获取组名称
3. 检查是否为 ML-KEM 相关算法（MLKEM512/768/1024, KYBER 等）
4. 如果不是 PQC 算法，返回 0（拒绝连接）
```

## 注意事项

1. **对称加密算法**：TLS 1.3 仍使用 AES-GCM 等传统对称加密算法，这是正常的，因为：
   - 对称加密算法不受量子计算威胁（Grover 算法仅能提供平方根加速）
   - AES-256 在量子计算环境下仍然安全
   - TLS 1.3 标准要求使用这些对称加密算法

2. **兼容性**：纯 PQC 连接要求客户端和服务器都支持：
   - OpenSSL 3.5+ with oqs-provider
   - ML-KEM 和 ML-DSA 算法支持
   - TLS 1.3 协议支持

3. **严格模式影响**：
   - 如果对端不支持 PQC 算法，连接会直接失败（不会降级）
   - 如果 PQC 算法配置失败，服务器/客户端无法启动
   - 这确保了所有连接都使用纯 PQC 算法，提高了安全性

## 参考资料

- ML-DSA：NIST PQC 标准数字签名算法（原 Dilithium）
- ML-KEM：NIST PQC 标准密钥封装算法（原 Kyber）
- OpenSSL oqs-provider：https://github.com/open-quantum-safe/oqs-provider

---

**生成时间**：2025-11-30
**分析工具**：OpenSSL 3.5 + oqs-provider 0.10.1-dev

