# Windows 编译说明

## 重要提示

**是的，如果 Windows 上没有 OpenSSL 3.5 和相关环境，项目将无法编译成功。**

## 编译要求

### 必需的环境

1. **Go 1.19+**
   - 下载地址：https://golang.org/dl/
   - 安装后确保 `go` 命令在 PATH 中

2. **CGO 编译器**
   - **MinGW-w64** 或 **MSYS2**（推荐）
   - 下载地址：https://www.mingw-w64.org/ 或 https://www.msys2.org/
   - 确保 `gcc` 命令可用

3. **OpenSSL 3.5+ with oqs-provider**
   - **这是最关键的依赖**
   - OpenSSL 3.5 必须编译时启用 oqs-provider
   - 需要包含头文件和库文件

### Windows 上的 OpenSSL 3.5 + oqs-provider 安装

#### 方案 1：使用预编译版本（如果可用）

1. 下载 OpenSSL 3.5 with oqs-provider 的 Windows 预编译版本
2. 解压到某个目录，例如 `C:\openssl-oqs\`
3. 设置环境变量：
   ```cmd
   set CGO_CFLAGS=-IC:\openssl-oqs\include
   set CGO_LDFLAGS=-LC:\openssl-oqs\lib -lssl -lcrypto
   set PATH=%PATH%;C:\openssl-oqs\bin
   ```

#### 方案 2：从源码编译（复杂但最可靠）

1. **安装 MSYS2**
   ```bash
   # 在 MSYS2 中安装依赖
   pacman -S base-devel mingw-w64-x86_64-toolchain
   pacman -S mingw-w64-x86_64-cmake
   ```

2. **编译 liboqs**
   ```bash
   git clone https://github.com/open-quantum-safe/liboqs.git
   cd liboqs
   mkdir build && cd build
   cmake -G"MinGW Makefiles" -DCMAKE_INSTALL_PREFIX=/c/openssl-oqs ..
   mingw32-make
   mingw32-make install
   ```

3. **编译 OpenSSL with oqs-provider**
   ```bash
   git clone https://github.com/open-quantum-safe/openssl.git
   cd openssl
   git checkout OQS-OpenSSL_1_3_0-stable
   ./Configure mingw64 --prefix=/c/openssl-oqs
   make
   make install
   ```

4. **设置环境变量**
   ```cmd
   set CGO_CFLAGS=-IC:\openssl-oqs\include
   set CGO_LDFLAGS=-LC:\openssl-oqs\lib -lssl -lcrypto
   set PATH=%PATH%;C:\openssl-oqs\bin
   ```

## 编译步骤

### 1. 设置环境变量

```cmd
# 设置 CGO 标志（根据实际安装路径调整）
set CGO_ENABLED=1
set CGO_CFLAGS=-IC:\openssl-oqs\include
set CGO_LDFLAGS=-LC:\openssl-oqs\lib -lssl -lcrypto

# 确保 OpenSSL DLL 在 PATH 中
set PATH=%PATH%;C:\openssl-oqs\bin
```

### 2. 编译服务器

```cmd
cd reverse-tunnel
go build -o bin\server.exe .\cmd\server
```

### 3. 编译客户端

```cmd
go build -o bin\client.exe .\cmd\client
```

## 如果没有 OpenSSL 3.5 环境会发生什么？

### 编译错误示例

```bash
# 尝试禁用 CGO 编译
set CGO_ENABLED=0
go build -o bin\server.exe .\cmd\server

# 错误输出：
# internal/tunnel/server.go:78:33: undefined: pqctls.NewPQCListenerOpenSSL
# internal/tunnel/client.go:114:25: undefined: pqctls.NewPQCDialerOpenSSL
```

**原因**：
- `pqctls.NewPQCListenerOpenSSL` 和 `pqctls.NewPQCDialerOpenSSL` 函数定义在 `internal/pqctls/pqc_tls_openssl.go` 中
- 该文件有 `// +build cgo` 构建标签，只有在启用 CGO 时才会编译
- 即使禁用 CGO，`internal/tunnel/server.go` 和 `client.go` 仍然会尝试调用这些函数
- 因此编译会失败

### 为什么不能禁用 CGO？

1. **代码依赖**：`internal/tunnel/server.go` 和 `client.go` 直接调用 `pqctls.NewPQCListenerOpenSSL` 和 `pqctls.NewPQCDialerOpenSSL`
2. **没有 fallback**：虽然 `internal/pqctls/pqc_tls.go` 提供了基于 Go 标准库的实现，但主代码没有使用它
3. **PQC 算法支持**：Go 标准库的 `crypto/tls` 不支持 PQC 算法（ML-KEM, ML-DSA），必须使用 OpenSSL

## 解决方案

### 方案 1：安装 OpenSSL 3.5 + oqs-provider（推荐）

按照上面的安装步骤，在 Windows 上安装完整的 OpenSSL 3.5 + oqs-provider 环境。

### 方案 2：修改代码支持条件编译（需要代码改动）

如果确实需要在没有 OpenSSL 的环境下编译（但无法使用 PQC 功能），可以修改代码：

1. **修改 `internal/tunnel/server.go`**：
   ```go
   // 添加构建标签支持
   // +build !cgo
   
   // 或者使用条件编译
   if useTLS {
       // 尝试使用 OpenSSL 实现
       // 如果失败，回退到标准库（不支持 PQC）
   }
   ```

2. **修改 `internal/tunnel/client.go`**：类似处理

**注意**：这种方案会导致：
- 无法使用 PQC 算法
- 无法使用 ML-DSA-65 证书（Go 标准库不支持）
- 只能使用传统 TLS 算法

### 方案 3：使用 Docker 或 WSL2（推荐用于开发）

如果 Windows 上安装 OpenSSL 3.5 困难，可以使用：

1. **WSL2（Windows Subsystem for Linux）**
   - 在 WSL2 中安装 Linux 版本的 OpenSSL 3.5
   - 在 WSL2 中编译和运行

2. **Docker**
   - 使用包含 OpenSSL 3.5 的 Docker 镜像
   - 在容器中编译和运行

## 验证编译环境

### 检查 CGO 是否可用

```cmd
go env CGO_ENABLED
# 应该输出：1
```

### 检查编译器是否可用

```cmd
gcc --version
# 应该输出 GCC 版本信息
```

### 检查 OpenSSL 是否可用

```cmd
# 如果 OpenSSL 在 PATH 中
openssl version
# 应该输出：OpenSSL 3.5.x

# 检查 oqs-provider
openssl list -providers
# 应该看到 oqsprovider
```

## 总结

**问题**：Windows 上没有 OpenSSL 3.5 + oqs-provider 环境时，项目无法编译成功。

**原因**：
1. 代码直接依赖 CGO 和 OpenSSL 函数
2. 没有提供非 CGO 的 fallback 实现
3. Go 标准库不支持 PQC 算法

**解决方案**：
1. **推荐**：在 Windows 上安装 OpenSSL 3.5 + oqs-provider
2. **备选**：使用 WSL2 或 Docker 进行编译
3. **不推荐**：修改代码禁用 PQC 功能（失去项目核心价值）

---

**最后更新**：2025-11-30

