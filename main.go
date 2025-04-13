package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"
)

var client *whatsmeow.Client
var clientReady = false
var qrCodeData string
var qrCodeMutex sync.Mutex

func main() {
	serverReady := make(chan bool, 1)
	go startHTTPServer(serverReady)

	<-serverReady
	log.Println("HTTP server is ready and listening")

	go initWhatsAppClient()

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	if client != nil {
		client.Disconnect()
	}
}

func startHTTPServer(ready chan<- bool) {
	http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "OK")
	})

	http.HandleFunc("/qr", func(w http.ResponseWriter, r *http.Request) {
		qrCodeMutex.Lock()
		defer qrCodeMutex.Unlock()

		if qrCodeData == "" {
			w.WriteHeader(http.StatusNotFound)
			fmt.Fprintln(w, "QR code not available yet. Please try again later.")
			return
		}

		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `
            <html>
            <head>
                <title>WhatsApp QR Code</title>
                <meta name="viewport" content="width=device-width, initial-scale=1">
            </head>
            <body style="display: flex; justify-content: center; align-items: center; height: 100vh; flex-direction: column;">
                <h1>Scan this QR code with WhatsApp</h1>
                <img src="data:image/png;base64,%s" alt="WhatsApp QR Code" />
                <p style="margin-top: 20px;">Scan this QR code with your WhatsApp app to log in</p>
            </body>
            </html>
        `, qrCodeData)
	})

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		status := "WhatsApp bot is running!"
		if clientReady {
			status += " WhatsApp client is connected."
		} else {
			status += " WhatsApp client is initializing..."
		}

		qrStatus := ""
		qrCodeMutex.Lock()
		if qrCodeData != "" {
			qrStatus = "QR code is available at <a href='/qr'>/qr</a> endpoint."
		}
		qrCodeMutex.Unlock()

		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `
            <html>
            <head>
                <title>WhatsApp Bot Status</title>
                <meta name="viewport" content="width=device-width, initial-scale=1">
                <style>
                    body { font-family: Arial, sans-serif; margin: 40px; line-height: 1.6; }
                    .container { max-width: 800px; margin: 0 auto; }
                    h1 { color: #4CAF50; }
                    .status { padding: 15px; background-color: #f5f5f5; border-radius: 5px; }
                </style>
            </head>
            <body>
                <div class="container">
                    <h1>WhatsApp Bot Status</h1>
                    <div class="status">
                        <p>%s</p>
                        <p>%s</p>
                    </div>
                </div>
            </body>
            </html>
        `, status, qrStatus)
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8000"
	}

	log.Printf("Starting HTTP server on port %s\n", port)

	ready <- true

	if err := http.ListenAndServe("0.0.0.0:"+port, nil); err != nil {
		log.Fatalf("HTTP server error: %v\n", err)
	}
}

func initWhatsAppClient() {
	logger := waLog.Stdout("Bot", "DEBUG", true)

	dataDir := "/app/data"
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		log.Printf("Error creating data directory: %v\n", err)
		return
	}

	dbPath := filepath.Join(dataDir, "whatsmeow.db")
	log.Printf("Using database at: %s\n", dbPath)

	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		log.Println("Database file doesn't exist, creating...")
		if err := os.WriteFile(dbPath, []byte{}, 0644); err != nil {
			log.Printf("Error creating empty database file: %v\n", err)
		}
	}

	container, err := sqlstore.New("sqlite3", fmt.Sprintf("file:%s?_foreign_keys=on", dbPath), logger)
	if err != nil {
		log.Printf("Error creating database container: %v\n", err)
		return
	}

	deviceStore, err := container.GetFirstDevice()
	if err != nil {
		log.Println("Creating new device...")
		deviceStore = container.NewDevice()
	}

	client = whatsmeow.NewClient(deviceStore, logger)
	client.AddEventHandler(eventHandler)

	if err := client.Connect(); err != nil {
		log.Printf("Error connecting to WhatsApp: %v\n", err)
		return
	}

	if !client.IsLoggedIn() {
		log.Println("Not logged in, please scan QR code")
		qrChan, _ := client.GetQRChannel(context.Background())
		for evt := range qrChan {
			if evt.Event == "code" {
				log.Println("QR code received")

				qr, err := qrcode.Encode(evt.Code, qrcode.Medium, 256)
				if err != nil {
					log.Printf("Error generating QR code: %v\n", err)
					continue
				}

				qrCodeMutex.Lock()
				qrCodeData = base64.StdEncoding.EncodeToString(qr)
				qrCodeMutex.Unlock()

				log.Println("QR code is now available at /qr endpoint")
			}
		}
	} else {
		log.Println("Logged in as", client.Store.ID.String())
	}

	clientReady = true
	log.Println("WhatsApp client initialization complete")
}

func eventHandler(evt interface{}) {
	switch v := evt.(type) {
	case *events.Message:
		var msgText string
		if v.Message.GetConversation() != "" {
			msgText = v.Message.GetConversation()
		} else if v.Message.GetExtendedTextMessage() != nil {
			msgText = v.Message.GetExtendedTextMessage().GetText()
		}

		if msgText != "" {
			log.Printf("Received message: %s\n", msgText)
			switch msgText {
			case "Halo":
				respondToMessage(v, "Hai! Ada yang bisa saya bantu?")
			case "Waktu":
				currentTime := time.Now().Format("15:04:05")
				respondToMessage(v, "Waktu saat ini: "+currentTime)
			case "Tanggal":
				currentDate := time.Now().Format("2006-01-02")
				respondToMessage(v, "Tanggal hari ini: "+currentDate)
			case "Info":
				respondToMessage(v, "Saya adalah bot WhatsApp sederhana dibuat dengan Go dan whatsmeow.")
			default:
				respondToMessage(v, "Anda mengirim: "+msgText)
			}
		}
	}
}

func respondToMessage(evt *events.Message, text string) {
	var recipient types.JID
	if evt.Info.IsGroup {
		recipient = evt.Info.Chat
	} else {
		recipient = evt.Info.Sender
	}

	msg := &waProto.Message{
		Conversation: proto.String(text),
	}

	_, err := client.SendMessage(context.Background(), recipient, msg)
	if err != nil {
		log.Printf("Error sending message: %v\n", err)
	} else {
		log.Printf("Message sent: %s\n", text)
	}
}
