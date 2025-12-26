#!/bin/sh

echo "正在生成测试配置文件..."
source .env-local

echo "配置文件已生成: config.test.yaml"
echo "正在启动本地测试..."
echo ""

# 运行本地程序
./rulerefinery --config ./config.test.yaml
