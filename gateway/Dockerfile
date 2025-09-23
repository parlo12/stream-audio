# ---- build ----
FROM golang:1.22-alpine as build
WORKDIR /src
COPY gateway/go.mod gateway/go.sum ./
RUN go mod download
COPY gateway/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/gateway

# ---- runtime ----
FROM alpine:3.18
# Install wget for health checks
RUN apk --no-cache add wget
WORKDIR /app
COPY --from=build /out/gateway /app/
EXPOSE 8080
CMD ["./gateway"]