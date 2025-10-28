# ---- build ----
FROM golang:1.24 as build
WORKDIR /src
COPY auth-service/go.mod auth-service/go.sum ./
RUN go mod download
COPY auth-service/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/auth-service

# ---- runtime ----
FROM debian:12-slim
RUN apt-get update && apt-get install -y \
    ca-certificates \
    postgresql-client \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /app
COPY --from=build /out/auth-service /app/
COPY auth-service/wait-for-postgres.sh /app/wait-for-postgres.sh
RUN chmod +x /app/wait-for-postgres.sh
EXPOSE 8082
ENTRYPOINT ["/app/wait-for-postgres.sh", "/app/auth-service"]