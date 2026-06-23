package api

import (
	"strings"
	"testing"
	"time"

	"databridge/internal/model"
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
