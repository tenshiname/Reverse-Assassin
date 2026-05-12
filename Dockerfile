# 构建阶段
FROM golang:1.22-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=1 go build -o /assassin ./cmd/assassin

# 运行阶段
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata sqlite-libs
ENV TZ=Asia/Shanghai

WORKDIR /app
COPY --from=builder /assassin .
COPY web/static/ ./web/static/

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/api/status || exit 1

ENTRYPOINT ["./assassin"]
CMD ["-mode", "server", "-port", "8080"]
