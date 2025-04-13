FROM golang:1.23-alpine

WORKDIR /app

RUN apk add --no-cache git gcc musl-dev sqlite ca-certificates

COPY . .

RUN rm -f go.mod go.sum && \
    go mod init whatsapp-bot && \
    go get github.com/mattn/go-sqlite3 && \
    go get go.mau.fi/whatsmeow && \
    go get google.golang.org/protobuf@v1.28.1 && \
    go mod tidy

RUN go build -o whatsapp-bot

RUN mkdir -p /app/data && chmod 777 /app/data

CMD ["./whatsapp-bot"]