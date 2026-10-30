# Build go
FROM golang:1.27.3-alpine AS builder
WORKDIR /app
COPY . .
ENV CGO_ENABLED=0
RUN GOEXPERIMENT=jsonv2 go mod download
RUN GOEXPERIMENT=jsonv2 go build -v -o proxynode

FROM alpine AS geodata
RUN apk --no-cache add bash curl ca-certificates coreutils
COPY script/download-geodata.sh /download-geodata.sh
RUN bash /download-geodata.sh /geodata

# Release
FROM  alpine
# 安装必要的工具包
RUN  apk --update --no-cache add tzdata ca-certificates \
    && cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime
RUN mkdir /etc/proxynode/
COPY --from=builder /app/proxynode /usr/local/bin
COPY --from=geodata /geodata/geoip.dat /geodata/geosite.dat /geodata/GEODATA_SOURCE.txt /usr/local/share/xray/
ENV XRAY_LOCATION_ASSET=/usr/local/share/xray

ENTRYPOINT [ "proxynode", "server", "--config", "/etc/proxynode/config.json"]
