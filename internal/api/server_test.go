package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"databridge/internal/model"
	"databridge/internal/store"
)

func TestNextBackupRunUsesAppTimezone(t *testing.T) {
	request := model.BackupRequest{
		ScheduleMode:      "scheduled",
		ScheduleFrequency: "daily",
		ScheduleTime:      "03:00:00",
	}
	after := time.Date(2026, 6, 17, 18, 37, 56, 0, time.UTC)

	next, err := nextBackupRun(request, after)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := next.In(appLocation).Format("2006-01-02 15:04:05"), "2026-06-18 03:00:00"; got != want {
		t.Fatalf("next run = %s, want %s", got, want)
	}
}

func TestNormalizeBackupScheduleKeepsRecipientSelection(t *testing.T) {
	request := model.BackupRequest{
		ScheduleMode:             "scheduled",
		ScheduleFrequency:        "daily",
		ScheduleTime:             "02:00:00",
		NotificationEnabled:      true,
		NotificationRecipientIDs: []string{"recipient-a", "recipient-b"},
	}
	normalizeBackupSchedule(&request)
	if !request.Notification.Enabled {
		t.Fatal("notification should remain enabled")
	}
	if got := request.Notification.RecipientIDs; len(got) != 2 || got[0] != "recipient-a" || got[1] != "recipient-b" {
		t.Fatalf("recipient ids = %#v", got)
	}
	if request.Notification.Target != "" {
		t.Fatalf("legacy target should stay empty, got %q", request.Notification.Target)
	}
}

func TestNotificationRecipientInputCanBeTestedBeforeSave(t *testing.T) {
	_, server, _ := newTestServer(t)
	received := make(chan struct{}, 1)
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer webhook.Close()

	body := bytes.NewBufferString(`{"name":"运维群","type":"webhook","target":"` + webhook.URL + `","enabled":true}`)
	response := httptest.NewRecorder()
	server.testNotificationRecipientInput(response, httptest.NewRequest(http.MethodPost, "/api/settings/notification-recipients/test", body))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("test webhook did not receive a notification")
	}
}

func TestNotificationRecipientInputUsesSavedTargetWhenEditing(t *testing.T) {
	appStore, server, _ := newTestServer(t)
	received := make(chan struct{}, 1)
	webhook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		received <- struct{}{}
		w.WriteHeader(http.StatusOK)
	}))
	defer webhook.Close()
	recipient, err := appStore.AddNotificationRecipient(model.NotificationRecipientInput{Name: "运维群", Type: "webhook", Target: webhook.URL, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}

	body := bytes.NewBufferString(`{"id":"` + recipient.ID + `","name":"运维群","type":"webhook","enabled":true}`)
	response := httptest.NewRecorder()
	server.testNotificationRecipientInput(response, httptest.NewRequest(http.MethodPost, "/api/settings/notification-recipients/test", body))
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	select {
	case <-received:
	case <-time.After(time.Second):
		t.Fatal("saved webhook did not receive a notification")
	}
}

func TestNextBackupRunRollsToNextDay(t *testing.T) {
	request := model.BackupRequest{
		ScheduleMode:      "scheduled",
		ScheduleFrequency: "daily",
		ScheduleTime:      "03:00:00",
	}
	after := time.Date(2026, 6, 18, 3, 1, 0, 0, appLocation)

	next, err := nextBackupRun(request, after)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := next.In(appLocation).Format("2006-01-02 15:04:05"), "2026-06-19 03:00:00"; got != want {
		t.Fatalf("next run = %s, want %s", got, want)
	}
}

func TestScheduleClockMatchesDetectsLegacyUTCNextRun(t *testing.T) {
	request := model.BackupRequest{
		ScheduleMode:      "scheduled",
		ScheduleFrequency: "daily",
		ScheduleTime:      "03:00:00",
	}
	legacyUTCNextRun := time.Date(2026, 6, 18, 3, 0, 0, 0, time.UTC)
	if scheduleClockMatches(request, legacyUTCNextRun) {
		t.Fatal("legacy UTC next run should not match the configured local schedule clock")
	}

	localNextRun := time.Date(2026, 6, 18, 3, 0, 0, 0, appLocation)
	if !scheduleClockMatches(request, localNextRun) {
		t.Fatal("local next run should match the configured schedule clock")
	}
}

func TestNormalizeBackupScheduleRetentionDays(t *testing.T) {
	request := model.BackupRequest{ScheduleMode: "scheduled", RetentionDays: 99}
	normalizeBackupSchedule(&request)
	if request.RetentionDays != 99 {
		t.Fatalf("retention days = %d, want 99", request.RetentionDays)
	}

	request = model.BackupRequest{ScheduleMode: "scheduled"}
	normalizeBackupSchedule(&request)
	if request.RetentionDays != 30 {
		t.Fatalf("default retention days = %d, want 30", request.RetentionDays)
	}

	request = model.BackupRequest{ScheduleMode: "scheduled", RetentionMonths: 2}
	normalizeBackupSchedule(&request)
	if request.RetentionDays != 60 {
		t.Fatalf("legacy retention months should convert to days = %d, want 60", request.RetentionDays)
	}

	request = model.BackupRequest{ScheduleMode: "once", RetentionDays: 3}
	normalizeBackupSchedule(&request)
	if request.RetentionDays != 0 {
		t.Fatalf("one-time retention days = %d, want 0", request.RetentionDays)
	}
}

func TestBackupNotificationTextIncludesFinishedAtAndArtifacts(t *testing.T) {
	finishedAt := time.Date(2026, 6, 23, 2, 15, 30, 0, time.UTC)
	job := model.BackupJob{
		ID:             "b73416a29db22892",
		Name:           "腾讯云新加坡 / 3 个数据库 备份",
		SourceDatabase: "new-api, ozon_erp, sub2api",
		Destination:    "腾讯云新加坡-MinIO / db-backup",
		FinishedAt:     &finishedAt,
		Artifacts: []model.BackupArtifact{
			{Database: "new-api", Name: "new-api_20260623.sql", Size: 1536},
			{Database: "ozon_erp", Name: "ozon_erp_20260623.sql", Size: 2 * 1024 * 1024},
			{Database: "sub2api", Name: "sub2api_20260623.sql", Size: 0},
		},
	}

	text := backupNotificationText(job, true)
	if strings.Contains(text, "任务 ID：") {
		t.Fatalf("notification text should not contain task id\n%s", text)
	}
	nameIndex := strings.Index(text, "任务名称：腾讯云新加坡 / 3 个数据库 备份")
	timeIndex := strings.Index(text, "备份时间：2026-06-23 10:15:30")
	sourceIndex := strings.Index(text, "源数据库：new-api, ozon_erp, sub2api")
	if !(nameIndex >= 0 && timeIndex > nameIndex && sourceIndex > timeIndex) {
		t.Fatalf("notification field order is incorrect\n%s", text)
	}
	for _, want := range []string{
		"DataBridge 备份成功",
		"备份时间：2026-06-23 10:15:30",
		"备份文件：",
		"- new-api：new-api_20260623.sql（1.50 KB）",
		"- ozon_erp：ozon_erp_20260623.sql（2.00 MB）",
		"- sub2api：sub2api_20260623.sql（未知大小）",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("notification text missing %q\n%s", want, text)
		}
	}
}

func TestDingTalkMarkdownUsesParagraphBreaks(t *testing.T) {
	markdown := "**DataBridge 备份成功**\n任务名称：通知测试\n备份时间：2026-06-27 22:27:37"
	payload := webhookPayloadForTarget("oapi.dingtalk.com", nil, "DataBridge 备份成功", markdown, true)
	content, ok := payload["markdown"].(map[string]string)
	if !ok {
		t.Fatalf("unexpected DingTalk payload: %#v", payload)
	}
	for _, want := range []string{"### 🟢 DataBridge 备份成功", "**任务名称：** 通知测试", "**备份时间：** 2026-06-27 22:27:37"} {
		if !strings.Contains(content["text"], want) {
			t.Fatalf("DingTalk markdown missing %q: %q", want, content["text"])
		}
	}
}

func TestFeishuNotificationCardUsesStatusTemplate(t *testing.T) {
	markdown := "**DataBridge 备份失败**\n任务名称：通知测试\n源数据库：system"
	payload := webhookPayloadForTarget("open.feishu.cn", nil, "DataBridge 备份失败", markdown, false)
	card, ok := payload["card"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected Feishu payload: %#v", payload)
	}
	header, ok := card["header"].(map[string]any)
	if !ok || header["template"] != "red" {
		t.Fatalf("unexpected Feishu header: %#v", header)
	}
	if _, exists := header["subtitle"]; exists {
		t.Fatalf("Feishu header should not contain subtitle: %#v", header)
	}
	if card["schema"] != "2.0" {
		t.Fatalf("schema = %#v", card["schema"])
	}
}

func TestSendTelegramMessage(t *testing.T) {
	var requestPath string
	var payload map[string]string
	telegramAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	}))
	defer telegramAPI.Close()

	target := `{"botToken":"123456:secret","chatId":"-100987654321"}`
	if err := sendTelegramMessage(telegramAPI.Client(), telegramAPI.URL, target, "DataBridge 测试通知"); err != nil {
		t.Fatal(err)
	}
	if requestPath != "/bot123456:secret/sendMessage" {
		t.Fatalf("path = %q", requestPath)
	}
	if payload["chat_id"] != "-100987654321" || payload["text"] != "DataBridge 测试通知" || payload["parse_mode"] != "HTML" {
		t.Fatalf("payload = %#v", payload)
	}
}

func TestBackupNotificationHTMLUsesStatusAndEscapesValues(t *testing.T) {
	job := model.BackupJob{Name: "通知 <测试>", SourceDatabase: "system", Destination: "MinIO & S3"}
	message := backupNotificationHTML(job, false)
	for _, want := range []string{"<b>🔴 DataBridge 备份失败</b>", "通知 &lt;测试&gt;", "MinIO &amp; S3"} {
		if !strings.Contains(message, want) {
			t.Fatalf("Telegram HTML missing %q: %s", want, message)
		}
	}
}

func TestPrepareTelegramRecipientKeepsSavedCredentials(t *testing.T) {
	saved := `{"botToken":"saved-token","chatId":"-1001234"}`
	input, message := prepareNotificationRecipientInput(model.NotificationRecipientInput{Name: "Telegram", Type: "telegram", Enabled: true}, saved, true)
	if message != "" {
		t.Fatal(message)
	}
	var config telegramRecipientConfig
	if err := json.Unmarshal([]byte(input.Target), &config); err != nil {
		t.Fatal(err)
	}
	if config.BotToken != "saved-token" || config.ChatID != "-1001234" {
		t.Fatalf("config = %#v", config)
	}
}

func TestBackupNotificationTextFallsBackToSingleArtifact(t *testing.T) {
	finishedAt := time.Date(2026, 6, 23, 8, 0, 0, 0, appLocation)
	job := model.BackupJob{
		ID:             "single-artifact",
		Name:           "单库备份",
		SourceDatabase: "system",
		Destination:    "local / backup",
		FinishedAt:     &finishedAt,
		ArtifactName:   "system_20260623.sql",
		ArtifactSize:   512,
	}

	text := backupNotificationText(job, true)
	if !strings.Contains(text, "- system：system_20260623.sql（512 B）") {
		t.Fatalf("notification text missing fallback artifact line\n%s", text)
	}
}

func TestCompactBackupJobKeepsArtifactVersions(t *testing.T) {
	events := make([]model.Event, maxCompactEvents+1)
	for index := range events {
		events[index] = model.Event{Level: "INFO", Message: "event"}
	}
	events[0].Message = "定时备份已触发"
	events[1].Message = "手动触发定时备份"
	events[2].Message = "备份任务完成"
	events[3].Message = "下一次备份将在 2026-06-25 03:00 执行"
	events[4].Level = "ERROR"
	events[4].Message = strings.Repeat("中", maxStoredEventMessage+10)
	job := model.BackupJob{
		ID:     "backup-1",
		Events: events,
		ArtifactVersions: []model.BackupArtifactVersion{{
			ID:        "version-1",
			Artifacts: []model.BackupArtifact{{Database: "db1", Name: "db1.sql", Size: 1024}},
		}},
		Request: model.BackupRequest{
			SourceID:        "source-1",
			SourceDatabase:  "db1",
			SourceDatabases: []string{"db1"},
		},
	}

	compact := compactBackupJob(job)
	if len(compact.ArtifactVersions) != 1 {
		t.Fatalf("compact response should keep artifact versions: %#v", compact.ArtifactVersions)
	}
	if len(compact.Events) != 5 {
		t.Fatalf("compact response should keep summary events only: %#v", compact.Events)
	}
	if len([]rune(compact.Events[4].Message)) > maxStoredEventMessage {
		t.Fatalf("compact response should truncate summary event messages: %d", len([]rune(compact.Events[4].Message)))
	}
	if compact.Request.SourceID != "" || compact.Request.SourceDatabase != "" || len(compact.Request.SourceDatabases) != 0 {
		t.Fatalf("compact response should omit request: %#v", compact.Request)
	}
	if len(job.ArtifactVersions) != 1 || job.Request.SourceID != "source-1" || len(job.Events) != maxCompactEvents+1 {
		t.Fatal("compact backup job should not mutate original value")
	}
}

func TestCompactBackupListJobKeepsArtifactVersions(t *testing.T) {
	job := model.BackupJob{
		ID: "backup-1",
		ArtifactVersions: []model.BackupArtifactVersion{{
			ID:        "version-1",
			Artifacts: []model.BackupArtifact{{Database: "db1", Name: "db1.sql", Size: 1024}},
		}},
		Request: model.BackupRequest{
			SourceID: "source-1",
		},
	}

	compact := compactBackupListJob(job)
	if len(compact.ArtifactVersions) != 1 {
		t.Fatalf("backup list response should keep artifact versions: %#v", compact.ArtifactVersions)
	}
	if compact.Request.SourceID != "" {
		t.Fatalf("backup list response should omit request: %#v", compact.Request)
	}
}

func TestCompactBackupResponseQuery(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/backups/backup-1?compact=1", nil)
	if !compactBackupResponse(req) {
		t.Fatal("compact=1 should enable compact response")
	}
	req = httptest.NewRequest(http.MethodGet, "/api/backups/backup-1?compact=true", nil)
	if !compactBackupResponse(req) {
		t.Fatal("compact=true should enable compact response")
	}
	req = httptest.NewRequest(http.MethodGet, "/api/backups/backup-1", nil)
	if compactBackupResponse(req) {
		t.Fatal("missing compact query should keep full response")
	}
}

func TestDeleteBackupArtifactEndpointIsDisabled(t *testing.T) {
	appStore, _, handler := newTestServer(t)
	now := time.Now().UTC()
	artifactPath := filepath.Join(t.TempDir(), "app.tar.gz")
	if err := os.WriteFile(artifactPath, []byte("backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	job := model.BackupJob{
		ID:              store.NewID(),
		Name:            "local backup",
		Status:          model.StatusCompleted,
		Stage:           "completed",
		StartedAt:       now,
		SourceName:      "source",
		SourceType:      "PostgreSQL",
		SourceDatabase:  "app",
		DestinationType: "local",
		Destination:     t.TempDir(),
		ArtifactName:    "app.tar.gz",
		Artifacts:       []model.BackupArtifact{{Database: "app", Name: "app.tar.gz", Path: artifactPath}},
		Events:          []model.Event{},
	}
	if err := appStore.AddBackup(job, func() {}); err != nil {
		t.Fatal(err)
	}

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodDelete, "/api/backups/"+job.ID+"/artifacts/app", nil)
	request.RemoteAddr = "127.0.0.1:12345"
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var payload map[string]string
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["code"] != "artifact_delete_disabled" {
		t.Fatalf("code = %q", payload["code"])
	}
	if _, err := os.Stat(artifactPath); err != nil {
		t.Fatalf("artifact file should not be removed: %v", err)
	}
}

func TestCleanupExpiredBackupArtifactsDeletesOldLocalVersions(t *testing.T) {
	appStore, server, _ := newTestServer(t)
	destination := t.TempDir()
	oldPath := filepath.Join(destination, "old.tar.gz")
	newPath := filepath.Join(destination, "new.tar.gz")
	if err := os.WriteFile(oldPath, []byte("old backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(newPath, []byte("new backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 6, 24, 10, 0, 0, 0, time.UTC)
	oldFinished := now.AddDate(0, 0, -2)
	newFinished := now.Add(-time.Hour)
	job := model.BackupJob{
		ID:              store.NewID(),
		Name:            "scheduled local backup",
		Status:          model.StatusScheduled,
		Stage:           "scheduled",
		StartedAt:       now.Add(-time.Hour),
		SourceName:      "source",
		SourceType:      "PostgreSQL",
		SourceDatabase:  "app",
		DestinationType: "local",
		Destination:     destination,
		ScheduleMode:    "scheduled",
		RetentionDays:   1,
		RetentionMonths: 1,
		ArtifactName:    "new.tar.gz",
		ArtifactPath:    newPath,
		Artifacts:       []model.BackupArtifact{{Database: "app", Name: "new.tar.gz", Path: newPath}},
		ArtifactVersions: []model.BackupArtifactVersion{
			{
				ID:         "new-version",
				StartedAt:  newFinished.Add(-time.Minute),
				FinishedAt: &newFinished,
				Status:     model.StatusCompleted,
				Stage:      "completed",
				Artifacts:  []model.BackupArtifact{{Database: "app", Name: "new.tar.gz", Path: newPath}},
			},
			{
				ID:         "old-version",
				StartedAt:  oldFinished.Add(-time.Minute),
				FinishedAt: &oldFinished,
				Status:     model.StatusCompleted,
				Stage:      "completed",
				Artifacts:  []model.BackupArtifact{{Database: "app", Name: "old.tar.gz", Path: oldPath}},
			},
		},
		Events: []model.Event{},
		Request: model.BackupRequest{
			DestinationType: "local",
			Destination:     destination,
			ScheduleMode:    "scheduled",
			RetentionDays:   1,
			RetentionMonths: 1,
		},
	}
	if err := appStore.AddBackup(job, func() {}); err != nil {
		t.Fatal(err)
	}

	server.cleanupExpiredBackupArtifacts(httptest.NewRequest(http.MethodGet, "/", nil).Context(), job, "new-version", now)

	if _, err := os.Stat(oldPath); !os.IsNotExist(err) {
		t.Fatalf("old artifact should be removed, stat error = %v", err)
	}
	if _, err := os.Stat(newPath); err != nil {
		t.Fatalf("new artifact should remain: %v", err)
	}
	updated, err := appStore.Backup(job.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, version := range updated.ArtifactVersions {
		switch version.ID {
		case "old-version":
			if len(version.Artifacts) != 0 {
				t.Fatalf("old version artifacts should be pruned: %#v", version.Artifacts)
			}
			if version.Error != "备份文件已按保留期限清理" {
				t.Fatalf("old version error = %q", version.Error)
			}
		case "new-version":
			if len(version.Artifacts) != 1 {
				t.Fatalf("new version artifacts should remain: %#v", version.Artifacts)
			}
		}
	}
	if _, err := backupJobForArtifactVersion(updated, "old-version"); err == nil {
		t.Fatal("old version should no longer be restorable")
	}
	foundCleanupEvent := false
	for _, event := range updated.Events {
		if strings.Contains(event.Message, "过期备份文件清理完成") &&
			strings.Contains(event.Message, "已删除 1 个") &&
			strings.Contains(event.Message, "2 天前 old.tar.gz") {
			foundCleanupEvent = true
		}
	}
	if !foundCleanupEvent {
		t.Fatalf("cleanup event not found: %#v", updated.Events)
	}
}

func TestRestoreBackupRequiresPrecheckBeforeCreatingJob(t *testing.T) {
	appStore, _, handler := newTestServer(t)
	now := time.Now().UTC()
	artifactPath := filepath.Join(t.TempDir(), "app.tar.gz")
	if err := os.WriteFile(artifactPath, []byte("backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	sourceJob := model.BackupJob{
		ID:              store.NewID(),
		Name:            "source backup",
		Status:          model.StatusCompleted,
		Stage:           "completed",
		StartedAt:       now,
		SourceName:      "source",
		SourceType:      "PostgreSQL",
		SourceDatabase:  "app",
		DestinationType: "local",
		Destination:     t.TempDir(),
		ArtifactName:    "app.tar.gz",
		Artifacts:       []model.BackupArtifact{{Database: "app", Name: "app.tar.gz", Path: artifactPath}},
		Events:          []model.Event{},
	}
	if err := appStore.AddBackup(sourceJob, func() {}); err != nil {
		t.Fatal(err)
	}
	target, err := appStore.AddConnection(model.ConnectionInput{
		Type: "PostgreSQL", Name: "target", Host: "127.0.0.1", Port: 1,
		Username: "postgres", Password: "postgres", SSLMode: "disable",
	}, 0, "test")
	if err != nil {
		t.Fatal(err)
	}

	body := bytes.NewBufferString(`{"targetId":"` + target.ID + `","targetDatabase":"app_restore","strategy":"append"}`)
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/backups/"+sourceJob.ID+"/restore", body)
	request.RemoteAddr = "127.0.0.1:12345"
	handler.ServeHTTP(response, request)

	if response.Code != http.StatusBadRequest && response.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	jobs, err := appStore.Backups()
	if err != nil {
		t.Fatal(err)
	}
	for _, job := range jobs {
		if job.DestinationType == "restore" {
			t.Fatalf("restore job should not be created when precheck fails: %#v", job)
		}
	}
}

func newTestServer(t *testing.T) (*store.Store, *Server, http.Handler) {
	t.Helper()
	directory := t.TempDir()
	appStore, err := store.New(filepath.Join(directory, "databridge.db"), filepath.Join(directory, "databridge.key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := appStore.Close(); err != nil {
			t.Fatal(err)
		}
	})
	return appStore, &Server{store: appStore}, New(appStore)
}
