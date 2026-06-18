package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"databridge/internal/api"
	"databridge/internal/store"
)

func main() {
	applyAppTimezone()
	addr := env("DATABRIDGE_ADDR", ":8081")
	databasePath := env("DATABRIDGE_DB_PATH", "data/databridge.db")
	keyPath := env("DATABRIDGE_KEY_PATH", "data/databridge.key")
	appStore, err := store.New(databasePath, keyPath)
	if err != nil {
		log.Fatalf("initialize persistent store: %v", err)
	}
	defer appStore.Close()
	log.Printf("SQLite persistence: %s", databasePath)
	handler := api.New(appStore)
	server := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("DataBridge API listening on %s", addr)
		if serveErr := server.ListenAndServe(); serveErr != nil && !errors.Is(serveErr, http.ErrServerClosed) {
			log.Fatal(serveErr)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "shutdown: %v\n", err)
	}
}

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func applyAppTimezone() {
	location, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		log.Printf("load app timezone failed: %v", err)
		return
	}
	time.Local = location
}
