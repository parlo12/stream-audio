# ---- build ----
FROM golang:1.22 as build
WORKDIR /src
COPY content-service/go.mod content-service/go.sum ./
RUN go mod download
COPY content-service/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /out/content-service

# ---- runtime ----
FROM gcr.io/distroless/base-debian12
WORKDIR /app
COPY --from=build /out/content-service /app/
COPY content-service/wait-for-postgres.sh /app/wait-for-postgres.sh
# audio & uploads directories the app serves from:
RUN mkdir -p /app/audio /app/uploads/covers
VOLUME ["/app/audio", "/app/uploads/covers"]
EXPOSE 8083
ENTRYPOINT ["/app/wait-for-postgres.sh", "/app/content-service"]