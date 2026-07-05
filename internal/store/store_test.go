package store

import (
	"context"
	"errors"
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
		Endpoints: []string{"https://etcd-1:2379", "https://etcd-2:2379"},
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
	if len(loadedConnection.Endpoints) != 2 || loadedConnection.Endpoints[1] != "https://etcd-2:2379" {
		t.Fatalf("endpoints = %#v", loadedConnection.Endpoints)
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

func TestStoreRecoversInterruptedScheduledBackupWithoutImmediateRetry(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "databridge.db")
	keyPath := filepath.Join(directory, "databridge.key")
	first, err := New(databasePath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	due := time.Now().UTC().Add(-time.Hour)
	backup := model.BackupJob{
		ID: NewID(), Name: "interrupted schedule", Status: model.StatusRunning,
		Stage: "export", StartedAt: due, ScheduleMode: "scheduled", NextRunAt: &due,
		SourceName: "source", SourceType: "MongoDB", Destination: "object",
		ArtifactVersions: []model.BackupArtifactVersion{{
			ID: "run-1", StartedAt: due, Status: model.StatusRunning, Stage: "export",
		}},
		Events: []model.Event{},
	}
	if err := first.AddBackup(backup, context.CancelFunc(func() {})); err != nil {
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
	loaded, err := second.Backup(backup.ID)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Status != model.StatusScheduled || loaded.Stage != "scheduled" {
		t.Fatalf("status = %s, stage = %s", loaded.Status, loaded.Stage)
	}
	if loaded.NextRunAt != nil {
		t.Fatalf("stale next run was retained: %v", loaded.NextRunAt)
	}
	if len(loaded.ArtifactVersions) != 1 || loaded.ArtifactVersions[0].Status != model.StatusFailed || loaded.ArtifactVersions[0].FinishedAt == nil {
		t.Fatalf("interrupted version was not finalized: %#v", loaded.ArtifactVersions)
	}
}

func TestStoreNotificationRecipientCRUDAndEncryption(t *testing.T) {
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "databridge.db")
	appStore, err := New(databasePath, filepath.Join(directory, "databridge.key"))
	if err != nil {
		t.Fatal(err)
	}
	defer appStore.Close()

	const target = "https://oapi.dingtalk.com/robot/send?access_token=secret-token"
	created, err := appStore.AddNotificationRecipient(model.NotificationRecipientInput{Name: "运维群", Type: "dingtalk", Target: target, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if created.Target != target || created.TargetMasked == target || !created.TargetSet {
		t.Fatalf("unexpected created recipient: %#v", created)
	}
	databaseBytes, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(databaseBytes), "secret-token") {
		t.Fatal("database contains plaintext notification target")
	}
	updated, err := appStore.UpdateNotificationRecipient(created.ID, model.NotificationRecipientInput{Name: "值班群", Type: "dingtalk", Enabled: false})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "值班群" || updated.Enabled || updated.Target != target {
		t.Fatalf("unexpected updated recipient: %#v", updated)
	}
	if err := appStore.DeleteNotificationRecipient(created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := appStore.NotificationRecipient(created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestTelegramRecipientTargetSummary(t *testing.T) {
	item := model.NotificationRecipient{Type: "telegram", Target: `{"botToken":"secret","chatId":"-1001234567890"}`}
	setNotificationRecipientTargetSummary(&item)
	if got, want := item.TargetMasked, "Chat ID: ••••7890"; got != want {
		t.Fatalf("target masked = %q, want %q", got, want)
	}
	if !item.TargetSet {
		t.Fatal("target should be set")
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
