#!/bin/bash
# Windows 版本编译脚本

set -e

echo "开始编译 Windows 版本..."

# 创建 bin 目录（如果不存在）
mkdir -p bin

# 编译 Windows 64位版本
echo "编译 Windows 64位服务器..."
GOOS=windows GOARCH=amd64 go build -o bin/server.exe ./cmd/server

echo "编译 Windows 64位客户端..."
GOOS=windows GOARCH=amd64 go build -o bin/client.exe ./cmd/client

echo "编译完成！"
echo "可执行文件位于 bin/ 目录："
ls -lh bin/*.exe

