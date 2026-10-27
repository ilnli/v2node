# Build go
FROM golang:1.27.1-alpine AS builder
WORKDIR /app
COPY . .
ENV CGO_ENABLED=0
RUN GOEXPERIMENT=jsonv2 go mod download
RUN GOEXPERIMENT=jsonv2 go build -v -o proxynode

# Release
FROM  alpine
# 安装必要的工具包
RUN  apk --update --no-cache add tzdata ca-certificates \
    && cp /usr/share/zoneinfo/Asia/Shanghai /etc/localtime
RUN mkdir /etc/proxynode/
COPY --from=builder /app/proxynode /usr/local/bin

ENTRYPOINT [ "proxynode", "server", "--config", "/etc/proxynode/config.json"]
