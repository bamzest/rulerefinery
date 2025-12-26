#!/bin/sh

echo "正在生成测试配置文件..."
source .env-docker

echo "配置文件已生成: config.test.yaml"
echo "正在启动 Docker 容器测试..."
echo ""

# 运行 Docker 容器
docker run --rm \
  -v $(pwd)/config.test.yaml:/app/config.yaml \
  -v $(pwd)/log:/app/log \
  -v $(pwd)/rule_config:/app/rule_config \
  -v $(pwd)/rule_sources:/app/rule_sources \
  -v $(pwd)/rules:/app/rules \
  ghcr.io/bamzest/rulerefinery:latest
