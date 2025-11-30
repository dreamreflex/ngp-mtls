# 从 Linux 系统编译 Windows 版本说明

## 重要提示

**在 Linux 系统上无法直接交叉编译 Windows 版本**，因为项目依赖 CGO 和 OpenSSL 3.5 + oqs-provider。

## 为什么无法交叉编译？

### 1. CGO 依赖

项目使用了 CGO 来调用 OpenSSL 3.5 的 C API（`internal/pqctls/pqc_tls_openssl.go`）。CGO 交叉编译有以下限制：

- 需要目标平台的 C 编译器（Windows 需要 MinGW-w64）
- 需要目标平台的 OpenSSL 库文件（.dll 和 .lib）
- Linux 上的 gcc 无法编译 Windows 可执行文件

### 2. 测试结果

尝试在 Linux 上交叉编译会失败：

```bash
GOOS=windows GOARCH=amd64 CGO_ENABLED=1 go build -o bin/server.exe ./cmd/server
# 错误：gcc: error: unrecognized command-line option '-mthreads'
```

这是因为 Linux 的 gcc 不支持 Windows 特定的编译选项。

## 解决方案

### 方案 1：在 Windows 系统上编译（推荐）

这是最直接的方法，详细步骤请参考 [Windows编译说明.md](./Windows编译说明.md)。

**要求：**
- Windows 系统
- Go 1.21+
- MinGW-w64 或 MSYS2
- OpenSSL 3.5+ with oqs-provider（Windows 版本）

### 方案 2：使用 Docker 容器

在 Linux 上使用包含 Windows 编译环境的 Docker 容器。

#### 2.1 使用 x86_64-w64-mingw32 交叉编译工具链

```bash
# 安装交叉编译工具链
sudo apt-get update
sudo apt-get install -y gcc-mingw-w64-x86-64

# 设置环境变量
export CC=x86_64-w64-mingw32-gcc
export CXX=x86_64-w64-mingw32-g++
export CGO_ENABLED=1
export GOOS=windows
export GOARCH=amd64

# 但是仍然需要 Windows 版本的 OpenSSL 库
# 这需要从 Windows 系统或预编译包中获取
```

**问题**：仍然需要 Windows 版本的 OpenSSL 库文件（.dll 和 .lib），这些文件无法在 Linux 上直接获取。

#### 2.2 使用 Wine + Windows 工具链（复杂，不推荐）

理论上可以使用 Wine 运行 Windows 版本的编译工具，但配置复杂且不稳定。

### 方案 3：使用 WSL2（如果目标是在 Windows 上运行）

如果最终目标是在 Windows 上运行程序，可以使用 WSL2：

1. 在 Windows 上安装 WSL2
2. 在 WSL2 中安装 Linux 版本的 OpenSSL 3.5 + oqs-provider
3. 在 WSL2 中编译 Linux 版本
4. 在 WSL2 中运行（性能接近原生）

### 方案 4：修改代码支持条件编译（不推荐）

如果确实需要在没有 OpenSSL 的环境下编译（但会失去 PQC 功能），可以：

1. 修改代码，添加构建标签支持非 CGO 编译
2. 提供基于 Go 标准库的 fallback（不支持 PQC）
3. 使用条件编译，在非 CGO 模式下禁用 TLS 功能

**注意**：这会失去项目的核心价值（PQC mTLS 支持）。

## 推荐的编译流程

### 在 Windows 上编译（最佳方案）

1. **安装 MSYS2**
   ```bash
   # 下载并安装 MSYS2
   # https://www.msys2.org/
   ```

2. **在 MSYS2 中安装工具链**
   ```bash
   pacman -S base-devel mingw-w64-x86_64-toolchain
   pacman -S mingw-w64-x86_64-cmake
   ```

3. **编译 OpenSSL 3.5 + oqs-provider**
   - 参考 [Windows编译说明.md](./Windows编译说明.md) 中的详细步骤

4. **设置环境变量并编译**
   ```cmd
   set CGO_ENABLED=1
   set CGO_CFLAGS=-IC:\openssl-oqs\include
   set CGO_LDFLAGS=-LC:\openssl-oqs\lib -lssl -lcrypto
   set PATH=%PATH%;C:\openssl-oqs\bin
   
   go build -o bin\server.exe .\cmd\server
   go build -o bin\client.exe .\cmd\client
   ```

## 总结

| 方案 | 可行性 | 难度 | 推荐度 |
|------|--------|------|--------|
| Windows 系统上编译 | 高 | 中等 | ⭐⭐⭐⭐⭐ |
| Docker 容器 | 低 | 高 | ⭐⭐ |
| WSL2 | 中 | 低 | ⭐⭐⭐⭐ |
| 修改代码禁用 CGO | 高 | 高 | ⭐（失去 PQC 功能） |

**结论**：最可靠的方法是在 Windows 系统上直接编译，或者使用 WSL2 在 Windows 上运行 Linux 版本。

---

**最后更新**：2025-11-30

