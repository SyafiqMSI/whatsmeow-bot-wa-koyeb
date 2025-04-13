FROM golang:1.23

WORKDIR /app

RUN apk add --no-cache git gcc musl-dev sqlite ca-certificates

COPY . .

RUN rm -f go.mod go.sum && \
    go mod init whatsapp-bot && \
    go get github.com/mattn/go-sqlite3 && \
    go get go.mau.fi/whatsmeow && \
    go get google.golang.org/protobuf && \
    go get github.com/skip2/go-qrcode && \
    go mod tidy

RUN go build -o whatsapp-bot

RUN mkdir -p /app/data && chmod 777 /app/data

ENV PORT=8000

EXPOSE 8000

CMD ["./whatsapp-bot"]