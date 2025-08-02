FROM golang:1.24.2-alpine
WORKDIR /app
COPY go.mod ./
#COPY go.sum ./
RUN go mod download
COPY . .
RUN go build -o auth-service .
EXPOSE 8082
COPY wait-for-postgres.sh .
ENTRYPOINT ["./wait-for-postgres.sh"]
CMD ["./auth-service"]