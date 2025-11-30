package proto

import (
	"encoding/binary"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// FrameType 表示帧类型
type FrameType byte

const (
	// FrameTypeNEW_CONN 表示新连接请求（server → client）
	FrameTypeNEW_CONN FrameType = 0x01
	// FrameTypeDATA 表示数据传输（双向）
	FrameTypeDATA FrameType = 0x02
	// FrameTypeCLOSE 表示连接关闭（双向）
	FrameTypeCLOSE FrameType = 0x03
	// FrameTypeINIT 表示初始化配置（client → server）
	FrameTypeINIT FrameType = 0x04
)

// Frame 表示一个协议帧
// 帧格式：1 byte frame_type | 4 bytes conn_id | 4 bytes payload_len | payload...
type Frame struct {
	Type    FrameType // 帧类型
	ConnID  uint32    // 连接 ID
	Payload []byte    // 负载数据（NEW_CONN 和 CLOSE_CONN 时可能为空）
}

// EncodeFrame 将 Frame 编码为字节流
// 返回的字节数组格式：frame_type(1) + conn_id(4) + payload_len(4) + payload(n)
func EncodeFrame(f *Frame) ([]byte, error) {
	if f == nil {
		return nil, io.ErrUnexpectedEOF
	}

	// 计算总长度：1 + 4 + 4 + payload_len
	payloadLen := len(f.Payload)
	totalLen := 1 + 4 + 4 + payloadLen

	// 分配缓冲区
	buf := make([]byte, totalLen)
	offset := 0

	// 写入 frame_type (1 byte)
	buf[offset] = byte(f.Type)
	offset++

	// 写入 conn_id (4 bytes, big endian)
	binary.BigEndian.PutUint32(buf[offset:offset+4], f.ConnID)
	offset += 4

	// 写入 payload_len (4 bytes, big endian)
	binary.BigEndian.PutUint32(buf[offset:offset+4], uint32(payloadLen))
	offset += 4

	// 写入 payload（如果存在）
	if payloadLen > 0 {
		copy(buf[offset:], f.Payload)
	}

	return buf, nil
}

// DecodeFrame 从 io.Reader 读取并解码一个完整的帧
// 该函数会阻塞直到读取到完整的帧数据
func DecodeFrame(r io.Reader) (*Frame, error) {
	// 读取帧头：frame_type(1) + conn_id(4) + payload_len(4) = 9 bytes
	header := make([]byte, 9)
	if _, err := io.ReadFull(r, header); err != nil {
		return nil, err
	}

	// 解析 frame_type
	frameType := FrameType(header[0])

	// 解析 conn_id (big endian)
	connID := binary.BigEndian.Uint32(header[1:5])

	// 解析 payload_len (big endian)
	payloadLen := binary.BigEndian.Uint32(header[5:9])

	// 创建 Frame
	frame := &Frame{
		Type:   frameType,
		ConnID: connID,
	}

	// 如果 payload_len > 0，读取 payload
	if payloadLen > 0 {
		// 分配 payload 缓冲区
		payload := make([]byte, payloadLen)
		if _, err := io.ReadFull(r, payload); err != nil {
			return nil, err
		}
		frame.Payload = payload
	} else {
		// payload_len = 0，Payload 保持为 nil
		frame.Payload = nil
	}

	return frame, nil
}

// InitConfig 表示初始化配置信息
type InitConfig struct {
	RemotePort int    // 远程端口（服务器要监听的端口）
	LocalAddr  string // 本地地址（客户端要映射的本地服务地址）
}

// EncodeInitConfig 将 InitConfig 编码为字符串（简单格式：remotePort:localAddr）
func EncodeInitConfig(config *InitConfig) []byte {
	return []byte(fmt.Sprintf("%d:%s", config.RemotePort, config.LocalAddr))
}

// DecodeInitConfig 从字节数组解码 InitConfig
func DecodeInitConfig(data []byte) (*InitConfig, error) {
	parts := strings.SplitN(string(data), ":", 2)
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid init config format")
	}

	remotePort, err := strconv.Atoi(parts[0])
	if err != nil {
		return nil, fmt.Errorf("invalid remote port: %v", err)
	}

	return &InitConfig{
		RemotePort: remotePort,
		LocalAddr:  parts[1],
	}, nil
}
