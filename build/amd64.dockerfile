FROM --platform=$TARGETPLATFORM docker.elastic.co/beats-dev/golang-crossbuild:1.19-main

RUN apt-get install -y debian-archive-keyring

COPY sources.list.amd64 /etc/apt/sources.list

RUN apt-get -y update && apt-get install -y libpcap-dev --allow-unauthenticated