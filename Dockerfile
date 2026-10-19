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
COPY server/templates/ /app/templates/
COPY server/static/ /app/static/
COPY web/ /app/web/

# Railway sets PORT env var
ENV PORT=8080
EXPOSE 8080

CMD ["/app/lxdapi"]
