package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	_ "github.com/mattn/go-sqlite3"
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

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		status := "WhatsApp bot is running!"
		if clientReady {
			status += " WhatsApp client is connected."
		} else {
			status += " WhatsApp client is initializing..."
		}
		fmt.Fprintln(w, status)
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
				log.Println("QR code:", evt.Code)
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
