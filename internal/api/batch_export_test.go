package api

import (
	"testing"

	"databridge/internal/model"
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
