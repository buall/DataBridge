package api

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"databridge/internal/model"
)

func TestParseXLSXReadsGeneratedImportTemplate(t *testing.T) {
	data, err := os.ReadFile("testdata/batch-import-valid.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	workbook, err := parseXLSX(data)
	if err != nil {
		t.Fatal(err)
	}
	connections := workbook.Sheets["数据库连接"]
	if got, want := connections[4][0], "test-pg"; got != want {
		t.Fatalf("connection key = %q, want %q", got, want)
	}
	if got, want := connections[4][4], "5432"; got != want {
		t.Fatalf("port = %q, want %q", got, want)
	}
	schedules := workbook.Sheets["定时备份任务"]
	if got, want := schedules[4][4], "每天"; got != want {
		t.Fatalf("frequency = %q, want %q", got, want)
	}
	if got, want := schedules[4][2], "all"; got != want {
		t.Fatalf("databases = %q, want %q", got, want)
	}
}

func TestPreviewBatchImportValidatesGeneratedWorkbook(t *testing.T) {
	appStore, server, _ := newTestServer(t)
	if _, err := appStore.AddStorage(model.StorageInput{
		Type: "local", Name: "测试存储", Provider: "local", Path: t.TempDir(), Verified: true,
	}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("testdata/batch-import-valid.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "batch-import-valid.xlsx")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/imports/excel/preview", body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response := httptest.NewRecorder()
	server.previewBatchImport(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	var preview batchImportPreview
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if !preview.Valid || preview.Summary.Connections != 1 || preview.Summary.Schedules != 1 || preview.Summary.Errors != 0 {
		t.Fatalf("unexpected preview: %#v", preview)
	}
	if got, want := preview.Schedules[0].Time, "02:00:00"; got != want {
		t.Fatalf("schedule time = %q, want %q", got, want)
	}
	if got, want := preview.Schedules[0].Databases, []string{"all"}; len(got) != len(want) || got[0] != want[0] {
		t.Fatalf("databases = %#v, want %#v", got, want)
	}
}

func TestImportsAllDatabasesRequiresStandaloneToken(t *testing.T) {
	if !importsAllDatabases([]string{"ALL"}) {
		t.Fatal("ALL should select every database")
	}
	if importsAllDatabases([]string{"all", "app"}) {
		t.Fatal("all must not be mixed with explicit database names")
	}
	if !containsAllDatabaseToken([]string{"app", "All"}) {
		t.Fatal("mixed list should still detect the all token")
	}
}
