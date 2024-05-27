FROM --platform=$TARGETPLATFORM docker.elastic.co/beats-dev/golang-crossbuild:1.19-arm

RUN apt-get install -y debian-archive-keyring

COPY sources.list.arm64 /etc/apt/sources.list

RUN apt-get -y update && apt-get install -y libpcap-dev --allow-unauthenticated