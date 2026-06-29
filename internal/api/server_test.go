package api

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"databridge/internal/model"
	"databridge/internal/store"
)

func TestRemoteStorageErrorsUseStorageSpecificMessages(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{name: "timeout", err: errors.New("dial tcp 192.168.100.150:32647: i/o timeout"), want: "连接对象存储超时，请确认 Endpoint、网络连通性、防火墙规则和对象存储服务状态"},
		{name: "refused", err: errors.New("dial tcp 192.168.100.150:32647: connect: connection refused"), want: "无法连接到对象存储 Endpoint，请确认地址、端口和网络访问策略是否正确"},
		{name: "dns", err: errors.New("lookup minio.internal: no such host"), want: "无法解析对象存储 Endpoint，请确认域名或 IP 地址是否正确"},
		{name: "auth", err: errors.New("AccessDenied: access denied"), want: "对象存储认证失败，请确认 Access Key、Secret Key、Region 和 Bucket 权限是否正确"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := userFacingErrorMessage(errorContextRemoteStorage, test.err); got != test.want {
				t.Fatalf("message = %q, want %q", got, test.want)
			}
		})
	}
}

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

func TestFailedScheduledBackupRemainsEligibleForNextRun(t *testing.T) {
	next := time.Now().Add(time.Hour)
	job := model.BackupJob{
		Status:       model.StatusFailed,
		ScheduleMode: "scheduled",
		NextRunAt:    &next,
		Request: model.BackupRequest{
			ScheduleMode:      "scheduled",
			ScheduleFrequency: "daily",
			ScheduleTime:      "02:00:00",
		},
	}

	request, ok := scheduleRequestForBackup(job)
	if !ok {
		t.Fatal("failed execution must not disable its scheduled backup plan")
	}
	if request.ScheduleMode != "scheduled" {
		t.Fatalf("schedule mode = %q", request.ScheduleMode)
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

func TestHydrateBackupTargetsMarksPrimaryAndSecondaries(t *testing.T) {
	appStore, server, _ := newTestServer(t)
	primary, err := appStore.AddStorage(model.StorageInput{Type: "object", Name: "primary", Provider: "MinIO", Endpoint: "https://s3.example.com", Bucket: "main", AccessKey: "ak", SecretKey: "sk", Verified: true})
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := appStore.AddStorage(model.StorageInput{Type: "local", Name: "secondary", Provider: "Local", Path: t.TempDir(), Verified: true})
	if err != nil {
		t.Fatal(err)
	}
	request := model.BackupRequest{
		DestinationType:      "multi",
		Destination:          primary.ID,
		PrimaryDestinationID: primary.ID,
		BackupTargets: []model.BackupTarget{
			{StorageID: secondary.ID, Role: "secondary", Order: 2},
			{StorageID: primary.ID, Role: "primary", Order: 0},
		},
	}

	storages, err := server.hydrateBackupTargets(&request)
	if err != nil {
		t.Fatal(err)
	}
	if request.DestinationType != "multi" || request.Destination != primary.ID || request.PrimaryDestinationID != primary.ID {
		t.Fatalf("request destination not normalized: %#v", request)
	}
	if len(storages) != 2 || storages[0].ID != primary.ID || storages[1].ID != secondary.ID {
		t.Fatalf("storages order = %#v", storages)
	}
	if len(request.BackupTargets) != 2 || request.BackupTargets[0].Role != "primary" || request.BackupTargets[1].Role != "secondary" {
		t.Fatalf("targets = %#v", request.BackupTargets)
	}
}

func TestMergeBackupArtifactKeepsCopiesInPrimaryOrder(t *testing.T) {
	current := model.BackupArtifact{Database: "app", Name: "old.tar.gz"}
	next := model.BackupArtifact{Database: "app", Name: "new.tar.gz", Copies: []model.BackupArtifactCopy{
		{StorageID: "secondary", StorageName: "secondary", Role: "secondary", Order: 2, Type: "local", Name: "new.tar.gz", Path: "/backup/new.tar.gz", Status: "completed"},
	}}
	merged := mergeBackupArtifact(current, next)
	merged = mergeBackupArtifact(merged, model.BackupArtifact{Database: "app", Copies: []model.BackupArtifactCopy{
		{StorageID: "primary", StorageName: "primary", Role: "primary", Order: 0, Type: "object", Name: "new.tar.gz", Status: "completed"},
	}})

	if merged.Name != "new.tar.gz" {
		t.Fatalf("name = %q", merged.Name)
	}
	if len(merged.Copies) != 2 || merged.Copies[0].StorageID != "primary" || merged.Copies[1].StorageID != "secondary" {
		t.Fatalf("copies = %#v", merged.Copies)
	}
}

func TestRestoreCopiesPreferPrimaryAndSkipFailedCopies(t *testing.T) {
	appStore, server, _ := newTestServer(t)
	primary, err := appStore.AddStorage(model.StorageInput{Type: "object", Name: "primary", Provider: "MinIO", Endpoint: "https://s3.example.com", Bucket: "main", AccessKey: "ak", SecretKey: "sk", Verified: true})
	if err != nil {
		t.Fatal(err)
	}
	secondary, err := appStore.AddStorage(model.StorageInput{Type: "object", Name: "secondary", Provider: "MinIO", Endpoint: "https://s3.backup.example.com", Bucket: "backup", AccessKey: "ak", SecretKey: "sk", Verified: true})
	if err != nil {
		t.Fatal(err)
	}
	job := model.BackupJob{DestinationType: "multi"}
	artifact := model.BackupArtifact{Database: "app", Copies: []model.BackupArtifactCopy{
		{StorageID: secondary.ID, StorageName: secondary.Name, Role: "secondary", Order: 2, Type: "object", Name: "app.tar.gz", Status: "completed"},
		{StorageID: primary.ID, StorageName: primary.Name, Role: "primary", Order: 0, Type: "object", Name: "app.tar.gz", Status: "completed"},
		{StorageID: "failed", Role: "secondary", Order: 3, Type: "object", Name: "app.tar.gz", Status: "failed"},
	}}

	copies := server.restoreCopiesForArtifact(job, artifact)
	if len(copies) != 2 {
		t.Fatalf("copies = %#v", copies)
	}
	if copies[0].Storage == nil || copies[0].Storage.ID != primary.ID {
		t.Fatalf("primary copy should be first: %#v", copies[0])
	}
	if copies[1].Storage == nil || copies[1].Storage.ID != secondary.ID {
		t.Fatalf("secondary copy should be second: %#v", copies[1])
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

func TestSendSMTPMailUsesImplicitTLSForPort465(t *testing.T) {
	t.Cleanup(resetSMTPTestHooks)
	sendMailCalled := false
	smtpSendMail = func(string, smtp.Auth, string, []string, []byte) error {
		sendMailCalled = true
		return nil
	}
	dialedNetwork := ""
	dialedAddress := ""
	var tlsConfig *tls.Config
	smtpTLSDial = func(network, addr string, config *tls.Config) (*tls.Conn, error) {
		dialedNetwork = network
		dialedAddress = addr
		tlsConfig = config
		return nil, errors.New("stop after TLS dial")
	}

	err := sendSMTPMail("smtp.resend.com:465", nil, "DataBridge <no-reply@wideclaw.space>", []string{"to@example.com"}, []byte("Subject: test\r\n\r\nbody"))
	if err == nil || !strings.Contains(err.Error(), "连接 SMTP TLS 服务失败") {
		t.Fatalf("error = %v", err)
	}
	if sendMailCalled {
		t.Fatal("smtp.SendMail must not be used for implicit TLS port 465")
	}
	if dialedNetwork != "tcp" || dialedAddress != "smtp.resend.com:465" {
		t.Fatalf("dial = %s %s", dialedNetwork, dialedAddress)
	}
	if tlsConfig == nil || tlsConfig.ServerName != "smtp.resend.com" || tlsConfig.MinVersion != tls.VersionTLS12 {
		t.Fatalf("unexpected tls config: %#v", tlsConfig)
	}
}

func TestSendSMTPMailUsesEnvelopeAddressForStandardPorts(t *testing.T) {
	t.Cleanup(resetSMTPTestHooks)
	var gotAddress, gotFrom string
	var gotTo []string
	var gotMessage string
	smtpSendMail = func(addr string, _ smtp.Auth, from string, to []string, msg []byte) error {
		gotAddress = addr
		gotFrom = from
		gotTo = append([]string(nil), to...)
		gotMessage = string(msg)
		return nil
	}
	smtpTLSDial = func(string, string, *tls.Config) (*tls.Conn, error) {
		t.Fatal("TLS dial should not be used for port 587")
		return nil, nil
	}

	err := sendSMTPMail("smtp.resend.com:587", nil, "DataBridge <no-reply@wideclaw.space>", []string{"to@example.com"}, []byte("Subject: test\r\n\r\nbody"))
	if err != nil {
		t.Fatal(err)
	}
	if gotAddress != "smtp.resend.com:587" || gotFrom != "no-reply@wideclaw.space" {
		t.Fatalf("addr/from = %q/%q", gotAddress, gotFrom)
	}
	if len(gotTo) != 1 || gotTo[0] != "to@example.com" {
		t.Fatalf("to = %#v", gotTo)
	}
	if gotMessage == "" {
		t.Fatal("message should be passed through")
	}
}

func TestSMTPEnvelopeAddressRejectsInvalidSender(t *testing.T) {
	if _, err := smtpEnvelopeAddress("DataBridge no-reply@wideclaw.space"); err == nil {
		t.Fatal("expected invalid sender error")
	}
}

func resetSMTPTestHooks() {
	smtpSendMail = smtp.SendMail
	smtpTLSDial = tls.Dial
	smtpTLSConfig = func(host string) *tls.Config {
		return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
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
	body, ok := card["body"].(map[string]any)
	if !ok {
		t.Fatalf("unexpected Feishu body: %#v", card["body"])
	}
	elements, ok := body["elements"].([]map[string]any)
	if !ok || len(elements) != 1 {
		t.Fatalf("unexpected Feishu elements: %#v", body["elements"])
	}
	content, _ := elements[0]["content"].(string)
	if !strings.Contains(content, "**任务名称：** 通知测试\n\n**源数据库：** system") {
		t.Fatalf("Feishu card fields should use paragraph breaks: %q", content)
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
