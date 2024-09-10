# 构建阶段
FROM golang:1.21-alpine AS builder

# 设置 Go 模块代理为国内代理
ENV GOPROXY=https://goproxy.cn,direct

# 设置工作目录
WORKDIR /app

# 将 Go 模块下载到缓存中
COPY go.mod ./
COPY go.sum ./
RUN go mod download

# 复制应用代码
COPY . .

# 编译应用
RUN go build -o main .

# 运行阶段
FROM alpine:latest

# 复制编译后的二进制文件
COPY --from=builder /app/main /app/main

# 暴露应用端口
EXPOSE 2112

# 启动应用
ENTRYPOINT ["/app/main"]
