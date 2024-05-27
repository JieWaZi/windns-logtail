#!/usr/bin bash

# 目标路径
BIN_DIR="/usr/local/sbin"
CONFIG_DIR="/etc/dnslogtail"

cd $BIN_DIR
service dnslogtail stop
dnslogtail -conf "$CONFIG_DIR/dnslogtail.yaml" -service uninstall

# 检查路径是否存在
if [ -d "$CONFIG_DIR" ]; then
  rm -rf "$CONFIG_DIR"
fi

rm -rf "$BIN_DIR/dnslogtail"