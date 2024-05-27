if [ -z "$1" ] || [ -z "$2" ]; then
    echo "Usage: $0 <os> <architecture>"
    exit 1
fi


if [ "$1" != "linux" ] && [ "$1" != "windows" ]; then
    echo "Usage: $0 linux/windows <architecture>"
    exit 1
fi

if [ "$2" != "amd64" ] && [ "$2" != "arm64" ]; then
    echo "Usage: $0 <os> amd64/arm64"
    exit 1
fi


OS=$1
ARCH=$2

docker build -f $ARCH.dockerfile -t dnslogtail-builder-$ARCH:latest .

mkdir /tmp/dns-logtail
cp -r ../* /tmp/dns-logtail/

BUILD_CMD="cd cmd/dnslogtail && go build"

if [ "$OS" == "linux" ]; then
  rm -rf /tmp/dns-logtail/reader/channel_reader.go
  rm -rf /tmp/dns-logtail/reader/etl_reader.go
  rm -rf /tmp/dns-logtail/eventlog/normal_log.go
else
  BUILD_CMD="cd cmd/wineventtail && go build"
fi



if [ "$ARCH" == "arm64" ]; then
    docker run -it --rm \
      -v /tmp/dns-logtail:/tmp/dns-logtail \
      -w /tmp/dns-logtail \
      -e CGO_ENABLED=1 \
      -e CC=aarch64-linux-gnu-gcc \
      -e CGO_LDFLAGS="-L/libpcap/libpcap-1.8.1" \
      dnslogtail-builder-$ARCH:latest \
      --build-cmd "$BUILD_CMD" \
      -p "linux/$ARCH"
else
    docker run -it --rm \
      -v /tmp/dns-logtail:/tmp/dns-logtail \
      -w /tmp/dns-logtail \
      -e CGO_ENABLED=1 \
      dnslogtail-builder-$ARCH:latest \
      --build-cmd "$BUILD_CMD" \
      -p "$OS/$ARCH"
fi


mkdir -p ../deploy/$OS/$ARCH
if [ "$OS" == "linux" ]; then
  cp /tmp/dns-logtail/cmd/dnslogtail/dnslogtail  ../deploy/$OS/$ARCH/
else
  cp /tmp/dns-logtail/cmd/wineventtail/wineventtail.exe  ../deploy/$OS/$ARCH/
fi


rm -rf /tmp/dns-logtail