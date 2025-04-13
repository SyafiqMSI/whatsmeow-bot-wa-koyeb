package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/binary/proto"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	_ "github.com/mattn/go-sqlite3"
)

var client *whatsmeow.Client

func main() {
	logger := waLog.Stdout("Bot", "DEBUG", true)

	dataDir := "/app/data"
	if _, err := os.Stat(dataDir); os.IsNotExist(err) {
		if err := os.MkdirAll(dataDir, 0755); err != nil {
			return
		}
	}

	dbPath := filepath.Join(dataDir, "whatsmeow.db")

	container, err := sqlstore.New("sqlite3", fmt.Sprintf("file:%s?_foreign_keys=on", dbPath), logger)
	if err != nil {
		os.WriteFile(dbPath, []byte{}, 0644)
		container, err = sqlstore.New("sqlite3", fmt.Sprintf("file:%s?_foreign_keys=on", dbPath), logger)
		if err != nil {
			return
		}
	}

	deviceStore, err := container.GetFirstDevice()
	if err != nil {
		deviceStore = container.NewDevice()
	}

	client = whatsmeow.NewClient(deviceStore, logger)
	client.AddEventHandler(eventHandler)

	if err := client.Connect(); err != nil {
		return
	}

	if !client.IsLoggedIn() {
		qrChan, _ := client.GetQRChannel(context.Background())
		for evt := range qrChan {
			if evt.Event == "code" {
				fmt.Println("QR code:", evt.Code)
			}
		}
	} else {
		fmt.Println("Logged in as", client.Store.ID.String())
	}

	go func() {
		http.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			fmt.Fprintln(w, "OK")
		})
		http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintln(w, "WhatsApp bot is running!")
		})

		port := os.Getenv("PORT")
		if port == "" {
			port = "8000"
		}

		fmt.Printf("Starting HTTP server on port %s\n", port)
		if err := http.ListenAndServe("0.0.0.0:"+port, nil); err != nil {
			fmt.Printf("HTTP server error: %v\n", err)
		}
	}()

	fmt.Println("HTTP server initiated")

	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)
	<-c

	client.Disconnect()
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

	client.SendMessage(context.Background(), recipient, msg)
}
