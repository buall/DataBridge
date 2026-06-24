package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"databridge/internal/model"
)

func TestStorePersistsEncryptedConnectionAndMigration(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "databridge.db")
	keyPath := filepath.Join(directory, "databridge.key")

	first, err := New(databasePath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	connection, err := first.AddConnection(model.ConnectionInput{
		Name: "source", Host: "db.example.internal", Port: 5432,
		Username: "migration", Password: "secret-password", SSLMode: "require",
	}, 15*time.Millisecond, "16.3")
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now().UTC().Truncate(time.Millisecond)
	migration := model.Migration{
		ID: NewID(), Name: "persistent task", Status: model.StatusCompleted,
		Stage: "completed", Progress: 100, StartedAt: started,
		SourceName: "source", TargetName: "target",
		SourceDatabase: "app", TargetDatabase: "app_copy",
		Results: []model.TableResult{{
			Table: "public.users", SourceRows: 12, TargetRows: 12,
			CopiedRows: 12, Duration: 1250 * time.Millisecond, Valid: true,
		}},
		Events: []model.Event{{Time: started, Level: "INFO", Message: "done"}},
	}
	if err := first.AddMigration(migration, func() {}); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	databaseBytes, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(databaseBytes), "secret-password") {
		t.Fatal("database contains plaintext password")
	}

	second, err := New(databasePath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	loadedConnection, err := second.Connection(connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loadedConnection.Password != "secret-password" {
		t.Fatalf("password = %q", loadedConnection.Password)
	}
	if loadedConnection.Version != "16.3" {
		t.Fatalf("version = %q", loadedConnection.Version)
	}
	loadedMigration, err := second.Migration(migration.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(loadedMigration.Results) != 1 || loadedMigration.Results[0].Duration != 1250*time.Millisecond {
		t.Fatalf("migration result was not preserved: %#v", loadedMigration.Results)
	}
}

func TestStoreMarksRunningMigrationFailedAfterRestart(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "databridge.db")
	keyPath := filepath.Join(directory, "databridge.key")
	first, err := New(databasePath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	migration := model.Migration{
		ID: NewID(), Name: "interrupted", Status: model.StatusRunning,
		Stage: "data", StartedAt: time.Now().UTC(),
		Results: []model.TableResult{}, Events: []model.Event{},
	}
	if err := first.AddMigration(migration, context.CancelFunc(func() {})); err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second, err := New(databasePath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	loaded, err := second.Migration(migration.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != model.StatusFailed || loaded.Stage != "failed" {
		t.Fatalf("status = %s, stage = %s", loaded.Status, loaded.Stage)
	}
	if !strings.Contains(loaded.Error, "服务重启") {
		t.Fatalf("error = %q", loaded.Error)
	}
}

func TestStoreReplacesAndClearsBackupCancel(t *testing.T) {
	directory := t.TempDir()
	appStore, err := New(filepath.Join(directory, "databridge.db"), filepath.Join(directory, "databridge.key"))
	if err != nil {
		t.Fatal(err)
	}
	defer appStore.Close()

	backup := model.BackupJob{
		ID:          NewID(),
		Name:        "scheduled",
		Status:      model.StatusScheduled,
		Stage:       "scheduled",
		StartedAt:   time.Now().UTC(),
		SourceName:  "source",
		SourceType:  "PostgreSQL",
		Destination: "local",
		Events:      []model.Event{},
	}
	firstCancelled := 0
	if err := appStore.AddBackup(backup, func() { firstCancelled++ }); err != nil {
		t.Fatal(err)
	}
	secondCancelled := 0
	if err := appStore.SetBackupCancel(backup.ID, func() { secondCancelled++ }); err != nil {
		t.Fatal(err)
	}
	if err := appStore.CancelBackup(backup.ID); err != nil {
		t.Fatal(err)
	}
	if firstCancelled != 0 || secondCancelled != 1 {
		t.Fatalf("cancel counts after replacement: first=%d second=%d", firstCancelled, secondCancelled)
	}

	appStore.ClearBackupCancel(backup.ID)
	if err := appStore.CancelBackup(backup.ID); err != nil {
		t.Fatal(err)
	}
	if secondCancelled != 1 {
		t.Fatalf("cleared cancel should not be called again: second=%d", secondCancelled)
	}
}
