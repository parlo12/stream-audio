# syntax=docker/dockerfile:1
FROM golang:1.24.2-alpine

ENV GOPROXY=https://proxy.golang.org,direct

# Install system dependencies: ffmpeg, git, postgres client for pg_isready
RUN apk update && apk add --no-cache ffmpeg git postgresql-client

WORKDIR /app

# ✅ Step 1: Copy only mod files first and cache Go modules
COPY go.mod .
COPY go.sum .
RUN go mod download

# ✅ Step 2: Copy full source
COPY . .

# ✅ Step 3: Build the Go binary
RUN go build -o content-service .

EXPOSE 8083

# ✅ Step 4: Copy startup wait script and make it executable
COPY wait-for-postgres.sh .
RUN chmod +x wait-for-postgres.sh

# ✅ Step 5: Entrypoint and command
ENTRYPOINT ["./wait-for-postgres.sh"]
CMD ["./content-service"]