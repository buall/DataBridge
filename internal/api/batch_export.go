package api

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"databridge/internal/model"
)

func (s *Server) exportExcel(w http.ResponseWriter, r *http.Request) {
	scope := r.URL.Query().Get("scope")
	var exportRows [][]string
	var sheetPath string
	var additionalRows [][]string
	var additionalSheetPath string
	var filename string
	switch scope {
	case "connections":
		connections, err := s.store.Connections()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "export_failed", "读取数据连接失败")
			return
		}
		exportRows = connectionExportRows(connections)[1:]
		sheetPath = "xl/worksheets/sheet1.xml"
		filename = "databridge-connections-" + time.Now().Format("20060102-150405") + ".xlsx"
	case "schedules":
		connections, err := s.store.Connections()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "export_failed", "读取数据连接失败")
			return
		}
		jobs, err := s.store.Backups()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "export_failed", "读取备份任务失败")
			return
		}
		storages, err := s.store.Storages()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "export_failed", "读取存储配置失败")
			return
		}
		recipients, err := s.store.NotificationRecipients()
		if err != nil {
			writeError(w, http.StatusInternalServerError, "export_failed", "读取通知接收人失败")
			return
		}
		exportRows = scheduledConnectionExportRows(connections, jobs)[1:]
		sheetPath = "xl/worksheets/sheet1.xml"
		additionalRows = scheduleExportRows(jobs, storages, recipients)[1:]
		additionalSheetPath = "xl/worksheets/sheet2.xml"
		filename = "databridge-schedules-" + time.Now().Format("20060102-150405") + ".xlsx"
	default:
		writeError(w, http.StatusBadRequest, "invalid_export_scope", "导出类型仅支持 connections 或 schedules")
		return
	}
	template, err := readBatchImportTemplate()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "export_failed", "读取 Excel 导入模板失败")
		return
	}
	data, err := fillExportTemplate(template, sheetPath, exportRows)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "export_failed", "生成 Excel 文件失败")
		return
	}
	if additionalSheetPath != "" {
		data, err = fillExportTemplate(data, additionalSheetPath, additionalRows)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "export_failed", "生成 Excel 文件失败")
			return
		}
	}
	w.Header().Set("Content-Type", "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func readBatchImportTemplate() ([]byte, error) {
	for _, name := range []string{"dist/databridge-batch-import-template.xlsx", "public/databridge-batch-import-template.xlsx", "../../public/databridge-batch-import-template.xlsx"} {
		data, err := os.ReadFile(name)
		if err == nil {
			return data, nil
		}
	}
	return nil, os.ErrNotExist
}

func fillExportTemplate(template []byte, sheetPath string, rows [][]string) ([]byte, error) {
	reader, err := zip.NewReader(bytes.NewReader(template), int64(len(template)))
	if err != nil {
		return nil, err
	}
	var output bytes.Buffer
	writer := zip.NewWriter(&output)
	found := false
	for _, source := range reader.File {
		input, err := source.Open()
		if err != nil {
			return nil, err
		}
		data, readErr := io.ReadAll(input)
		_ = input.Close()
		if readErr != nil {
			return nil, readErr
		}
		if source.Name == sheetPath {
			data = []byte(fillTemplateWorksheet(string(data), rows))
			found = true
		}
		header := source.FileHeader
		header.CompressedSize64 = 0
		header.UncompressedSize64 = 0
		header.CRC32 = 0
		target, err := writer.CreateHeader(&header)
		if err != nil {
			return nil, err
		}
		if _, err := target.Write(data); err != nil {
			return nil, err
		}
	}
	if !found {
		return nil, fmt.Errorf("template worksheet %s not found", sheetPath)
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func fillTemplateWorksheet(sheet string, rows [][]string) string {
	const firstDataRow = 5
	for rowOffset, row := range rows {
		rowNumber := firstDataRow + rowOffset
		if rowNumber <= 104 {
			for columnIndex, value := range row {
				cellRef := xlsxColumnName(columnIndex) + strconv.Itoa(rowNumber)
				pattern := regexp.MustCompile(`<x:c r="` + regexp.QuoteMeta(cellRef) + `"([^>]*)\s*/>`)
				sheet = pattern.ReplaceAllStringFunc(sheet, func(cell string) string {
					return strings.TrimSuffix(strings.TrimSpace(cell), "/>") + ` t="inlineStr"><x:is><x:t xml:space="preserve">` + xmlEscape(value) + `</x:t></x:is></x:c>`
				})
			}
			continue
		}
		var extra strings.Builder
		extra.WriteString(fmt.Sprintf(`<x:row r="%d" ht="24" customHeight="1">`, rowNumber))
		for columnIndex, value := range row {
			style := "37"
			if columnIndex == len(row)-1 {
				style = "38"
			}
			extra.WriteString(fmt.Sprintf(`<x:c r="%s%d" s="%s" t="inlineStr"><x:is><x:t xml:space="preserve">%s</x:t></x:is></x:c>`, xlsxColumnName(columnIndex), rowNumber, style, xmlEscape(value)))
		}
		extra.WriteString(`</x:row>`)
		sheet = strings.Replace(sheet, `</x:sheetData>`, extra.String()+`</x:sheetData>`, 1)
	}
	return sheet
}

func connectionExportRows(items []model.Connection) [][]string {
	rows := [][]string{{"连接标识", "连接名称", "数据库类型", "主机地址", "端口", "用户名", "密码", "SSL模式", "备注"}}
	for _, item := range items {
		rows = append(rows, []string{item.ID, item.Name, item.Type, item.Host, strconv.Itoa(item.Port), item.Username, item.Password, item.SSLMode, item.Note})
	}
	return rows
}

func scheduledConnectionExportRows(connections []model.Connection, jobs []model.BackupJob) [][]string {
	referenced := make(map[string]bool)
	for _, job := range jobs {
		if job.ScheduleMode == "scheduled" || job.Status == model.StatusScheduled {
			referenced[job.SourceID] = true
		}
	}
	items := make([]model.Connection, 0, len(referenced))
	for _, connection := range connections {
		if referenced[connection.ID] {
			items = append(items, connection)
		}
	}
	return connectionExportRows(items)
}

func scheduleExportRows(jobs []model.BackupJob, storages []model.RemoteStorage, recipients []model.NotificationRecipient) [][]string {
	rows := [][]string{{"任务名称", "连接标识", "数据库列表", "存储名称", "频率", "执行时间", "每周星期", "每月日期", "保留天数", "通知接收人"}}
	storageNames := make(map[string]string, len(storages))
	for _, item := range storages {
		storageNames[item.ID] = item.Name
	}
	recipientNames := make(map[string]string, len(recipients))
	for _, item := range recipients {
		recipientNames[item.ID] = item.Name
	}
	for _, job := range jobs {
		if job.ScheduleMode != "scheduled" && job.Status != model.StatusScheduled {
			continue
		}
		databases := job.Request.SourceDatabases
		if len(databases) == 0 {
			databases = splitImportList(job.SourceDatabase)
		}
		storageID := job.PrimaryDestinationID
		if storageID == "" && len(job.BackupTargets) > 0 {
			storageID = job.BackupTargets[0].StorageID
		}
		ids := job.NotificationRecipientIDs
		if len(ids) == 0 {
			ids = job.Notification.RecipientIDs
		}
		names := make([]string, 0, len(ids))
		for _, id := range ids {
			if name := recipientNames[id]; name != "" {
				names = append(names, name)
			}
		}
		rows = append(rows, []string{
			job.Name, job.SourceID, strings.Join(databases, ", "), storageNames[storageID],
			importFrequencyLabel(job.ScheduleFrequency), job.ScheduleTime, exportWeekday(job.ScheduleWeekday),
			exportPositiveInt(job.ScheduleMonthDay), strconv.Itoa(job.RetentionDays), strings.Join(names, ", "),
		})
	}
	return rows
}

func exportWeekday(day int) string {
	labels := []string{"", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六", "星期日"}
	if day >= 1 && day <= 7 {
		return labels[day]
	}
	return ""
}

func exportPositiveInt(value int) string {
	if value > 0 {
		return strconv.Itoa(value)
	}
	return ""
}

type exportSheet struct {
	Name string
	Rows [][]string
}

func buildExportXLSX(sheets []exportSheet) ([]byte, error) {
	var output bytes.Buffer
	archive := zip.NewWriter(&output)
	write := func(name, body string) error {
		file, err := archive.Create(name)
		if err != nil {
			return err
		}
		_, err = file.Write([]byte(body))
		return err
	}
	contentTypes := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>`
	workbook := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`
	rels := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`
	for i, sheet := range sheets {
		index := i + 1
		contentTypes += fmt.Sprintf(`<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, index)
		workbook += fmt.Sprintf(`<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, xmlEscape(sheet.Name), index, index)
		rels += fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, index, index)
		if err := write(fmt.Sprintf("xl/worksheets/sheet%d.xml", index), exportWorksheetXML(sheet.Rows)); err != nil {
			return nil, err
		}
	}
	contentTypes += `</Types>`
	workbook += `</sheets></workbook>`
	rels += `</Relationships>`
	files := map[string]string{
		"[Content_Types].xml": contentTypes,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`,
		"xl/workbook.xml":     workbook, "xl/_rels/workbook.xml.rels": rels,
	}
	for name, body := range files {
		if err := write(name, body); err != nil {
			return nil, err
		}
	}
	if err := archive.Close(); err != nil {
		return nil, err
	}
	return output.Bytes(), nil
}

func exportWorksheetXML(rows [][]string) string {
	var body strings.Builder
	body.WriteString(`<?xml version="1.0" encoding="UTF-8" standalone="yes"?><worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for rowIndex, row := range rows {
		body.WriteString(fmt.Sprintf(`<row r="%d">`, rowIndex+1))
		for columnIndex, value := range row {
			body.WriteString(fmt.Sprintf(`<c r="%s%d" t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, xlsxColumnName(columnIndex), rowIndex+1, xmlEscape(value)))
		}
		body.WriteString(`</row>`)
	}
	body.WriteString(`</sheetData></worksheet>`)
	return body.String()
}

func xlsxColumnName(index int) string {
	name := ""
	for index >= 0 {
		name = string(rune('A'+index%26)) + name
		index = index/26 - 1
	}
	return name
}

func xmlEscape(value string) string {
	var escaped bytes.Buffer
	_ = xml.EscapeText(&escaped, []byte(value))
	return escaped.String()
}
