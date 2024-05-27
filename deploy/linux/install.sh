#!/usr/bin bash

# 目标路径
BIN_DIR="/usr/local/sbin"
CONFIG_DIR="/etc/dnslogtail"

# 检查路径是否存在
if [ ! -d "$CONFIG_DIR" ]; then
  mkdir -p "$CONFIG_DIR"
fi

mv dnslogtail.yml "$CONFIG_DIR/dnslogtail.yaml"
mv dnslogtail "$BIN_DIR/dnslogtail"

cd $BIN_DIR
dnslogtail -conf "$CONFIG_DIR/dnslogtail.yaml" -service install
service dnslogtail start