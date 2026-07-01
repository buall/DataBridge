package api

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"databridge/internal/model"
	"databridge/internal/store"
)

const maxBatchImportFileSize = 6 << 20

var importConnectionKeyPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)

type batchImportIssue struct {
	Sheet   string `json:"sheet"`
	Row     int    `json:"row"`
	Field   string `json:"field,omitempty"`
	Message string `json:"message"`
}

type batchImportConnectionPreview struct {
	Row      int      `json:"row"`
	Key      string   `json:"key"`
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	Username string   `json:"username"`
	SSLMode  string   `json:"sslMode"`
	Note     string   `json:"note,omitempty"`
	Errors   []string `json:"errors"`
}

type batchImportSchedulePreview struct {
	Row            int      `json:"row"`
	Name           string   `json:"name"`
	ConnectionKey  string   `json:"connectionKey"`
	Databases      []string `json:"databases"`
	StorageName    string   `json:"storageName"`
	Frequency      string   `json:"frequency"`
	Time           string   `json:"time"`
	Weekday        string   `json:"weekday,omitempty"`
	MonthDay       int      `json:"monthDay,omitempty"`
	RetentionDays  int      `json:"retentionDays"`
	RecipientNames []string `json:"recipientNames"`
	Errors         []string `json:"errors"`
}

type batchImportSummary struct {
	Connections int `json:"connections"`
	Schedules   int `json:"schedules"`
	Errors      int `json:"errors"`
}

type batchImportPreview struct {
	FileName    string                         `json:"fileName"`
	Valid       bool                           `json:"valid"`
	Summary     batchImportSummary             `json:"summary"`
	Connections []batchImportConnectionPreview `json:"connections"`
	Schedules   []batchImportSchedulePreview   `json:"schedules"`
	Issues      []batchImportIssue             `json:"issues"`
}

type batchImportResult struct {
	Success     bool               `json:"success"`
	Preview     batchImportPreview `json:"preview"`
	Connections []model.Connection `json:"connections"`
	Backups     []model.BackupJob  `json:"backups"`
}

type batchImportConnection struct {
	Row   int
	Key   string
	Input model.ConnectionInput
}

type batchImportSchedule struct {
	Row            int
	Name           string
	ConnectionKey  string
	Databases      []string
	Storage        model.RemoteStorage
	Frequency      string
	Time           string
	Weekday        int
	WeekdayLabel   string
	MonthDay       int
	RetentionDays  int
	RecipientIDs   []string
	RecipientNames []string
}

type parsedBatchImport struct {
	Preview     batchImportPreview
	Connections []batchImportConnection
	Schedules   []batchImportSchedule
}

func (s *Server) previewBatchImport(w http.ResponseWriter, r *http.Request) {
	fileName, data, ok := readBatchImportFile(w, r)
	if !ok {
		return
	}
	parsed, err := s.parseBatchImport(fileName, data)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_import_file", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, parsed.Preview)
}

func (s *Server) executeBatchImport(w http.ResponseWriter, r *http.Request) {
	fileName, data, ok := readBatchImportFile(w, r)
	if !ok {
		return
	}
	parsed, err := s.parseBatchImport(fileName, data)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_import_file", err.Error())
		return
	}
	result := batchImportResult{
		Success:     false,
		Preview:     parsed.Preview,
		Connections: []model.Connection{},
		Backups:     []model.BackupJob{},
	}
	if !parsed.Preview.Valid {
		writeJSON(w, http.StatusOK, result)
		return
	}

	testResults := make(map[string]struct {
		Latency time.Duration
		Version string
	}, len(parsed.Connections))
	for index, connection := range parsed.Connections {
		ctx, cancel := context.WithTimeout(r.Context(), 12*time.Second)
		latency, version, testErr := testConnection(ctx, connection.Input)
		cancel()
		if testErr != nil {
			message := userFacingErrorMessage(errorContextConnectionTest, testErr)
			parsed.Preview.Connections[index].Errors = append(parsed.Preview.Connections[index].Errors, message)
			parsed.Preview.Issues = append(parsed.Preview.Issues, batchImportIssue{Sheet: "数据库连接", Row: connection.Row, Field: "连接测试", Message: message})
			continue
		}
		testResults[connection.Key] = struct {
			Latency time.Duration
			Version string
		}{Latency: latency, Version: version}
	}
	if len(parsed.Preview.Issues) > 0 {
		parsed.Preview.Valid = false
		parsed.Preview.Summary.Errors = len(parsed.Preview.Issues)
		result.Preview = parsed.Preview
		writeJSON(w, http.StatusOK, result)
		return
	}

	connectionByKey := make(map[string]batchImportConnection, len(parsed.Connections))
	for _, connection := range parsed.Connections {
		connectionByKey[connection.Key] = connection
	}
	allDatabasesByConnection := make(map[string][]string)
	for index := range parsed.Schedules {
		schedule := &parsed.Schedules[index]
		if !importsAllDatabases(schedule.Databases) {
			continue
		}
		databaseNames, cached := allDatabasesByConnection[schedule.ConnectionKey]
		if !cached {
			connection := connectionByKey[schedule.ConnectionKey]
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			items, discoveryErr := databases(ctx, model.Connection{
				Type: connection.Input.Type, Host: connection.Input.Host, Port: connection.Input.Port,
				Username: connection.Input.Username, Password: connection.Input.Password,
				SSLMode: connection.Input.SSLMode, TLSConfig: connection.Input.TLSConfig,
			})
			cancel()
			if discoveryErr != nil {
				message := userFacingErrorMessage(errorContextDatabaseDiscovery, discoveryErr)
				parsed.Preview.Schedules[index].Errors = append(parsed.Preview.Schedules[index].Errors, message)
				parsed.Preview.Issues = append(parsed.Preview.Issues, batchImportIssue{Sheet: "定时备份任务", Row: schedule.Row, Field: "数据库列表", Message: message})
				continue
			}
			databaseNames = make([]string, 0, len(items))
			for _, item := range items {
				if name := strings.TrimSpace(item.Name); name != "" {
					databaseNames = append(databaseNames, name)
				}
			}
			allDatabasesByConnection[schedule.ConnectionKey] = databaseNames
		}
		if len(databaseNames) == 0 {
			message := "该连接下未发现可备份的数据库"
			parsed.Preview.Schedules[index].Errors = append(parsed.Preview.Schedules[index].Errors, message)
			parsed.Preview.Issues = append(parsed.Preview.Issues, batchImportIssue{Sheet: "定时备份任务", Row: schedule.Row, Field: "数据库列表", Message: message})
			continue
		}
		schedule.Databases = append([]string(nil), databaseNames...)
		parsed.Preview.Schedules[index].Databases = append([]string{}, databaseNames...)
	}
	if len(parsed.Preview.Issues) > 0 {
		parsed.Preview.Valid = false
		parsed.Preview.Summary.Errors = len(parsed.Preview.Issues)
		result.Preview = parsed.Preview
		writeJSON(w, http.StatusOK, result)
		return
	}

	createdByKey := make(map[string]model.Connection, len(parsed.Connections))
	createdConnectionIDs := make([]string, 0, len(parsed.Connections))
	createdBackupIDs := make([]string, 0, len(parsed.Schedules))
	rollback := func() {
		for index := len(createdBackupIDs) - 1; index >= 0; index-- {
			_ = s.store.DeleteBackup(createdBackupIDs[index])
		}
		for index := len(createdConnectionIDs) - 1; index >= 0; index-- {
			_ = s.store.DeleteConnection(createdConnectionIDs[index])
		}
	}

	for _, connection := range parsed.Connections {
		tested := testResults[connection.Key]
		created, createErr := s.store.AddConnection(connection.Input, tested.Latency, tested.Version)
		if createErr != nil {
			rollback()
			parsed.Preview.Valid = false
			parsed.Preview.Issues = append(parsed.Preview.Issues, batchImportIssue{Sheet: "数据库连接", Row: connection.Row, Field: "保存", Message: "保存连接失败，请重试"})
			parsed.Preview.Summary.Errors = len(parsed.Preview.Issues)
			result.Preview = parsed.Preview
			writeJSON(w, http.StatusOK, result)
			return
		}
		created.Region = s.connectionRegion(r.Context(), created.Host)
		createdByKey[connection.Key] = created
		createdConnectionIDs = append(createdConnectionIDs, created.ID)
		result.Connections = append(result.Connections, created)
	}

	for _, schedule := range parsed.Schedules {
		connection := createdByKey[schedule.ConnectionKey]
		job, createErr := s.addImportedSchedule(connection, schedule)
		if createErr != nil {
			rollback()
			parsed.Preview.Valid = false
			parsed.Preview.Issues = append(parsed.Preview.Issues, batchImportIssue{Sheet: "定时备份任务", Row: schedule.Row, Field: "保存", Message: createErr.Error()})
			parsed.Preview.Summary.Errors = len(parsed.Preview.Issues)
			result.Preview = parsed.Preview
			result.Connections = []model.Connection{}
			result.Backups = []model.BackupJob{}
			writeJSON(w, http.StatusOK, result)
			return
		}
		createdBackupIDs = append(createdBackupIDs, job.ID)
		result.Backups = append(result.Backups, job)
	}

	result.Success = true
	writeJSON(w, http.StatusCreated, result)
}

func (s *Server) addImportedSchedule(connection model.Connection, schedule batchImportSchedule) (model.BackupJob, error) {
	request := model.BackupRequest{
		Name:              schedule.Name,
		SourceID:          connection.ID,
		SourceDatabase:    strings.Join(schedule.Databases, ", "),
		SourceDatabases:   append([]string(nil), schedule.Databases...),
		DestinationType:   schedule.Storage.Type,
		ScheduleMode:      "scheduled",
		ScheduleFrequency: schedule.Frequency,
		ScheduleTime:      schedule.Time,
		ScheduleWeekday:   schedule.Weekday,
		ScheduleMonthDay:  schedule.MonthDay,
		RetentionDays:     schedule.RetentionDays,
		Notification: model.BackupNotification{
			Enabled:      len(schedule.RecipientIDs) > 0,
			RecipientIDs: append([]string(nil), schedule.RecipientIDs...),
		},
	}
	destinationLabel := schedule.Storage.Name
	if schedule.Storage.Type == "object" || schedule.Storage.Type == "webdav" {
		request.Destination = schedule.Storage.ID
		request.Storage = &schedule.Storage
		if schedule.Storage.Type == "object" {
			destinationLabel += " / " + schedule.Storage.Bucket
		} else {
			destinationLabel += " / " + schedule.Storage.Path
		}
	} else if schedule.Storage.Type == "local" {
		request.Destination = schedule.Storage.ID
		destinationLabel += " / " + schedule.Storage.Path
	} else {
		return model.BackupJob{}, fmt.Errorf("存储 %q 暂不支持备份写入", schedule.Storage.Name)
	}
	request.PrimaryDestinationID = schedule.Storage.ID
	request.BackupTargets = []model.BackupTarget{{StorageID: schedule.Storage.ID, Role: "primary", Order: 0}}
	request.TargetStorages = []model.RemoteStorage{schedule.Storage}
	normalizeBackupSchedule(&request)
	now := time.Now().In(appLocation)
	next, err := nextBackupRun(request, now)
	if err != nil {
		return model.BackupJob{}, err
	}
	_, cancel := context.WithCancel(context.Background())
	job := model.BackupJob{
		ID: store.NewID(), Name: request.Name, Status: model.StatusScheduled, Stage: "scheduled", Progress: 0,
		StartedAt: now, SourceName: connection.Name, SourceType: connection.Type,
		SourceDatabase: request.SourceDatabase, DestinationType: request.DestinationType,
		Destination: destinationLabel, BackupTargets: append([]model.BackupTarget(nil), request.BackupTargets...), PrimaryDestinationID: request.PrimaryDestinationID,
		Request: request, ScheduleMode: request.ScheduleMode,
		ScheduleFrequency: request.ScheduleFrequency, ScheduleTime: request.ScheduleTime,
		ScheduleWeekday: request.ScheduleWeekday, ScheduleMonthDay: request.ScheduleMonthDay,
		RetentionDays: request.RetentionDays, RetentionMonths: request.RetentionMonths,
		Notification: request.Notification, NotificationEnabled: request.Notification.Enabled,
		NotificationRecipientIDs: append([]string(nil), request.Notification.RecipientIDs...),
		NotificationTrigger:      request.Notification.Trigger,
		NextRunAt:                &next,
		Events: []model.Event{
			{Time: now, Level: "INFO", Message: "备份任务已通过 Excel 批量导入创建"},
			{Time: now, Level: "INFO", Message: "定时备份计划已保存，等待下一次执行"},
		},
	}
	if err := s.store.AddBackup(job, cancel); err != nil {
		cancel()
		return model.BackupJob{}, fmt.Errorf("保存定时任务失败，请重试")
	}
	return job, nil
}

func (s *Server) parseBatchImport(fileName string, data []byte) (parsedBatchImport, error) {
	workbook, err := parseXLSX(data)
	if err != nil {
		return parsedBatchImport{}, err
	}
	connectionRows, ok := workbook.Sheets["数据库连接"]
	if !ok {
		return parsedBatchImport{}, fmt.Errorf("缺少“数据库连接”工作表，请使用系统提供的模板")
	}
	scheduleRows, ok := workbook.Sheets["定时备份任务"]
	if !ok {
		return parsedBatchImport{}, fmt.Errorf("缺少“定时备份任务”工作表，请使用系统提供的模板")
	}
	result := parsedBatchImport{Preview: batchImportPreview{
		FileName: fileName, Valid: true,
		Connections: []batchImportConnectionPreview{}, Schedules: []batchImportSchedulePreview{}, Issues: []batchImportIssue{},
	}}

	existingConnections, err := s.store.Connections()
	if err != nil {
		return parsedBatchImport{}, fmt.Errorf("读取已有连接失败")
	}
	existingStorages, err := s.store.Storages()
	if err != nil {
		return parsedBatchImport{}, fmt.Errorf("读取已有存储失败")
	}
	existingRecipients, err := s.store.NotificationRecipients()
	if err != nil {
		return parsedBatchImport{}, fmt.Errorf("读取通知接收人失败")
	}
	existingBackups, err := s.store.Backups()
	if err != nil {
		return parsedBatchImport{}, fmt.Errorf("读取已有备份任务失败")
	}

	connectionHeaders := []string{"连接标识", "连接名称", "数据库类型", "主机地址", "端口", "用户名", "密码", "SSL模式", "备注"}
	headerRow, columns, err := findHeaderRow(connectionRows, connectionHeaders)
	if err != nil {
		return parsedBatchImport{}, fmt.Errorf("“数据库连接”工作表：%w", err)
	}
	existingConnectionNames := make(map[string]bool)
	for _, item := range existingConnections {
		existingConnectionNames[importLookupKey(item.Name)] = true
	}
	connectionKeys := make(map[string]bool)
	connectionNames := make(map[string]bool)
	for rowIndex := headerRow + 1; rowIndex < len(connectionRows); rowIndex++ {
		row := connectionRows[rowIndex]
		if emptyImportRow(row, columns, connectionHeaders) {
			continue
		}
		connection, preview := parseImportConnection(rowIndex+1, row, columns)
		addConnectionError := func(field, message string) {
			preview.Errors = append(preview.Errors, message)
			result.Preview.Issues = append(result.Preview.Issues, batchImportIssue{Sheet: "数据库连接", Row: rowIndex + 1, Field: field, Message: message})
		}
		if connection.Key == "" {
			addConnectionError("连接标识", "连接标识不能为空")
		} else if !importConnectionKeyPattern.MatchString(connection.Key) {
			addConnectionError("连接标识", "连接标识只能包含字母、数字、短横线和下划线，且最长 64 个字符")
		} else if connectionKeys[connection.Key] {
			addConnectionError("连接标识", "连接标识在文件中重复")
		} else {
			connectionKeys[connection.Key] = true
		}
		nameKey := importLookupKey(connection.Input.Name)
		if nameKey == "" {
			addConnectionError("连接名称", "连接名称不能为空")
		} else if existingConnectionNames[nameKey] {
			addConnectionError("连接名称", "系统中已存在同名连接")
		} else if connectionNames[nameKey] {
			addConnectionError("连接名称", "连接名称在文件中重复")
		} else {
			connectionNames[nameKey] = true
		}
		if connection.Input.Type == "" {
			addConnectionError("数据库类型", "数据库类型仅支持 PostgreSQL、MySQL、MongoDB 或 Neo4j")
		}
		if strings.TrimSpace(connection.Input.Host) == "" {
			addConnectionError("主机地址", "主机地址不能为空")
		}
		if connection.Input.Port < 1 || connection.Input.Port > 65535 {
			addConnectionError("端口", "端口必须是 1 到 65535 之间的整数")
		}
		if strings.TrimSpace(connection.Input.Username) == "" {
			addConnectionError("用户名", "用户名不能为空")
		}
		if connection.Input.Password == "" {
			addConnectionError("密码", "密码不能为空")
		}
		if !validImportSSLMode(connection.Input.Type, connection.Input.SSLMode) {
			addConnectionError("SSL模式", "SSL 模式与数据库类型不匹配，请使用模板下拉选项")
		}
		result.Connections = append(result.Connections, connection)
		result.Preview.Connections = append(result.Preview.Connections, preview)
	}

	storageByName := groupStoragesByName(existingStorages)
	recipientsByName := groupRecipientsByName(existingRecipients)
	existingBackupNames := make(map[string]bool)
	for _, item := range existingBackups {
		existingBackupNames[importLookupKey(item.Name)] = true
	}
	scheduleNames := make(map[string]bool)
	scheduleHeaders := []string{"任务名称", "连接标识", "数据库列表", "存储名称", "频率", "执行时间", "每周星期", "每月日期", "保留天数", "通知接收人"}
	scheduleHeaderRow, scheduleColumns, err := findHeaderRow(scheduleRows, scheduleHeaders)
	if err != nil {
		return parsedBatchImport{}, fmt.Errorf("“定时备份任务”工作表：%w", err)
	}
	for rowIndex := scheduleHeaderRow + 1; rowIndex < len(scheduleRows); rowIndex++ {
		row := scheduleRows[rowIndex]
		if emptyImportRow(row, scheduleColumns, scheduleHeaders) {
			continue
		}
		schedule, preview := parseImportSchedule(rowIndex+1, row, scheduleColumns)
		addScheduleError := func(field, message string) {
			preview.Errors = append(preview.Errors, message)
			result.Preview.Issues = append(result.Preview.Issues, batchImportIssue{Sheet: "定时备份任务", Row: rowIndex + 1, Field: field, Message: message})
		}
		nameKey := importLookupKey(schedule.Name)
		if nameKey == "" {
			addScheduleError("任务名称", "任务名称不能为空")
		} else if existingBackupNames[nameKey] {
			addScheduleError("任务名称", "系统中已存在同名备份任务")
		} else if scheduleNames[nameKey] {
			addScheduleError("任务名称", "任务名称在文件中重复")
		} else {
			scheduleNames[nameKey] = true
		}
		if !connectionKeys[schedule.ConnectionKey] {
			addScheduleError("连接标识", "未找到对应的数据库连接标识")
		}
		if len(schedule.Databases) == 0 {
			addScheduleError("数据库列表", "至少填写一个数据库")
		} else if containsAllDatabaseToken(schedule.Databases) && !importsAllDatabases(schedule.Databases) {
			addScheduleError("数据库列表", "all 需要单独填写，不能与具体数据库名称混用")
		}
		storageName := importCell(row, scheduleColumns, "存储名称")
		storages := storageByName[importLookupKey(storageName)]
		if len(storages) == 0 {
			addScheduleError("存储名称", "系统中未找到该存储")
		} else if len(storages) > 1 {
			addScheduleError("存储名称", "系统中存在多个同名存储，请先修改为唯一名称")
		} else if storages[0].Type != "local" && storages[0].Type != "object" && storages[0].Type != "webdav" {
			addScheduleError("存储名称", "该存储类型暂不支持备份写入")
		} else {
			schedule.Storage = storages[0]
		}
		if schedule.Frequency == "" {
			addScheduleError("频率", "频率仅支持每天、每周或每月")
		}
		if _, _, _, timeErr := parseScheduleTime(schedule.Time); timeErr != nil {
			addScheduleError("执行时间", "请填写 HH:mm 或 HH:mm:ss 格式的执行时间")
		}
		if schedule.Frequency == "weekly" && schedule.WeekdayLabel == "" {
			addScheduleError("每周星期", "每周任务必须选择星期")
		}
		if schedule.Frequency == "monthly" && (schedule.MonthDay < 1 || schedule.MonthDay > 28) {
			addScheduleError("每月日期", "每月任务必须填写 1 到 28 之间的日期")
		}
		if schedule.RetentionDays < 1 || schedule.RetentionDays > 3650 {
			addScheduleError("保留天数", "保留天数必须是 1 到 3650 之间的整数")
		}
		for _, recipientName := range schedule.RecipientNames {
			recipients := recipientsByName[importLookupKey(recipientName)]
			if len(recipients) == 0 {
				addScheduleError("通知接收人", fmt.Sprintf("系统中未找到通知接收人“%s”", recipientName))
			} else if len(recipients) > 1 {
				addScheduleError("通知接收人", fmt.Sprintf("通知接收人“%s”存在重名，请先修改为唯一名称", recipientName))
			} else {
				schedule.RecipientIDs = append(schedule.RecipientIDs, recipients[0].ID)
			}
		}
		result.Schedules = append(result.Schedules, schedule)
		result.Preview.Schedules = append(result.Preview.Schedules, preview)
	}

	if len(result.Connections) == 0 && len(result.Schedules) == 0 {
		result.Preview.Issues = append(result.Preview.Issues, batchImportIssue{Sheet: "Excel", Row: 0, Message: "模板中没有可导入的数据"})
	}
	if len(result.Schedules) > 0 && len(result.Connections) == 0 {
		result.Preview.Issues = append(result.Preview.Issues, batchImportIssue{Sheet: "数据库连接", Row: 0, Message: "定时任务必须引用本次导入的数据库连接"})
	}
	result.Preview.Summary = batchImportSummary{Connections: len(result.Connections), Schedules: len(result.Schedules), Errors: len(result.Preview.Issues)}
	result.Preview.Valid = len(result.Preview.Issues) == 0
	return result, nil
}

func parseImportConnection(rowNumber int, row []string, columns map[string]int) (batchImportConnection, batchImportConnectionPreview) {
	port, _ := parseImportInteger(importCell(row, columns, "端口"))
	databaseType := normalizeDatabaseType(importCell(row, columns, "数据库类型"))
	sslMode := strings.ToLower(importCell(row, columns, "SSL模式"))
	connection := batchImportConnection{
		Row: rowNumber,
		Key: importCell(row, columns, "连接标识"),
		Input: model.ConnectionInput{
			Name: importCell(row, columns, "连接名称"), Type: databaseType,
			Host: importCell(row, columns, "主机地址"), Port: port,
			Username: importCell(row, columns, "用户名"), Password: importCell(row, columns, "密码"),
			SSLMode: sslMode, Note: importCell(row, columns, "备注"),
		},
	}
	preview := batchImportConnectionPreview{
		Row: connection.Row, Key: connection.Key, Name: connection.Input.Name, Type: connection.Input.Type,
		Host: connection.Input.Host, Port: connection.Input.Port, Username: connection.Input.Username,
		SSLMode: connection.Input.SSLMode, Note: connection.Input.Note, Errors: []string{},
	}
	return connection, preview
}

func parseImportSchedule(rowNumber int, row []string, columns map[string]int) (batchImportSchedule, batchImportSchedulePreview) {
	frequency := normalizeImportFrequency(importCell(row, columns, "频率"))
	timeValue := normalizeImportTime(importCell(row, columns, "执行时间"))
	weekday, weekdayLabel := normalizeImportWeekday(importCell(row, columns, "每周星期"))
	monthDay, _ := parseImportInteger(importCell(row, columns, "每月日期"))
	retentionDays, _ := parseImportInteger(importCell(row, columns, "保留天数"))
	schedule := batchImportSchedule{
		Row: rowNumber, Name: importCell(row, columns, "任务名称"), ConnectionKey: importCell(row, columns, "连接标识"),
		Databases: splitImportList(importCell(row, columns, "数据库列表")),
		Frequency: frequency, Time: timeValue, Weekday: weekday, WeekdayLabel: weekdayLabel,
		MonthDay: monthDay, RetentionDays: retentionDays,
		RecipientNames: splitImportList(importCell(row, columns, "通知接收人")),
	}
	preview := batchImportSchedulePreview{
		Row: schedule.Row, Name: schedule.Name, ConnectionKey: schedule.ConnectionKey,
		Databases: append([]string{}, schedule.Databases...), StorageName: importCell(row, columns, "存储名称"),
		Frequency: importFrequencyLabel(schedule.Frequency), Time: schedule.Time, Weekday: schedule.WeekdayLabel,
		MonthDay: schedule.MonthDay, RetentionDays: schedule.RetentionDays,
		RecipientNames: append([]string{}, schedule.RecipientNames...), Errors: []string{},
	}
	return schedule, preview
}

func readBatchImportFile(w http.ResponseWriter, r *http.Request) (string, []byte, bool) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBatchImportFileSize)
	if err := r.ParseMultipartForm(maxBatchImportFileSize); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_import_file", "文件过大或上传格式无效，Excel 文件不能超过 6 MB")
		return "", nil, false
	}
	if r.MultipartForm != nil {
		defer r.MultipartForm.RemoveAll()
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_import_file", "请选择要导入的 Excel 文件")
		return "", nil, false
	}
	defer file.Close()
	fileName := strings.TrimSpace(header.Filename)
	if !strings.HasSuffix(strings.ToLower(fileName), ".xlsx") {
		writeError(w, http.StatusBadRequest, "invalid_import_file", "仅支持 .xlsx 格式的 Excel 文件")
		return "", nil, false
	}
	data, err := io.ReadAll(io.LimitReader(file, maxBatchImportFileSize+1))
	if err != nil || len(data) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_import_file", "读取 Excel 文件失败")
		return "", nil, false
	}
	if len(data) > maxBatchImportFileSize {
		writeError(w, http.StatusBadRequest, "invalid_import_file", "Excel 文件不能超过 6 MB")
		return "", nil, false
	}
	return fileName, data, true
}

func parseImportInteger(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("empty")
	}
	if parsed, err := strconv.Atoi(value); err == nil {
		return parsed, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil || parsed != float64(int(parsed)) {
		return 0, fmt.Errorf("not integer")
	}
	return int(parsed), nil
}

func normalizeImportTime(value string) string {
	value = strings.TrimSpace(value)
	if numeric, err := strconv.ParseFloat(value, 64); err == nil && numeric >= 0 && numeric < 1 {
		totalSeconds := int(numeric*24*60*60 + 0.5)
		totalSeconds %= 24 * 60 * 60
		return fmt.Sprintf("%02d:%02d:%02d", totalSeconds/3600, (totalSeconds%3600)/60, totalSeconds%60)
	}
	if len(strings.Split(value, ":")) == 2 {
		return value + ":00"
	}
	return value
}

func normalizeImportFrequency(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "每天", "daily":
		return "daily"
	case "每周", "weekly":
		return "weekly"
	case "每月", "monthly":
		return "monthly"
	default:
		return ""
	}
}

func importFrequencyLabel(value string) string {
	switch value {
	case "daily":
		return "每天"
	case "weekly":
		return "每周"
	case "monthly":
		return "每月"
	default:
		return ""
	}
}

func normalizeImportWeekday(value string) (int, string) {
	labels := []string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}
	value = strings.TrimSpace(value)
	for index, label := range labels {
		if value == label || value == strings.TrimPrefix(label, "星期") || value == strconv.Itoa(index) {
			return index, label
		}
	}
	return 0, ""
}

func splitImportList(value string) []string {
	value = strings.ReplaceAll(value, "，", ",")
	seen := make(map[string]bool)
	items := make([]string, 0)
	for _, part := range strings.Split(value, ",") {
		part = strings.TrimSpace(part)
		key := importLookupKey(part)
		if part == "" || seen[key] {
			continue
		}
		seen[key] = true
		items = append(items, part)
	}
	return items
}

func importsAllDatabases(databases []string) bool {
	return len(databases) == 1 && strings.EqualFold(strings.TrimSpace(databases[0]), "all")
}

func containsAllDatabaseToken(databases []string) bool {
	for _, database := range databases {
		if strings.EqualFold(strings.TrimSpace(database), "all") {
			return true
		}
	}
	return false
}

func validImportSSLMode(databaseType, value string) bool {
	switch databaseType {
	case "PostgreSQL", "MySQL":
		return value == "disable" || value == "prefer" || value == "require"
	case "MongoDB", "Neo4j":
		return value == "disable" || value == "require"
	default:
		return false
	}
}

func importLookupKey(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func groupStoragesByName(items []model.RemoteStorage) map[string][]model.RemoteStorage {
	result := make(map[string][]model.RemoteStorage)
	for _, item := range items {
		key := importLookupKey(item.Name)
		result[key] = append(result[key], item)
	}
	return result
}

func groupRecipientsByName(items []model.NotificationRecipient) map[string][]model.NotificationRecipient {
	result := make(map[string][]model.NotificationRecipient)
	for _, item := range items {
		key := importLookupKey(item.Name)
		result[key] = append(result[key], item)
	}
	return result
}

func sortedBatchImportIssues(items []batchImportIssue) []batchImportIssue {
	result := append([]batchImportIssue(nil), items...)
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Sheet != result[j].Sheet {
			return result[i].Sheet < result[j].Sheet
		}
		return result[i].Row < result[j].Row
	})
	return result
}
