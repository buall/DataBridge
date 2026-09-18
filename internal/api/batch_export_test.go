package api

import (
	"archive/zip"
	"bytes"
	"io"
	"mime"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"databridge/internal/model"
	"databridge/internal/store"
)

func TestConnectionExportXLSXCanBeParsed(t *testing.T) {
	rows := connectionExportRows([]model.Connection{{
		ID: "connection-1", Name: "生产库 & 报表", Type: "postgresql", Host: "db.example.com",
		Port: 5432, Username: "reader", Password: "p<ass>&word", SSLMode: "require", Note: "夜间任务",
	}})
	data, err := buildExportXLSX([]exportSheet{{Name: "数据库连接", Rows: rows}})
	if err != nil {
		t.Fatalf("build export: %v", err)
	}
	workbook, err := parseXLSX(data)
	if err != nil {
		t.Fatalf("parse exported workbook: %v", err)
	}
	got := workbook.Sheets["数据库连接"]
	if len(got) != 2 || got[1][1] != "生产库 & 报表" || got[1][6] != "p<ass>&word" {
		t.Fatalf("unexpected exported rows: %#v", got)
	}
}

func TestExportUsesBatchImportTemplate(t *testing.T) {
	template, err := readBatchImportTemplate()
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	data, err := fillExportTemplate(template, "xl/worksheets/sheet1.xml", [][]string{{
		"connection-1", "生产库", "postgresql", "db.example.com", "5432", "reader", "secret", "require", "备注",
	}})
	if err != nil {
		t.Fatalf("fill template: %v", err)
	}
	workbook, err := parseXLSX(data)
	if err != nil {
		t.Fatalf("parse export: %v", err)
	}
	if len(workbook.Sheets) != 3 {
		t.Fatalf("expected all three template sheets, got %d", len(workbook.Sheets))
	}
	connections := workbook.Sheets["数据库连接"]
	if len(connections) < 5 || connections[4][1] != "生产库" || connections[4][6] != "secret" {
		t.Fatalf("unexpected connection sheet: %#v", connections)
	}
	if _, ok := workbook.Sheets["填写说明"]; !ok {
		t.Fatal("template instructions sheet was not preserved")
	}
}

func TestScheduleTemplateExportIncludesConnectionsAndSchedules(t *testing.T) {
	template, err := readBatchImportTemplate()
	if err != nil {
		t.Fatalf("read template: %v", err)
	}
	data, err := fillExportTemplate(template, "xl/worksheets/sheet1.xml", [][]string{{"connection-1", "生产库", "postgresql", "db", "5432", "user", "pass", "require", ""}})
	if err == nil {
		data, err = fillExportTemplate(data, "xl/worksheets/sheet2.xml", [][]string{{"每日备份", "connection-1", "app", "本地归档", "每天", "02:30", "", "", "7", ""}})
	}
	if err != nil {
		t.Fatalf("fill schedule export: %v", err)
	}
	workbook, err := parseXLSX(data)
	if err != nil {
		t.Fatalf("parse schedule export: %v", err)
	}
	if workbook.Sheets["数据库连接"][4][0] != "connection-1" || workbook.Sheets["定时备份任务"][4][0] != "每日备份" {
		t.Fatalf("dependent template sheets were not populated")
	}
}

func TestScheduleExportOnlyIncludesScheduledJobs(t *testing.T) {
	rows := scheduleExportRows([]model.BackupJob{
		{Name: "每日备份", SourceID: "connection-1", SourceDatabase: "app", ScheduleMode: "scheduled", ScheduleFrequency: "daily", ScheduleTime: "02:30", RetentionDays: 7, PrimaryDestinationID: "storage-1"},
		{Name: "手动备份", SourceID: "connection-1", SourceDatabase: "app", ScheduleMode: "once"},
	}, []model.RemoteStorage{{ID: "storage-1", Name: "本地归档"}}, nil)
	if len(rows) != 2 {
		t.Fatalf("expected header and one schedule, got %d rows", len(rows))
	}
	if rows[1][0] != "每日备份" || rows[1][3] != "本地归档" || rows[1][4] != "每天" {
		t.Fatalf("unexpected schedule row: %#v", rows[1])
	}
}

func TestBackupRecordExportRowsIncludeEveryExecution(t *testing.T) {
	started := time.Date(2026, 9, 17, 2, 30, 0, 0, appLocation)
	finished := started.Add(2*time.Minute + 3*time.Second)
	rows := backupRecordExportRows(model.BackupJob{
		ID: "backup-1", Name: "每日生产备份", Destination: "生产归档",
		ArtifactVersions: []model.BackupArtifactVersion{
			{ID: "failed", StartedAt: started.Add(-time.Hour), Status: model.StatusFailed, Error: "对象存储不可用"},
			{ID: "completed", StartedAt: started, FinishedAt: &finished, Status: model.StatusCompleted, Artifacts: []model.BackupArtifact{
				{Database: "orders", Name: "orders.tar.gz", Size: 1024},
				{Database: "users", Copies: []model.BackupArtifactCopy{{Name: "users.tar.gz", Size: 2048}}},
			}},
		},
	})
	if len(rows) != 3 {
		t.Fatalf("record rows = %d, want 3: %#v", len(rows), rows)
	}
	if got, want := rows[1][2], "completed"; got != want {
		t.Fatalf("latest record id = %q, want %q", got, want)
	}
	if rows[1][5] != "2分3秒" || rows[1][6] != "成功" || rows[1][8] != "3.0 KB" {
		t.Fatalf("unexpected completed record: %#v", rows[1])
	}
	if !strings.Contains(rows[1][7], "orders: orders.tar.gz") || !strings.Contains(rows[1][7], "users: users.tar.gz") {
		t.Fatalf("artifacts missing from record: %#v", rows[1])
	}
	if rows[2][6] != "失败" || rows[2][10] != "对象存储不可用" {
		t.Fatalf("unexpected failed record: %#v", rows[2])
	}
}

func TestBackupRecordExportPreservesStylesPastTemplateRows(t *testing.T) {
	template, err := readBackupRecordTemplate()
	if err != nil {
		t.Fatalf("read backup record template: %v", err)
	}
	records := make([][]string, 101)
	for index := range records {
		records[index] = []string{"task", "task-id", "record-id", "2026-09-17 04:00:27", "", "52秒", "成功", "artifact.tar.gz", "1.0 MB", "destination", ""}
	}
	data, err := fillExportTemplate(template, "xl/worksheets/sheet1.xml", records)
	if err != nil {
		t.Fatalf("fill template: %v", err)
	}
	sheetXML := xlsxEntry(t, data, "xl/worksheets/sheet1.xml")
	for _, column := range []string{"A", "D", "H", "I", "K"} {
		if got, want := templateCellStyle(t, sheetXML, column+"104"), templateCellStyle(t, sheetXML, column+"105"); got != want {
			t.Errorf("%s105 style = %q, want template %s104 style %q", column, want, column, got)
		}
	}
}

func templateCellStyle(t *testing.T, sheetXML, cellRef string) string {
	t.Helper()
	pattern := regexp.MustCompile(`<x:c r="` + regexp.QuoteMeta(cellRef) + `"[^>]*\bs="([0-9]+)"`)
	match := pattern.FindStringSubmatch(sheetXML)
	if len(match) != 2 {
		t.Fatalf("style not found for %s", cellRef)
	}
	return match[1]
}

func TestExportBackupRecordsForScheduledJob(t *testing.T) {
	appStore, server, _ := newTestServer(t)
	started := time.Date(2026, 9, 17, 2, 30, 0, 0, appLocation)
	finished := started.Add(45 * time.Second)
	job := model.BackupJob{
		ID: store.NewID(), Name: "每日生产备份", Status: model.StatusScheduled, ScheduleMode: "scheduled", Destination: "生产归档",
		ArtifactVersions: []model.BackupArtifactVersion{{
			ID: "record-1", StartedAt: started, FinishedAt: &finished, Status: model.StatusCompleted,
			Artifacts: []model.BackupArtifact{{Database: "orders", Name: "orders.tar.gz", Size: 1024}},
		}},
	}
	if err := appStore.AddBackup(job, nil); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/backups/"+job.ID+"/records/export", nil)
	request.SetPathValue("id", job.ID)
	response := httptest.NewRecorder()
	server.exportBackupRecords(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	mediaType, params, err := mime.ParseMediaType(response.Header().Get("Content-Disposition"))
	if err != nil || mediaType != "attachment" || !strings.Contains(params["filename"], "每日生产备份-备份记录-") {
		t.Fatalf("unexpected content disposition: %q", response.Header().Get("Content-Disposition"))
	}
	workbook, err := parseXLSX(response.Body.Bytes())
	if err != nil {
		t.Fatalf("parse export: %v", err)
	}
	rows := workbook.Sheets["备份记录"]
	if len(rows) < 5 || rows[0][0] != "DataBridge 备份记录" || rows[3][0] != "任务名称" || rows[4][0] != "每日生产备份" || rows[4][6] != "成功" {
		t.Fatalf("unexpected exported rows: %#v", rows)
	}
	sheetXML := xlsxEntry(t, response.Body.Bytes(), "xl/worksheets/sheet1.xml")
	stylesXML := xlsxEntry(t, response.Body.Bytes(), "xl/styles.xml")
	if !strings.Contains(sheetXML, "conditionalFormatting") || !strings.Contains(sheetXML, "width=\"42\"") || !strings.Contains(sheetXML, "ht=\"34\"") || !strings.Contains(stylesXML, "FFEAF8EF") || !strings.Contains(stylesXML, "FFFDECEC") || strings.Contains(stylesXML, "FF15803D") || strings.Contains(stylesXML, "FFB91C1C") {
		t.Fatal("backup record export should preserve template formatting")
	}
}

func TestExportBackupRecordsRejectsNonScheduledJob(t *testing.T) {
	appStore, server, _ := newTestServer(t)
	job := model.BackupJob{ID: store.NewID(), Name: "手动备份", Status: model.StatusCompleted, ScheduleMode: "once"}
	if err := appStore.AddBackup(job, nil); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/backups/"+job.ID+"/records/export", nil)
	request.SetPathValue("id", job.ID)
	response := httptest.NewRecorder()
	server.exportBackupRecords(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "仅定时备份任务支持") {
		t.Fatalf("unexpected response: status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestBackupRecordExportFileNameUsesSafeTaskName(t *testing.T) {
	filename := backupRecordExportFileName(`生产/订单:每日?备份`)
	if !strings.Contains(filename, "生产_订单_每日_备份-备份记录-") || !strings.HasSuffix(filename, ".xlsx") {
		t.Fatalf("unexpected filename: %q", filename)
	}
}

func xlsxEntry(t *testing.T, data []byte, name string) string {
	t.Helper()
	reader, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("open xlsx: %v", err)
	}
	for _, file := range reader.File {
		if file.Name != name {
			continue
		}
		input, err := file.Open()
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		contents, err := io.ReadAll(input)
		_ = input.Close()
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return string(contents)
	}
	t.Fatalf("xlsx entry not found: %s", name)
	return ""
}
