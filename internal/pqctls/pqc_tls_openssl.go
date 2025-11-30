// +build cgo

package pqctls

/*
#cgo CFLAGS: -I/opt/openssl-oqs/include
#cgo LDFLAGS: -L/opt/openssl-oqs/lib -lssl -lcrypto -ldl -lpthread
#cgo LDFLAGS: -Wl,-rpath,/opt/openssl-oqs/lib

#include <openssl/ssl.h>
#include <openssl/err.h>
#include <openssl/x509.h>
#include <openssl/pem.h>
#include <openssl/conf.h>
#include <openssl/tls1.h>
#include <openssl/provider.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include <sys/socket.h>
#include <netinet/in.h>
#include <arpa/inet.h>

#define SSL_ERROR_NONE 0
#define SSL_ERROR_SSL 1
#define SSL_ERROR_WANT_READ 2
#define SSL_ERROR_WANT_WRITE 3
#define SSL_ERROR_WANT_X509_LOOKUP 4
#define SSL_ERROR_SYSCALL 5
#define SSL_ERROR_ZERO_RETURN 6
#define SSL_ERROR_WANT_CONNECT 7
#define SSL_ERROR_WANT_ACCEPT 8

// 验证握手后使用的算法是否为 PQC 算法
// 返回 1 表示是 PQC 算法，0 表示不是（需要拒绝连接）
static int verify_pqc_algorithms(SSL* ssl) {
    // 检查密钥交换组是否为 ML-KEM
    int group_id = SSL_get_negotiated_group(ssl);
    if (group_id <= 0) {
        return 0; // 无法获取组信息，拒绝
    }
    
    // 获取组名（需要传入 SSL 对象）
    const char* group_name = SSL_get0_group_name(ssl);
    if (group_name == NULL) {
        return 0; // 无法获取组名，拒绝
    }
    
    // 检查是否为 ML-KEM 组（MLKEM512, MLKEM768, MLKEM1024）
    // 也检查可能的变体名称（KYBER 是 ML-KEM 的旧名称）
    if (strstr(group_name, "MLKEM") == NULL && 
        strstr(group_name, "ML-KEM") == NULL &&
        strstr(group_name, "KYBER") == NULL &&
        strstr(group_name, "mlkem") == NULL &&
        strstr(group_name, "ml-kem") == NULL) {
        return 0; // 不是 PQC 密钥交换算法，拒绝
    }
    
    // 检查签名算法（TLS 1.3 中通过证书验证）
    // 注意：在 TLS 1.3 中，签名算法主要用于证书验证
    // 我们已经通过证书使用了 ML-DSA-65，这里主要验证密钥交换
    
    return 1; // 验证通过
}

static void init_openssl() {
    OPENSSL_init_ssl(0, NULL);
    OPENSSL_init_crypto(0, NULL);
    
    // 加载 OpenSSL 配置文件（包含 oqs-provider）
    // 注意：OPENSSL_config 在 OpenSSL 3.x 中已废弃，使用 CONF_modules_load_file
    const char* conf_file = "/opt/openssl-oqs/ssl/openssl-oqs.cnf";
    CONF_modules_load_file(conf_file, NULL, 0);
}

static SSL_CTX* create_server_ctx(const char* cert_file, const char* key_file, const char* ca_file) {
    SSL_CTX* ctx = SSL_CTX_new(TLS_server_method());
    if (!ctx) {
        return NULL;
    }
    
    // 强制使用 TLS 1.3（对 PQC 支持最好）
    SSL_CTX_set_min_proto_version(ctx, TLS1_3_VERSION);
    SSL_CTX_set_max_proto_version(ctx, TLS1_3_VERSION);
    
    // 配置纯 PQC 密钥交换组（TLS 1.3）
    // 优先级：ML-KEM-768 (NIST Level 3) > ML-KEM-512 (NIST Level 1) > ML-KEM-1024 (NIST Level 5)
    // 严格模式：如果设置失败，直接返回错误，禁止降级
    const char* groups = "MLKEM768:MLKEM512:MLKEM1024";
    if (SSL_CTX_set1_groups_list(ctx, groups) <= 0) {
        ERR_print_errors_fp(stderr);
        SSL_CTX_free(ctx);
        return NULL;
    }
    
    // 配置纯 PQC 签名算法（TLS 1.3）
    // 使用 ML-DSA-65（与证书匹配）
    // 严格模式：如果设置失败，直接返回错误，禁止降级
    const char* sigalgs = "MLDSA65:MLDSA44:MLDSA87";
    if (SSL_CTX_set1_sigalgs_list(ctx, sigalgs) <= 0) {
        ERR_print_errors_fp(stderr);
        SSL_CTX_free(ctx);
        return NULL;
    }

    // 加载服务器证书和私钥
    if (SSL_CTX_use_certificate_file(ctx, cert_file, SSL_FILETYPE_PEM) <= 0) {
        ERR_print_errors_fp(stderr);
        SSL_CTX_free(ctx);
        return NULL;
    }

    if (SSL_CTX_use_PrivateKey_file(ctx, key_file, SSL_FILETYPE_PEM) <= 0) {
        ERR_print_errors_fp(stderr);
        SSL_CTX_free(ctx);
        return NULL;
    }

    // 验证私钥和证书匹配
    if (!SSL_CTX_check_private_key(ctx)) {
        ERR_print_errors_fp(stderr);
        SSL_CTX_free(ctx);
        return NULL;
    }

    // 加载 CA 证书用于验证客户端证书
    if (ca_file && SSL_CTX_load_verify_locations(ctx, ca_file, NULL) <= 0) {
        ERR_print_errors_fp(stderr);
        SSL_CTX_free(ctx);
        return NULL;
    }

    // 要求客户端证书（mTLS）
    SSL_CTX_set_verify(ctx, SSL_VERIFY_PEER | SSL_VERIFY_FAIL_IF_NO_PEER_CERT, NULL);
    
    // 设置验证深度
    SSL_CTX_set_verify_depth(ctx, 1);

    return ctx;
}

static SSL_CTX* create_client_ctx(const char* cert_file, const char* key_file, const char* ca_file) {
    SSL_CTX* ctx = SSL_CTX_new(TLS_client_method());
    if (!ctx) {
        return NULL;
    }
    
    // 强制使用 TLS 1.3（对 PQC 支持最好）
    SSL_CTX_set_min_proto_version(ctx, TLS1_3_VERSION);
    SSL_CTX_set_max_proto_version(ctx, TLS1_3_VERSION);
    
    // 配置纯 PQC 密钥交换组（TLS 1.3）
    // 优先级：ML-KEM-768 (NIST Level 3) > ML-KEM-512 (NIST Level 1) > ML-KEM-1024 (NIST Level 5)
    // 严格模式：如果设置失败，直接返回错误，禁止降级
    const char* groups = "MLKEM768:MLKEM512:MLKEM1024";
    if (SSL_CTX_set1_groups_list(ctx, groups) <= 0) {
        ERR_print_errors_fp(stderr);
        SSL_CTX_free(ctx);
        return NULL;
    }
    
    // 配置纯 PQC 签名算法（TLS 1.3）
    // 使用 ML-DSA-65（与证书匹配）
    // 严格模式：如果设置失败，直接返回错误，禁止降级
    const char* sigalgs = "MLDSA65:MLDSA44:MLDSA87";
    if (SSL_CTX_set1_sigalgs_list(ctx, sigalgs) <= 0) {
        ERR_print_errors_fp(stderr);
        SSL_CTX_free(ctx);
        return NULL;
    }

    // 加载客户端证书和私钥
    if (cert_file && SSL_CTX_use_certificate_file(ctx, cert_file, SSL_FILETYPE_PEM) <= 0) {
        ERR_print_errors_fp(stderr);
        SSL_CTX_free(ctx);
        return NULL;
    }

    if (key_file && SSL_CTX_use_PrivateKey_file(ctx, key_file, SSL_FILETYPE_PEM) <= 0) {
        ERR_print_errors_fp(stderr);
        SSL_CTX_free(ctx);
        return NULL;
    }

    // 验证私钥和证书匹配
    if (cert_file && key_file && !SSL_CTX_check_private_key(ctx)) {
        ERR_print_errors_fp(stderr);
        SSL_CTX_free(ctx);
        return NULL;
    }

    // 加载 CA 证书用于验证服务器证书
    if (ca_file && SSL_CTX_load_verify_locations(ctx, ca_file, NULL) <= 0) {
        ERR_print_errors_fp(stderr);
        SSL_CTX_free(ctx);
        return NULL;
    }

    // 验证服务器证书
    SSL_CTX_set_verify(ctx, SSL_VERIFY_PEER, NULL);
    
    // 设置验证深度
    SSL_CTX_set_verify_depth(ctx, 1);

    return ctx;
}
*/
import "C"

import (
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"time"
	"unsafe"
)

func init() {
	C.init_openssl()
}

// PQCConn 表示一个 PQC TLS 连接（使用 OpenSSL）
// 注意：OpenSSL 的 SSL 对象不是线程安全的，需要互斥锁保护
type PQCConn struct {
	conn net.Conn
	ssl  *C.SSL
	ctx  *C.SSL_CTX
	mu   sync.Mutex // 保护 SSL 对象的并发访问
}

// Read 从 TLS 连接读取数据
func (c *PQCConn) Read(b []byte) (n int, err error) {
	if c.ssl == nil {
		return 0, errors.New("SSL connection not established")
	}

	if len(b) == 0 {
		return 0, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	n = int(C.SSL_read(c.ssl, unsafe.Pointer(&b[0]), C.int(len(b))))
	if n <= 0 {
		errCode := C.SSL_get_error(c.ssl, C.int(n))
		if errCode == C.SSL_ERROR_ZERO_RETURN {
			return 0, io.EOF
		}
		if errCode == C.SSL_ERROR_WANT_READ || errCode == C.SSL_ERROR_WANT_WRITE {
			// 需要重试
			return 0, nil
		}
		return 0, fmt.Errorf("SSL read error: %d", errCode)
	}
	return n, nil
}

// Write 向 TLS 连接写入数据
func (c *PQCConn) Write(b []byte) (n int, err error) {
	if c.ssl == nil {
		return 0, errors.New("SSL connection not established")
	}

	if len(b) == 0 {
		return 0, nil
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	n = int(C.SSL_write(c.ssl, unsafe.Pointer(&b[0]), C.int(len(b))))
	if n <= 0 {
		errCode := C.SSL_get_error(c.ssl, C.int(n))
		if errCode == C.SSL_ERROR_ZERO_RETURN {
			return 0, io.EOF
		}
		if errCode == C.SSL_ERROR_WANT_READ || errCode == C.SSL_ERROR_WANT_WRITE {
			// 需要重试，但返回 0 表示没有写入
			return 0, nil
		}
		return 0, fmt.Errorf("SSL write error: %d", errCode)
	}
	return n, nil
}

// Close 关闭 TLS 连接
func (c *PQCConn) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.ssl != nil {
		C.SSL_shutdown(c.ssl)
		C.SSL_free(c.ssl)
		c.ssl = nil
	}
	if c.conn != nil {
		return c.conn.Close()
	}
	return nil
}

// LocalAddr 返回本地地址
func (c *PQCConn) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

// RemoteAddr 返回远程地址
func (c *PQCConn) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

// SetDeadline 设置读写截止时间
func (c *PQCConn) SetDeadline(t time.Time) error {
	return c.conn.SetDeadline(t)
}

// SetReadDeadline 设置读截止时间
func (c *PQCConn) SetReadDeadline(t time.Time) error {
	return c.conn.SetReadDeadline(t)
}

// SetWriteDeadline 设置写截止时间
func (c *PQCConn) SetWriteDeadline(t time.Time) error {
	return c.conn.SetWriteDeadline(t)
}

// PQCListener 表示一个 PQC TLS 监听器（使用 OpenSSL）
type PQCListener struct {
	listener net.Listener
	ctx      *C.SSL_CTX
}

// Accept 接受一个新的 TLS 连接
func (l *PQCListener) Accept() (net.Conn, error) {
	conn, err := l.listener.Accept()
	if err != nil {
		return nil, err
	}

	tcpConn := conn.(*net.TCPConn)
	// 使用 syscall 获取底层文件描述符
	rawConn, err := tcpConn.SyscallConn()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to get raw connection: %v", err)
	}

	var fd int
	err = rawConn.Control(func(f uintptr) {
		fd = int(f)
	})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to get file descriptor: %v", err)
	}

	ssl := C.SSL_new(l.ctx)
	if ssl == nil {
		conn.Close()
		return nil, errors.New("failed to create SSL object")
	}

	if C.SSL_set_fd(ssl, C.int(fd)) <= 0 {
		C.SSL_free(ssl)
		conn.Close()
		return nil, errors.New("failed to set SSL file descriptor")
	}

	// SSL_accept 握手（可能需要多次调用）
	for {
		ret := C.SSL_accept(ssl)
		if ret > 0 {
			// 握手成功，验证是否使用了 PQC 算法
			if C.verify_pqc_algorithms(ssl) == 0 {
				// 握手成功但未使用 PQC 算法，拒绝连接
				C.SSL_free(ssl)
				conn.Close()
				return nil, fmt.Errorf("handshake succeeded but non-PQC algorithms were negotiated, connection rejected")
			}
			// PQC 算法验证通过
			break
		}
		errCode := C.SSL_get_error(ssl, ret)
		if errCode == C.SSL_ERROR_WANT_READ || errCode == C.SSL_ERROR_WANT_WRITE {
			// 需要更多 I/O，继续重试
			continue
		}
		// 其他错误
		var errBuf [512]C.char
		// 获取所有错误队列中的错误
		var errNum C.ulong
		for {
			errNum = C.ERR_get_error()
			if errNum == 0 {
				break
			}
			C.ERR_error_string_n(errNum, &errBuf[0], 512)
		}
		errMsg := C.GoString(&errBuf[0])
		if errMsg == "" {
			errMsg = "unknown error"
		}
		
		C.SSL_free(ssl)
		conn.Close()
		return nil, fmt.Errorf("SSL accept failed: error code %d, %s", errCode, errMsg)
	}

	return &PQCConn{
		conn: conn,
		ssl:  ssl,
		ctx:  l.ctx,
	}, nil
}

// Close 关闭监听器
func (l *PQCListener) Close() error {
	if l.ctx != nil {
		C.SSL_CTX_free(l.ctx)
		l.ctx = nil
	}
	return l.listener.Close()
}

// Addr 返回监听地址
func (l *PQCListener) Addr() net.Addr {
	return l.listener.Addr()
}

// PQCDialer 用于创建 PQC TLS 客户端连接（使用 OpenSSL）
type PQCDialer struct {
	ctx *C.SSL_CTX
}

// Dial 连接到服务器并建立 TLS 连接
func (d *PQCDialer) Dial(network, address string) (net.Conn, error) {
	conn, err := net.Dial(network, address)
	if err != nil {
		return nil, err
	}

	tcpConn := conn.(*net.TCPConn)
	// 使用 syscall 获取底层文件描述符
	rawConn, err := tcpConn.SyscallConn()
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to get raw connection: %v", err)
	}

	var fd int
	err = rawConn.Control(func(f uintptr) {
		fd = int(f)
	})
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to get file descriptor: %v", err)
	}

	ssl := C.SSL_new(d.ctx)
	if ssl == nil {
		conn.Close()
		return nil, errors.New("failed to create SSL object")
	}

	if C.SSL_set_fd(ssl, C.int(fd)) <= 0 {
		C.SSL_free(ssl)
		conn.Close()
		return nil, errors.New("failed to set SSL file descriptor")
	}

	// SSL_connect 握手（可能需要多次调用）
	for {
		ret := C.SSL_connect(ssl)
		if ret > 0 {
			// 握手成功，验证是否使用了 PQC 算法
			if C.verify_pqc_algorithms(ssl) == 0 {
				// 握手成功但未使用 PQC 算法，拒绝连接
				C.SSL_free(ssl)
				conn.Close()
				return nil, fmt.Errorf("handshake succeeded but non-PQC algorithms were negotiated, connection rejected")
			}
			// PQC 算法验证通过
			break
		}
		errCode := C.SSL_get_error(ssl, ret)
		if errCode == C.SSL_ERROR_WANT_READ || errCode == C.SSL_ERROR_WANT_WRITE {
			// 需要更多 I/O，继续重试
			continue
		}
		// 其他错误
		var errBuf [512]C.char
		// 获取所有错误队列中的错误
		var errNum C.ulong
		for {
			errNum = C.ERR_get_error()
			if errNum == 0 {
				break
			}
			C.ERR_error_string_n(errNum, &errBuf[0], 512)
		}
		errMsg := C.GoString(&errBuf[0])
		if errMsg == "" {
			errMsg = "unknown error"
		}
		
		C.SSL_free(ssl)
		conn.Close()
		return nil, fmt.Errorf("SSL connect failed: error code %d, %s", errCode, errMsg)
	}

	return &PQCConn{
		conn: conn,
		ssl:  ssl,
		ctx:  d.ctx,
	}, nil
}

// Close 释放资源
func (d *PQCDialer) Close() error {
	if d.ctx != nil {
		C.SSL_CTX_free(d.ctx)
		d.ctx = nil
	}
	return nil
}

// NewPQCListenerOpenSSL 创建一个新的 PQC TLS 监听器（使用 OpenSSL）
func NewPQCListenerOpenSSL(listener net.Listener, certFile, keyFile, caFile string) (*PQCListener, error) {
	// 检查文件是否存在
	if _, err := os.Stat(certFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("certificate file not found: %s", certFile)
	}
	if _, err := os.Stat(keyFile); os.IsNotExist(err) {
		return nil, fmt.Errorf("key file not found: %s", keyFile)
	}
	if caFile != "" {
		if _, err := os.Stat(caFile); os.IsNotExist(err) {
			return nil, fmt.Errorf("CA file not found: %s", caFile)
		}
	}

	cCertFile := C.CString(certFile)
	cKeyFile := C.CString(keyFile)
	var cCaFile *C.char
	if caFile != "" {
		cCaFile = C.CString(caFile)
	}
	defer C.free(unsafe.Pointer(cCertFile))
	defer C.free(unsafe.Pointer(cKeyFile))
	if cCaFile != nil {
		defer C.free(unsafe.Pointer(cCaFile))
	}

	ctx := C.create_server_ctx(cCertFile, cKeyFile, cCaFile)
	if ctx == nil {
		return nil, errors.New("failed to create SSL context for server")
	}

	return &PQCListener{
		listener: listener,
		ctx:      ctx,
	}, nil
}

// NewPQCDialerOpenSSL 创建一个新的 PQC TLS 拨号器（使用 OpenSSL）
func NewPQCDialerOpenSSL(certFile, keyFile, caFile string) (*PQCDialer, error) {
	var cCertFile, cKeyFile, cCaFile *C.char

	if certFile != "" {
		if _, err := os.Stat(certFile); os.IsNotExist(err) {
			return nil, fmt.Errorf("certificate file not found: %s", certFile)
		}
		cCertFile = C.CString(certFile)
		defer C.free(unsafe.Pointer(cCertFile))
	}

	if keyFile != "" {
		if _, err := os.Stat(keyFile); os.IsNotExist(err) {
			return nil, fmt.Errorf("key file not found: %s", keyFile)
		}
		cKeyFile = C.CString(keyFile)
		defer C.free(unsafe.Pointer(cKeyFile))
	}

	if caFile != "" {
		if _, err := os.Stat(caFile); os.IsNotExist(err) {
			return nil, fmt.Errorf("CA file not found: %s", caFile)
		}
		cCaFile = C.CString(caFile)
		defer C.free(unsafe.Pointer(cCaFile))
	}

	ctx := C.create_client_ctx(cCertFile, cKeyFile, cCaFile)
	if ctx == nil {
		return nil, errors.New("failed to create SSL context for client")
	}

	return &PQCDialer{
		ctx: ctx,
	}, nil
}

