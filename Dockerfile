# Build stage
FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
RUN CGO_ENABLED=0 go build -o /lxdapi ./cmd/lxdapi/

# Runtime stage
FROM alpine:latest
RUN apk --no-cache add ca-certificates tzdata
WORKDIR /app
COPY --from=builder /lxdapi /app/lxdapi
COPY server/configs/ /app/configs/
# templates/docs/static 通过 go:embed 编译进二进制，无需复制
# web/ 是独立前端，如需 nginx 可另起服务

# Railway sets PORT env var
ENV PORT=8080
EXPOSE 8080

CMD ["/app/lxdapi"]
