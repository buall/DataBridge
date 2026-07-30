package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"databridge/internal/api"
	"databridge/internal/backup"
	"databridge/internal/license"
	"databridge/internal/store"
)

var (
	version          = "dev"
	licensePublicKey = ""
)

func main() {
	applyAppTimezone()
	removed, cleanupErr := backup.CleanupStaleWorkDirs()
	if cleanupErr != nil {
		log.Printf("cleanup stale backup work directories failed: %v", cleanupErr)
	}
	if removed > 0 {
		log.Printf("cleaned up %d stale backup work directories", removed)
	}
	addr := env("DATABRIDGE_ADDR", ":8081")
	databasePath := env("DATABRIDGE_DB_PATH", "data/databridge.db")
	keyPath := env("DATABRIDGE_KEY_PATH", "data/databridge.key")
	licenseDirectory := env("DATABRIDGE_LICENSE_DIR", filepath.Join(filepath.Dir(databasePath), "license"))
	verificationKey := licensePublicKey
	if verificationKey == "" {
		verificationKey = os.Getenv("DATABRIDGE_LICENSE_PUBLIC_KEY")
	}
	licenseManager, err := license.New(license.Config{
		Directory:      licenseDirectory,
		ProductVersion: version,
		PublicKey:      verificationKey,
		ActivationURL:  os.Getenv("DATABRIDGE_LICENSE_SERVER_URL"),
	})
	if err != nil {
		log.Fatalf("initialize license manager: %v", err)
	}
	appStore, err := store.New(databasePath, keyPath)
	if err != nil {
		log.Fatalf("initialize persistent store: %v", err)
	}
	defer appStore.Close()
	log.Printf("SQLite persistence: %s", databasePath)
	licenseStatus := licenseManager.Status()
	log.Printf("DataBridge %s deployment: %s (license: %s)", version, licenseStatus.DeploymentID, licenseStatus.State)
	handler := api.NewWithLicense(appStore, licenseManager)
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
