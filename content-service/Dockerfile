# ---- build ----
FROM golang:1.23 as build
WORKDIR /src
COPY content-service/go.mod content-service/go.sum ./
RUN go mod download
COPY content-service/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/content-service

# ---- runtime ----
FROM debian:12-slim
WORKDIR /app

# Install FFmpeg and other dependencies
RUN apt-get update && apt-get install -y \
    ffmpeg \
    ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY --from=build /out/content-service /app/
COPY content-service/wait-for-postgres.sh /app/wait-for-postgres.sh

# Make the script executable
RUN chmod +x /app/wait-for-postgres.sh

# audio & uploads directories the app serves from:
RUN mkdir -p /app/audio /app/uploads/covers
VOLUME ["/app/audio", "/app/uploads/covers"]

EXPOSE 8083
ENTRYPOINT ["/app/wait-for-postgres.sh", "/app/content-service"]