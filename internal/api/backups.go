package api

import (
	"context"
	"databridge/internal/backup"
	"databridge/internal/migrate"
	"databridge/internal/model"
	"databridge/internal/mongo"
	"databridge/internal/mysql"
	"databridge/internal/neo4j"
	"databridge/internal/postgres"
	"databridge/internal/remotestorage"
	"databridge/internal/store"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
)

func (s *Server) createBackup(w http.ResponseWriter, r *http.Request) {
	var request model.BackupRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	normalizeBackupSchedule(&request)
	if request.SourceID == "" || request.SourceDatabase == "" || !backupRequestHasDestination(request) {
		if request.SourceID != "" && len(request.SourceDatabases) > 0 && backupRequestHasDestination(request) {
			request.SourceDatabase = strings.Join(request.SourceDatabases, ", ")
		}
	}
	if request.SourceID == "" || request.SourceDatabase == "" || !backupRequestHasDestination(request) {
		writeError(w, http.StatusBadRequest, "invalid_request", "请完整选择源端数据库和备份目标")
		return
	}
	source, err := s.store.Connection(request.SourceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_source", "未找到源端连接，请重新选择")
		return
	}
	if err := s.normalizeMongoOplogBackupRequest(r.Context(), &request, source); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_backup_mode", err.Error())
		return
	}
	destinationLabel := request.Destination
	targets, err := s.hydrateBackupTargets(&request)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_destination", err.Error())
		return
	}
	if len(targets) > 0 {
		destinationLabel = backupTargetsLabel(targets)
	}
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now().In(appLocation)
	status := model.StatusPending
	stage := "pending"
	progress := 0
	var nextRunAt *time.Time
	if request.ScheduleMode == "scheduled" {
		status = model.StatusScheduled
		stage = "scheduled"
		next, err := nextBackupRun(request, now)
		if err != nil {
			cancel()
			writeError(w, http.StatusBadRequest, "invalid_schedule", err.Error())
			return
		}
		nextRunAt = &next
	}
	value := model.BackupJob{
		ID: store.NewID(), Name: request.Name, Status: status, Stage: stage, Progress: progress,
		StartedAt: now, SourceName: source.Name, SourceID: source.ID, SourceType: source.Type,
		SourceDatabase: request.SourceDatabase, DestinationType: request.DestinationType,
		Destination: destinationLabel, BackupTargets: append([]model.BackupTarget(nil), request.BackupTargets...), PrimaryDestinationID: request.PrimaryDestinationID,
		Request: request, ScheduleMode: request.ScheduleMode,
		ScheduleFrequency: request.ScheduleFrequency, ScheduleTime: request.ScheduleTime,
		ScheduleWeekday: request.ScheduleWeekday, ScheduleMonthDay: request.ScheduleMonthDay,
		BackupMode: request.BackupMode, FullBackupIntervalDays: request.FullBackupIntervalDays,
		MongoRPOHours: mongoRPOHours(request), MongoRequiredOplogHours: mongoOplogRequirementHours(request, 0),
		RestoreDrillEnabled: request.RestoreDrillEnabled, RestoreDrillTargetID: request.RestoreDrillTargetID,
		RetentionDays:   request.RetentionDays,
		RetentionMonths: request.RetentionMonths,
		Notification:    request.Notification, NotificationEnabled: request.Notification.Enabled,
		NotificationMethod: request.Notification.Method, NotificationTarget: request.Notification.Target,
		NotificationRecipientIDs: request.Notification.RecipientIDs,
		NotificationTrigger:      request.Notification.Trigger,
		NextRunAt:                nextRunAt,
		Events:                   []model.Event{{Time: now, Level: "INFO", Message: "备份任务已创建"}},
	}
	if request.ScheduleMode == "scheduled" {
		appendJobEvent(&value.Events, model.Event{Time: now, Level: "INFO", Message: "定时备份计划已保存，等待下一次执行"})
	}
	if value.Name == "" {
		value.Name = request.SourceDatabase + " 备份"
	}
	if err := s.store.AddBackup(value, cancel); err != nil {
		cancel()
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateBackup, err))
		return
	}
	if request.ScheduleMode != "scheduled" {
		go s.runBackup(ctx, value.ID, source, request)
	}
	writeJSON(w, http.StatusAccepted, value)
}

func (s *Server) runBackup(ctx context.Context, id string, source model.Connection, request model.BackupRequest) {
	defer s.store.ClearBackupCancel(id)
	if !s.acquireJobSlot(ctx) {
		_ = s.store.UpdateBackup(id, func(item *model.BackupJob) {
			now := time.Now()
			item.Status = model.StatusCancelled
			item.Stage = "cancelled"
			item.FinishedAt = &now
			item.Error = "备份任务在排队等待执行时已取消"
			appendJobEvent(&item.Events, model.Event{Time: now, Level: "WARN", Message: item.Error})
		})
		return
	}
	defer s.releaseJobSlot()
	log.Printf("backup[%s] started: %s/%s -> %s", id, source.Name, request.SourceDatabase, request.DestinationType)
	started := time.Now()
	versionID := store.NewID()
	job, loadErr := s.store.Backup(id)
	if loadErr != nil {
		log.Printf("backup[%s] load failed: %v", id, loadErr)
		return
	}
	mongoPlan, planErr := s.mongoOplogBackupPlan(ctx, job, request, source, versionID, started)
	if planErr != nil {
		mongoPlan = mongoOplogRunPlan{Enabled: true, Mode: "full", ChainID: versionID, Message: "无法确认 oplog 增量链，已回退为全量基线：" + planErr.Error()}
	}
	if mongoPlan.Enabled {
		request.MongoOplogStart = mongoPlan.Start
	}
	_ = s.store.UpdateBackup(id, func(item *model.BackupJob) {
		item.Status = model.StatusRunning
		item.Stage = "export"
		item.Progress = 5
		item.StartedAt = started
		item.LastRunAt = &started
		item.FinishedAt = nil
		item.Error = ""
		if mongoPlan.Enabled && mongoPlan.RequiredOplogHours > 0 {
			item.MongoRPOHours = mongoPlan.RPOHours
			item.MongoRequiredOplogHours = mongoPlan.RequiredOplogHours
			item.MongoOplogWindowHours = mongoPlan.OplogWindowHours
		}
		item.Artifacts = nil
		item.ArtifactVersions = append([]model.BackupArtifactVersion{{
			ID:              versionID,
			StartedAt:       started,
			Status:          model.StatusRunning,
			Stage:           "export",
			BackupMode:      mongoPlan.Mode,
			ChainID:         mongoPlan.ChainID,
			ParentID:        mongoPlan.ParentID,
			MongoOplogStart: mongoPlan.Start,
		}}, item.ArtifactVersions...)
		// An oplog restore chain is only valid when every increment remains
		// available. Do not truncate MongoDB versions by count; retention removes
		// expired chains atomically instead.
		if item.BackupMode != mongoOplogBackupMode && len(item.ArtifactVersions) > 20 {
			item.ArtifactVersions = item.ArtifactVersions[:20]
		}
	})
	if mongoPlan.Message != "" {
		_ = s.store.UpdateBackup(id, func(item *model.BackupJob) {
			appendJobEvent(&item.Events, model.Event{Time: started, Level: "INFO", Message: mongoPlan.Message})
		})
	}
	reporter := func(progress backup.Progress) {
		_ = s.store.UpdateBackup(id, func(item *model.BackupJob) {
			item.Stage = progress.Stage
			item.Progress = backupProgress(progress.Stage, progress.Progress)
			if progress.ArtifactName != "" {
				item.ArtifactName = progress.ArtifactName
			}
			if progress.ArtifactSize > 0 {
				item.ArtifactSize = progress.ArtifactSize
			}
			if progress.ArtifactPath != "" {
				item.ArtifactPath = progress.ArtifactPath
			}
			appendJobEvent(&item.Events, model.Event{Time: time.Now(), Level: progress.EventLevel, Message: progress.EventMessage})
		})
	}
	databases := backupDatabases(request)
	var err error
	if mongoPlan.Enabled && mongoPlan.Mode == "full" {
		err = s.preflightMongoFullBackupWorkspace(ctx, source, reporter)
	}
	for index, database := range databases {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if err != nil {
			break
		}
		current := request
		current.SourceDatabase = database
		reporter(backup.Progress{Stage: "export", EventLevel: "INFO", EventMessage: fmt.Sprintf("开始备份数据库 %s（%d/%d）", database, index+1, len(databases)), Progress: index * 90 / len(databases)})
		if runErr := s.backup.Run(ctx, source, current, func(progress backup.Progress) {
			if len(databases) > 1 {
				progress.Progress = index*90/len(databases) + backupProgress(progress.Stage, progress.Progress)/len(databases)
			}
			if progress.ArtifactName != "" {
				s.recordBackupArtifact(id, versionID, database, progress)
			}
			if progress.MongoOplogEnd != nil {
				s.recordMongoOplogVersionMetadata(id, versionID, progress)
			}
			reporter(progress)
		}); runErr != nil {
			err = runErr
			break
		}
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	finished := time.Now()
	_ = s.store.UpdateBackup(id, func(item *model.BackupJob) {
		item.FinishedAt = &finished
		if item.ScheduleMode == "scheduled" && !item.SchedulePaused {
			next, nextErr := nextBackupRun(item.Request, finished)
			if nextErr == nil {
				item.NextRunAt = &next
			}
		}
		if err == nil {
			if item.ScheduleMode == "scheduled" {
				item.Status = model.StatusScheduled
				item.Stage = "scheduled"
			} else {
				item.Status = model.StatusCompleted
				item.Stage = "completed"
			}
			item.Progress = 100
			finishBackupArtifactVersion(item, versionID, model.StatusCompleted, "completed", finished, "")
			if item.ScheduleMode == "scheduled" && !item.SchedulePaused && item.NextRunAt != nil {
				appendJobEvent(&item.Events, model.Event{Time: finished, Level: "INFO", Message: "下一次备份将在 " + item.NextRunAt.Format("2006-01-02 15:04") + " 执行"})
			} else {
				appendJobEvent(&item.Events, model.Event{Time: finished, Level: "INFO", Message: "备份任务完成"})
			}
			return
		}
		if errors.Is(err, context.Canceled) {
			if item.ScheduleMode == "scheduled" {
				item.Status = model.StatusScheduled
				item.Stage = "scheduled"
			} else {
				item.Status = model.StatusCancelled
				item.Stage = "cancelled"
			}
			finishBackupArtifactVersion(item, versionID, model.StatusCancelled, "cancelled", finished, "任务已取消")
			appendJobEvent(&item.Events, model.Event{Time: finished, Level: "WARN", Message: "任务已取消"})
		} else {
			if item.ScheduleMode == "scheduled" {
				item.Status = model.StatusScheduled
				item.Stage = "scheduled"
			} else {
				item.Status = model.StatusFailed
				item.Stage = "failed"
			}
			item.Error = userFacingErrorMessage(errorContextBackupRuntime, err)
			finishBackupArtifactVersion(item, versionID, model.StatusFailed, "failed", finished, err.Error())
			appendJobEvent(&item.Events, model.Event{Time: finished, Level: "ERROR", Message: err.Error()})
		}
		if item.ScheduleMode == "scheduled" && !item.SchedulePaused && item.NextRunAt != nil {
			appendJobEvent(&item.Events, model.Event{Time: finished.Add(time.Millisecond), Level: "INFO", Message: "定时计划保持有效，下一次备份将在 " + item.NextRunAt.Format("2006-01-02 15:04") + " 执行"})
		}
	})
	if err == nil {
		if updatedJob, loadErr := s.store.Backup(id); loadErr == nil && updatedJob.ScheduleMode == "scheduled" {
			s.cleanupExpiredBackupArtifacts(context.Background(), updatedJob, versionID, finished.Add(time.Millisecond))
			_ = s.store.UpdateBackup(id, func(item *model.BackupJob) {
				appendJobEvent(&item.Events, model.Event{Time: finished.Add(2 * time.Millisecond), Level: "INFO", Message: "备份任务完成"})
			})
			if mongoPlan.Enabled && mongoPlan.Mode == "full" && updatedJob.RestoreDrillEnabled {
				s.startMongoRestoreDrill(updatedJob, versionID)
			}
		}
	}
	if updatedJob, loadErr := s.store.Backup(id); loadErr == nil && updatedJob.ScheduleMode == "scheduled" && backupNotificationShouldSend(updatedJob.Notification, err == nil) {
		if notifyErr := s.sendBackupNotification(updatedJob, err == nil); notifyErr != nil {
			log.Printf("backup[%s] notification failed: %v", id, notifyErr)
			_ = s.store.UpdateBackup(id, func(item *model.BackupJob) {
				appendJobEvent(&item.Events, model.Event{Time: time.Now(), Level: "WARN", Message: "备份通知发送失败：" + notifyErr.Error()})
			})
		} else {
			_ = s.store.UpdateBackup(id, func(item *model.BackupJob) {
				appendJobEvent(&item.Events, model.Event{Time: time.Now(), Level: "INFO", Message: "备份通知已发送"})
			})
		}
	}
}

func (s *Server) startMongoRestoreDrill(sourceJob model.BackupJob, versionID string) {
	if !isMongoOplogBackup(sourceJob) || strings.TrimSpace(sourceJob.RestoreDrillTargetID) == "" {
		return
	}
	plan, err := buildMongoOplogRestorePlan(sourceJob, model.RestoreBackupRequest{TargetID: sourceJob.RestoreDrillTargetID, ArtifactVersionID: versionID})
	if err != nil {
		s.recordMongoRestoreDrillEvent(sourceJob.ID, "WARN", "恢复演练未启动："+err.Error())
		return
	}
	target, err := s.store.Connection(plan.TargetID)
	if err != nil {
		s.recordMongoRestoreDrillEvent(sourceJob.ID, "WARN", "恢复演练未启动：未找到目标副本集")
		return
	}
	precheck := s.runRestorePrecheck(context.Background(), sourceJob, target, plan)
	if !precheck.OK {
		messages := make([]string, 0, len(precheck.Checks))
		for _, check := range precheck.Checks {
			if check.Status == "failed" {
				messages = append(messages, check.Label+"："+check.Message)
			}
		}
		s.recordMongoRestoreDrillEvent(sourceJob.ID, "WARN", "恢复演练预检未通过："+strings.Join(messages, "；"))
		return
	}
	sources, err := s.restoreSourcesForPlan(sourceJob, plan)
	if err != nil {
		s.recordMongoRestoreDrillEvent(sourceJob.ID, "WARN", "恢复演练未启动："+err.Error())
		return
	}
	restoreJob, err := backupJobForArtifactVersion(sourceJob, plan.ArtifactVersionID)
	if err != nil {
		s.recordMongoRestoreDrillEvent(sourceJob.ID, "WARN", "恢复演练未启动："+err.Error())
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now().In(appLocation)
	summary := restoreArtifactSummary(restoreJob)
	value := model.BackupJob{
		ID: store.NewID(), Name: fmt.Sprintf("恢复演练 %s 到 %s", sourceJob.Name, target.Name),
		Status: model.StatusPending, Stage: "pending", StartedAt: now,
		SourceName: sourceJob.SourceName, SourceID: sourceJob.SourceID, SourceType: "MongoDB", SourceDatabase: mongoReplicaSetScope,
		DestinationType: "restore", Destination: target.Name + " / " + mongoReplicaSetScope,
		ArtifactName: summary.Name, ArtifactSize: summary.Size, ArtifactPath: sources[len(sources)-1].Path,
		Events: []model.Event{{Time: now, Level: "INFO", Message: "自动恢复演练已创建，来源备份任务 " + sourceJob.ID}, {Time: now, Level: "INFO", Message: "恢复备份版本：" + restoreVersionLabel(restoreJob, versionID)}},
	}
	if err := s.store.AddBackup(value, cancel); err != nil {
		cancel()
		s.recordMongoRestoreDrillEvent(sourceJob.ID, "WARN", "恢复演练任务创建失败："+err.Error())
		return
	}
	s.recordMongoRestoreDrillEvent(sourceJob.ID, "INFO", "全量基线完成，已启动恢复演练："+target.Name)
	go s.runRestore(ctx, value.ID, target, plan.SourceDatabases, plan.TargetDatabases, plan.Strategy, sources, true)
}

func (s *Server) recordMongoRestoreDrillEvent(backupID, level, message string) {
	_ = s.store.UpdateBackup(backupID, func(job *model.BackupJob) {
		appendJobEvent(&job.Events, model.Event{Time: time.Now().In(appLocation), Level: level, Message: message})
	})
}

func (s *Server) preflightMongoFullBackupWorkspace(ctx context.Context, source model.Connection, reporter func(backup.Progress)) error {
	databases, err := mongo.Databases(ctx, source)
	if err != nil {
		return fmt.Errorf("读取 MongoDB 数据库大小失败：%w", err)
	}
	var sourceBytes int64
	for _, database := range databases {
		sourceBytes += database.Size
	}
	const minimumBufferBytes int64 = 512 << 20
	requiredBytes := sourceBytes*5/2 + minimumBufferBytes
	workDir, availableBytes, err := backup.AvailableWorkBytes()
	if err != nil {
		return fmt.Errorf("读取备份工作目录可用空间失败：%w", err)
	}
	reporter(backup.Progress{Stage: "export", EventLevel: "INFO", EventMessage: fmt.Sprintf("MongoDB 全量空间预检：源端约 %s，工作目录至少需要 %s，当前可用 %s", formatBytes(sourceBytes), formatBytes(requiredBytes), formatBytes(availableBytes))})
	if availableBytes < requiredBytes {
		return fmt.Errorf("备份工作目录空间不足：%s 至少需要 %s，当前仅可用 %s，请扩容后重试", workDir, formatBytes(requiredBytes), formatBytes(availableBytes))
	}
	return nil
}

type expiredBackupArtifactCleanup struct {
	RetentionDays int
	Deleted       int
	Failed        int
	Files         []expiredBackupArtifactFile
}

type expiredBackupArtifactFile struct {
	Name       string
	AgeDays    int
	FinishedAt time.Time
}

func (s *Server) cleanupExpiredBackupArtifacts(ctx context.Context, job model.BackupJob, currentVersionID string, eventTime time.Time) {
	result := s.deleteExpiredBackupArtifacts(ctx, &job, currentVersionID, eventTime)
	if result.RetentionDays <= 0 {
		return
	}
	if err := s.store.UpdateBackup(job.ID, func(item *model.BackupJob) {
		item.ArtifactVersions = job.ArtifactVersions
		appendJobEvent(&item.Events, model.Event{Time: eventTime, Level: cleanupEventLevel(result), Message: cleanupEventMessage(result)})
	}); err != nil {
		log.Printf("backup[%s] retention cleanup record failed: %v", job.ID, err)
	}
}

func (s *Server) deleteExpiredBackupArtifacts(ctx context.Context, job *model.BackupJob, currentVersionID string, now time.Time) expiredBackupArtifactCleanup {
	retentionDays := backupRetentionDays(job.RetentionDays, job.RetentionMonths)
	if retentionDays <= 0 {
		retentionDays = backupRetentionDays(job.Request.RetentionDays, job.Request.RetentionMonths)
	}
	result := expiredBackupArtifactCleanup{RetentionDays: retentionDays}
	if retentionDays <= 0 || job.ScheduleMode != "scheduled" || len(job.ArtifactVersions) == 0 {
		return result
	}
	cutoff := now.AddDate(0, 0, -retentionDays)
	var objectStorage *model.RemoteStorage
	if job.DestinationType == "object" || job.DestinationType == "webdav" || job.DestinationType == "one_drive" {
		storage, err := s.backupArtifactStorage(*job)
		if err != nil {
			log.Printf("backup[%s] retention cleanup storage lookup failed: %v", job.ID, err)
			result.Failed++
			return result
		}
		objectStorage = &storage
	}
	mongoExpiredVersions := mongoExpiredBackupVersionIDs(*job, currentVersionID, cutoff)
	for versionIndex := range job.ArtifactVersions {
		version := &job.ArtifactVersions[versionIndex]
		expired := version.FinishedAt != nil && version.FinishedAt.Before(cutoff)
		if isMongoOplogBackup(*job) {
			expired = mongoExpiredVersions[version.ID]
		}
		if version.ID == currentVersionID || version.Status != model.StatusCompleted || !expired {
			continue
		}
		kept := version.Artifacts[:0]
		for _, artifact := range version.Artifacts {
			if strings.TrimSpace(artifact.Name) == "" && strings.TrimSpace(artifact.Path) == "" {
				continue
			}
			if err := s.deleteExpiredBackupArtifact(ctx, *job, artifact, objectStorage); err != nil {
				result.Failed++
				kept = append(kept, artifact)
				log.Printf("backup[%s] retention cleanup failed for %s: %v", job.ID, artifact.Name, err)
				continue
			}
			result.Deleted++
			result.Files = append(result.Files, expiredBackupArtifactFile{
				Name:       cleanupArtifactName(artifact),
				AgeDays:    backupArtifactAgeDays(now, *version.FinishedAt),
				FinishedAt: *version.FinishedAt,
			})
		}
		version.Artifacts = kept
		if len(version.Artifacts) == 0 {
			version.Error = "备份文件已按保留期限清理"
		}
	}
	return result
}

func mongoExpiredBackupVersionIDs(job model.BackupJob, currentVersionID string, cutoff time.Time) map[string]bool {
	expired := map[string]bool{}
	if !isMongoOplogBackup(job) {
		return expired
	}
	currentChain := ""
	chainBaseline := map[string]time.Time{}
	for _, version := range job.ArtifactVersions {
		if version.ID == currentVersionID {
			currentChain = version.ChainID
		}
		if version.Status != model.StatusCompleted || version.ChainID == "" {
			continue
		}
		if version.BackupMode == "full" {
			if existing, ok := chainBaseline[version.ChainID]; !ok || version.StartedAt.Before(existing) {
				chainBaseline[version.ChainID] = version.StartedAt
			}
		}
	}
	for _, version := range job.ArtifactVersions {
		if version.Status != model.StatusCompleted || version.ChainID == "" || version.ChainID == currentChain {
			continue
		}
		if baseline, ok := chainBaseline[version.ChainID]; ok && baseline.Before(cutoff) {
			expired[version.ID] = true
		}
	}
	return expired
}

func (s *Server) deleteExpiredBackupArtifact(ctx context.Context, job model.BackupJob, artifact model.BackupArtifact, objectStorage *model.RemoteStorage) error {
	if len(artifact.Copies) > 0 {
		var failures []string
		for _, copy := range artifact.Copies {
			if copy.Status != "" && copy.Status != "completed" {
				continue
			}
			if err := s.deleteExpiredBackupArtifactCopy(ctx, job, artifact, copy, objectStorage); err != nil {
				failures = append(failures, err.Error())
			}
		}
		if len(failures) > 0 {
			return errors.New(strings.Join(failures, "；"))
		}
		return nil
	}
	switch job.DestinationType {
	case "local":
		path := backupArtifactLocalPath(job, artifact)
		if path == "" {
			return fmt.Errorf("未找到本地备份文件路径")
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return err
		}
		return nil
	case "object":
		if objectStorage == nil {
			return fmt.Errorf("未找到备份目标对象存储")
		}
		return remotestorage.DeleteObject(ctx, *objectStorage, artifact.Name)
	case "webdav":
		if objectStorage == nil {
			return fmt.Errorf("未找到备份目标 WebDAV 存储")
		}
		return remotestorage.DeleteWebDAV(ctx, *objectStorage, artifact.Name)
	case "one_drive":
		if objectStorage == nil {
			return fmt.Errorf("未找到备份目标 OneDrive 存储")
		}
		return remotestorage.DeleteOneDrive(ctx, *objectStorage, artifact.Name)
	default:
		return fmt.Errorf("该备份目标暂不支持清理备份文件")
	}
}

func (s *Server) deleteExpiredBackupArtifactCopy(ctx context.Context, job model.BackupJob, artifact model.BackupArtifact, copy model.BackupArtifactCopy, fallbackObjectStorage *model.RemoteStorage) error {
	switch copy.Type {
	case "local":
		path := copy.Path
		if path == "" {
			path = backupArtifactLocalPath(job, artifact)
		}
		if path == "" {
			return fmt.Errorf("%s：未找到本地备份文件路径", backupCopyLabel(copy))
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%s：%w", backupCopyLabel(copy), err)
		}
		return nil
	case "object":
		storage := fallbackObjectStorage
		if strings.TrimSpace(copy.StorageID) != "" {
			loaded, err := s.store.Storage(copy.StorageID)
			if err != nil {
				return fmt.Errorf("%s：未找到备份目标对象存储", backupCopyLabel(copy))
			}
			storage = &loaded
		}
		if storage == nil {
			return fmt.Errorf("%s：未找到备份目标对象存储", backupCopyLabel(copy))
		}
		name := copy.Name
		if name == "" {
			name = artifact.Name
		}
		return remotestorage.DeleteObject(ctx, *storage, name)
	case "webdav":
		storage := fallbackObjectStorage
		if strings.TrimSpace(copy.StorageID) != "" {
			loaded, err := s.store.Storage(copy.StorageID)
			if err != nil {
				return fmt.Errorf("%s：未找到备份目标 WebDAV 存储", backupCopyLabel(copy))
			}
			storage = &loaded
		}
		if storage == nil {
			return fmt.Errorf("%s：未找到备份目标 WebDAV 存储", backupCopyLabel(copy))
		}
		name := copy.Name
		if name == "" {
			name = artifact.Name
		}
		return remotestorage.DeleteWebDAV(ctx, *storage, name)
	case "one_drive":
		storage := fallbackObjectStorage
		if strings.TrimSpace(copy.StorageID) != "" {
			loaded, err := s.store.Storage(copy.StorageID)
			if err != nil {
				return fmt.Errorf("%s：未找到备份目标 OneDrive 存储", backupCopyLabel(copy))
			}
			storage = &loaded
		}
		if storage == nil {
			return fmt.Errorf("%s：未找到备份目标 OneDrive 存储", backupCopyLabel(copy))
		}
		name := copy.Name
		if name == "" {
			name = artifact.Name
		}
		return remotestorage.DeleteOneDrive(ctx, *storage, name)
	default:
		return fmt.Errorf("%s：该备份目标暂不支持清理备份文件", backupCopyLabel(copy))
	}
}

func backupArtifactLocalPath(job model.BackupJob, artifact model.BackupArtifact) string {
	if strings.TrimSpace(artifact.Path) != "" {
		return artifact.Path
	}
	if job.Request.Destination != "" && strings.TrimSpace(artifact.Name) != "" {
		return filepath.Join(job.Request.Destination, filepath.FromSlash(artifact.Name))
	}
	return ""
}

func cleanupEventLevel(result expiredBackupArtifactCleanup) string {
	if result.Failed > 0 {
		return "WARN"
	}
	return "INFO"
}

func cleanupEventMessage(result expiredBackupArtifactCleanup) string {
	retention := retentionDaysLabel(result.RetentionDays)
	if result.Deleted == 0 && result.Failed == 0 {
		return "过期备份文件清理完成：未发现超过 " + retention + " 的备份文件"
	}
	if result.Failed == 0 {
		return fmt.Sprintf("过期备份文件清理完成：已删除 %d 个超过 %s 的备份文件：%s", result.Deleted, retention, cleanupFileSummary(result.Files))
	}
	return fmt.Sprintf("过期备份文件清理完成：已删除 %d 个，%d 个删除失败，请检查存储权限；已删除：%s", result.Deleted, result.Failed, cleanupFileSummary(result.Files))
}

func retentionDaysLabel(days int) string {
	return fmt.Sprintf("%d 天", days)
}

func cleanupArtifactName(artifact model.BackupArtifact) string {
	if strings.TrimSpace(artifact.Name) != "" {
		return artifact.Name
	}
	if strings.TrimSpace(artifact.Path) != "" {
		return filepath.Base(artifact.Path)
	}
	return "未知文件"
}

func backupArtifactAgeDays(now, finished time.Time) int {
	days := int(now.Sub(finished).Hours() / 24)
	if days < 1 {
		return 1
	}
	return days
}

func cleanupFileSummary(files []expiredBackupArtifactFile) string {
	if len(files) == 0 {
		return "无"
	}
	const maxItems = 5
	items := make([]string, 0, min(len(files), maxItems))
	for index, file := range files {
		if index >= maxItems {
			break
		}
		items = append(items, fmt.Sprintf("%d 天前 %s", file.AgeDays, file.Name))
	}
	if len(files) > maxItems {
		items = append(items, fmt.Sprintf("等 %d 个文件", len(files)))
	}
	return strings.Join(items, "、")
}

func backupRetentionDays(days, months int) int {
	if days > 0 {
		return normalizeBackupRetentionDays(days)
	}
	if months > 0 {
		return normalizeBackupRetentionDays(months * 30)
	}
	return 0
}

func normalizeBackupRetentionDays(days int) int {
	if days < 1 {
		return 30
	}
	if days > 1095 {
		return 1095
	}
	return days
}

func retentionMonthsForCompatibility(days int) int {
	if days <= 0 {
		return 0
	}
	months := days / 30
	if months < 1 {
		return 1
	}
	if months > 36 {
		return 36
	}
	return months
}

func (s *Server) recordBackupArtifact(id, versionID, database string, progress backup.Progress) {
	_ = s.store.UpdateBackup(id, func(item *model.BackupJob) {
		artifact := model.BackupArtifact{
			Database: database,
			Name:     progress.ArtifactName,
			Size:     progress.ArtifactSize,
			Path:     progress.ArtifactPath,
		}
		if progress.ArtifactCopy != nil {
			artifact.Copies = []model.BackupArtifactCopy{*progress.ArtifactCopy}
		}
		recordArtifactInVersion(item, versionID, artifact)
		for index := range item.Artifacts {
			if item.Artifacts[index].Database == database {
				item.Artifacts[index] = mergeBackupArtifact(item.Artifacts[index], artifact)
				return
			}
		}
		item.Artifacts = append(item.Artifacts, artifact)
	})
}

func (s *Server) recordMongoOplogVersionMetadata(id, versionID string, progress backup.Progress) {
	_ = s.store.UpdateBackup(id, func(item *model.BackupJob) {
		for index := range item.ArtifactVersions {
			version := &item.ArtifactVersions[index]
			if version.ID != versionID {
				continue
			}
			if progress.BackupMode != "" {
				version.BackupMode = progress.BackupMode
			}
			if progress.MongoOplogStart != nil {
				value := *progress.MongoOplogStart
				version.MongoOplogStart = &value
			}
			if progress.MongoOplogEnd != nil {
				value := *progress.MongoOplogEnd
				version.MongoOplogEnd = &value
			}
			return
		}
	})
}

type mongoOplogRunPlan struct {
	Enabled            bool
	Mode               string
	ChainID            string
	ParentID           string
	Start              *model.MongoOplogTimestamp
	RPOHours           float64
	RequiredOplogHours float64
	OplogWindowHours   float64
	Message            string
}

func (s *Server) mongoOplogBackupPlan(ctx context.Context, job model.BackupJob, request model.BackupRequest, source model.Connection, versionID string, now time.Time) (mongoOplogRunPlan, error) {
	if request.BackupMode != mongoOplogBackupMode || source.Type != "MongoDB" {
		return mongoOplogRunPlan{}, nil
	}
	fullPlan := func(message string) mongoOplogRunPlan {
		return mongoOplogRunPlan{Enabled: true, Mode: "full", ChainID: versionID, Message: message}
	}
	var latest *model.BackupArtifactVersion
	for index := range job.ArtifactVersions {
		candidate := &job.ArtifactVersions[index]
		if candidate.Status != model.StatusCompleted || len(candidate.Artifacts) == 0 || candidate.MongoOplogEnd == nil || candidate.ChainID == "" {
			continue
		}
		if latest == nil || candidate.StartedAt.After(latest.StartedAt) {
			latest = candidate
		}
	}
	if latest == nil {
		return fullPlan("首次执行 MongoDB oplog 备份，正在创建全量基线"), nil
	}
	var baseline *model.BackupArtifactVersion
	for index := range job.ArtifactVersions {
		candidate := &job.ArtifactVersions[index]
		if candidate.Status == model.StatusCompleted && candidate.ChainID == latest.ChainID && candidate.BackupMode == "full" {
			if baseline == nil || candidate.StartedAt.Before(baseline.StartedAt) {
				baseline = candidate
			}
		}
	}
	if baseline == nil {
		return fullPlan("未找到完整 MongoDB 基线，正在创建新的全量基线"), nil
	}
	info, err := mongo.IncrementalBackupInfo(ctx, source)
	if err != nil {
		return mongoOplogRunPlan{}, err
	}
	fullDuration := time.Duration(0)
	if baseline.FinishedAt != nil && baseline.FinishedAt.After(baseline.StartedAt) {
		fullDuration = baseline.FinishedAt.Sub(baseline.StartedAt)
	}
	rpoHours := mongoRPOHours(request)
	requiredHours := mongoOplogRequirementHours(request, fullDuration)
	windowHours := mongoOplogWindowHours(info)
	if windowHours < requiredHours {
		plan := fullPlan(fmt.Sprintf("oplog 窗口 %.1f 小时低于目标 RPO 所需的 %.1f 小时，正在创建新的全量基线", windowHours, requiredHours))
		plan.RPOHours, plan.RequiredOplogHours, plan.OplogWindowHours = rpoHours, requiredHours, windowHours
		return plan, nil
	}
	interval := request.FullBackupIntervalDays
	if interval <= 0 {
		interval = defaultMongoFullBackupIntervalDays
	}
	if now.Sub(baseline.StartedAt) >= time.Duration(interval)*24*time.Hour {
		return fullPlan("已达到全量基线周期，正在创建新的全量基线"), nil
	}
	if mongo.OplogTimestampCompare(*latest.MongoOplogEnd, info.Oldest) < 0 {
		plan := fullPlan("oplog 保留窗口已越过上一备份位点，正在创建新的全量基线")
		plan.RPOHours, plan.RequiredOplogHours, plan.OplogWindowHours = rpoHours, requiredHours, windowHours
		return plan, nil
	}
	start := *latest.MongoOplogEnd
	return mongoOplogRunPlan{
		Enabled: true, Mode: "incremental", ChainID: latest.ChainID, ParentID: latest.ID, Start: &start,
		RPOHours: rpoHours, RequiredOplogHours: requiredHours, OplogWindowHours: windowHours,
		Message: "正在基于上一成功版本创建 MongoDB oplog 增量",
	}, nil
}

func recordArtifactInVersion(item *model.BackupJob, versionID string, artifact model.BackupArtifact) {
	for versionIndex := range item.ArtifactVersions {
		if item.ArtifactVersions[versionIndex].ID != versionID {
			continue
		}
		for artifactIndex := range item.ArtifactVersions[versionIndex].Artifacts {
			if item.ArtifactVersions[versionIndex].Artifacts[artifactIndex].Database == artifact.Database {
				item.ArtifactVersions[versionIndex].Artifacts[artifactIndex] = mergeBackupArtifact(item.ArtifactVersions[versionIndex].Artifacts[artifactIndex], artifact)
				return
			}
		}
		item.ArtifactVersions[versionIndex].Artifacts = append(item.ArtifactVersions[versionIndex].Artifacts, artifact)
		return
	}
}

func mergeBackupArtifact(current, next model.BackupArtifact) model.BackupArtifact {
	if strings.TrimSpace(next.Name) != "" {
		current.Name = next.Name
	}
	if next.Size > 0 {
		current.Size = next.Size
	}
	if strings.TrimSpace(next.Path) != "" {
		current.Path = next.Path
	}
	if strings.TrimSpace(current.Database) == "" {
		current.Database = next.Database
	}
	for _, copy := range next.Copies {
		current = mergeBackupArtifactCopy(current, copy)
	}
	return current
}

func mergeBackupArtifactCopy(artifact model.BackupArtifact, copy model.BackupArtifactCopy) model.BackupArtifact {
	for index := range artifact.Copies {
		if artifact.Copies[index].StorageID != "" && artifact.Copies[index].StorageID == copy.StorageID {
			artifact.Copies[index] = copy
			return artifact
		}
		if artifact.Copies[index].StorageID == "" && artifact.Copies[index].Path == copy.Path && artifact.Copies[index].Name == copy.Name {
			artifact.Copies[index] = copy
			return artifact
		}
	}
	artifact.Copies = append(artifact.Copies, copy)
	sortBackupArtifactCopies(artifact.Copies)
	return artifact
}

func sortBackupArtifactCopies(copies []model.BackupArtifactCopy) {
	sort.SliceStable(copies, func(i, j int) bool {
		if copies[i].Role == "primary" && copies[j].Role != "primary" {
			return true
		}
		if copies[j].Role == "primary" && copies[i].Role != "primary" {
			return false
		}
		return copies[i].Order < copies[j].Order
	})
}

func finishBackupArtifactVersion(item *model.BackupJob, versionID string, status model.MigrationStatus, stage string, finished time.Time, message string) {
	for index := range item.ArtifactVersions {
		if item.ArtifactVersions[index].ID != versionID {
			continue
		}
		item.ArtifactVersions[index].Status = status
		item.ArtifactVersions[index].Stage = stage
		item.ArtifactVersions[index].FinishedAt = &finished
		item.ArtifactVersions[index].Error = message
		return
	}
}

func backupDatabases(request model.BackupRequest) []string {
	seen := map[string]bool{}
	var databases []string
	for _, database := range request.SourceDatabases {
		database = strings.TrimSpace(database)
		if database != "" && !seen[database] {
			seen[database] = true
			databases = append(databases, database)
		}
	}
	if len(databases) == 0 && strings.TrimSpace(request.SourceDatabase) != "" {
		databases = []string{strings.TrimSpace(request.SourceDatabase)}
	}
	return databases
}

func splitDatabaseList(value string) []string {
	seen := map[string]bool{}
	var databases []string
	for _, database := range strings.Split(value, ",") {
		database = strings.TrimSpace(database)
		if database != "" && !seen[database] {
			seen[database] = true
			databases = append(databases, database)
		}
	}
	return databases
}

func restoreTargetDatabases(sourceDatabases []string, input model.RestoreBackupRequest) (map[string]string, error) {
	if len(sourceDatabases) == 0 {
		return nil, fmt.Errorf("未找到备份中的源数据库")
	}
	targets := map[string]string{}
	for _, sourceDatabase := range sourceDatabases {
		target := strings.TrimSpace(input.TargetDatabases[sourceDatabase])
		if target == "" && len(sourceDatabases) == 1 {
			target = strings.TrimSpace(input.TargetDatabase)
		}
		if target == "" {
			target = sourceDatabase
		}
		targets[sourceDatabase] = target
	}
	return targets, nil
}

func selectedRestoreDatabases(available, selected []string) ([]string, error) {
	if len(available) == 0 {
		return nil, fmt.Errorf("未找到备份中的源数据库")
	}
	if len(selected) == 0 {
		return available, nil
	}
	allowed := map[string]bool{}
	for _, database := range available {
		allowed[database] = true
	}
	seen := map[string]bool{}
	var result []string
	for _, database := range selected {
		database = strings.TrimSpace(database)
		if database == "" || seen[database] {
			continue
		}
		if !allowed[database] {
			return nil, fmt.Errorf("选择的数据库 %s 不在该备份任务中", database)
		}
		seen[database] = true
		result = append(result, database)
	}
	if len(result) == 0 {
		return nil, fmt.Errorf("请至少选择一个要恢复的数据库")
	}
	return result, nil
}

func normalizeRestoreStrategy(strategy string) string {
	switch strings.ToLower(strings.TrimSpace(strategy)) {
	case "recreate", "drop-create", "drop_create":
		return "recreate"
	default:
		return "append"
	}
}

func restoreStrategyLabel(strategy string) string {
	if strategy == "recreate" {
		return "重建目标库后恢复"
	}
	return "追加导入，冲突则失败"
}

func restoreTargetLabel(sourceDatabases []string, targetDatabases map[string]string) string {
	var labels []string
	for _, sourceDatabase := range sourceDatabases {
		targetDatabase := strings.TrimSpace(targetDatabases[sourceDatabase])
		if targetDatabase == "" {
			targetDatabase = sourceDatabase
		}
		labels = append(labels, targetDatabase)
	}
	return strings.Join(labels, ", ")
}

func artifactPathForRestoreDatabase(artifactPath, database string) string {
	if strings.TrimSpace(database) == "" {
		return artifactPath
	}
	base := filepath.Base(artifactPath)
	dir := filepath.Dir(artifactPath)
	if strings.Contains(base, sanitizeFilePart(database)) {
		return artifactPath
	}
	candidates, err := filepath.Glob(filepath.Join(dir, "*", base))
	if err != nil {
		return artifactPath
	}
	for _, candidate := range candidates {
		if strings.Contains(filepath.ToSlash(candidate), "/"+sanitizeFilePart(database)+"/") {
			return candidate
		}
	}
	return artifactPath
}

func sanitizeFilePart(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "" {
		return "database"
	}
	var builder strings.Builder
	for _, char := range value {
		if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || char == '-' || char == '_' {
			builder.WriteRune(char)
		} else {
			builder.WriteRune('_')
		}
	}
	return strings.Trim(builder.String(), "_")
}

func backupProgress(stage string, reported int) int {
	if reported > 0 {
		return reported
	}
	switch stage {
	case "download":
		return 20
	case "export":
		return 35
	case "verify":
		return 70
	case "extract":
		return 25
	case "restore":
		return 75
	case "validation":
		return 95
	case "upload":
		return 85
	case "completed":
		return 100
	default:
		return 10
	}
}

func formatBytes(value int64) string {
	if value < 1024 {
		return fmt.Sprintf("%d B", value)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	size := float64(value)
	index := -1
	for size >= 1024 && index < len(units)-1 {
		size /= 1024
		index++
	}
	return fmt.Sprintf("%.1f %s", size, units[index])
}

func (s *Server) startBackupScheduler() {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			s.runDueBackups()
		}
	}()
}

func (s *Server) runDueBackups() {
	s.reconcileScheduledBackupRuns()
	items, err := s.store.Backups()
	if err != nil {
		log.Printf("backup scheduler list failed: %v", err)
		return
	}
	now := time.Now().In(appLocation)
	for _, item := range items {
		if item.ScheduleMode != "scheduled" || item.SchedulePaused || item.Status == model.StatusRunning || item.Status == model.StatusPending || item.NextRunAt == nil || item.NextRunAt.After(now) {
			continue
		}
		if _, err := s.triggerScheduledBackup(item, "定时备份已触发"); err != nil {
			log.Printf("backup scheduler trigger failed: %s: %v", item.ID, err)
		}
	}
}

func (s *Server) reconcileScheduledBackupRuns() {
	items, err := s.store.Backups()
	if err != nil {
		log.Printf("backup scheduler reconcile list failed: %v", err)
		return
	}
	now := time.Now().In(appLocation)
	for _, item := range items {
		s.reconcileScheduledBackupRun(item, now)
	}
}

func (s *Server) reconcileScheduledBackupRun(item model.BackupJob, now time.Time) {
	if item.SchedulePaused {
		if item.NextRunAt != nil {
			if err := s.store.UpdateBackup(item.ID, func(job *model.BackupJob) {
				if job.SchedulePaused {
					job.NextRunAt = nil
				}
			}); err != nil {
				log.Printf("backup scheduler clear paused due time failed: %s: %v", item.ID, err)
			}
		}
		return
	}
	request, ok := scheduleRequestForBackup(item)
	if !ok {
		return
	}
	if item.NextRunAt != nil && scheduleClockMatches(request, *item.NextRunAt) {
		return
	}
	next, err := nextBackupRun(request, now)
	if err != nil {
		log.Printf("backup scheduler reconcile failed: %s: %v", item.ID, err)
		return
	}
	if err := s.store.UpdateBackup(item.ID, func(job *model.BackupJob) {
		if job.ScheduleMode != "scheduled" || job.Status == model.StatusRunning || job.Status == model.StatusPending {
			return
		}
		job.Request = request
		job.ScheduleMode = request.ScheduleMode
		job.ScheduleFrequency = request.ScheduleFrequency
		job.ScheduleTime = request.ScheduleTime
		job.ScheduleWeekday = request.ScheduleWeekday
		job.ScheduleMonthDay = request.ScheduleMonthDay
		job.RetentionDays = request.RetentionDays
		job.RetentionMonths = request.RetentionMonths
		job.NextRunAt = &next
		appendJobEvent(&job.Events, model.Event{Time: now, Level: "INFO", Message: "已按应用时区修正下一次执行时间"})
	}); err != nil {
		log.Printf("backup scheduler reconcile update failed: %s: %v", item.ID, err)
	}
}

func scheduleRequestForBackup(item model.BackupJob) (model.BackupRequest, bool) {
	if item.ScheduleMode != "scheduled" || item.SchedulePaused || item.Status == model.StatusRunning || item.Status == model.StatusPending {
		return model.BackupRequest{}, false
	}
	return backupScheduleRequest(item), true
}

func backupScheduleRequest(item model.BackupJob) model.BackupRequest {
	request := item.Request
	request.ScheduleMode = "scheduled"
	if request.ScheduleFrequency == "" {
		request.ScheduleFrequency = item.ScheduleFrequency
	}
	if request.ScheduleTime == "" {
		request.ScheduleTime = item.ScheduleTime
	}
	if request.ScheduleWeekday == 0 {
		request.ScheduleWeekday = item.ScheduleWeekday
	}
	if request.ScheduleMonthDay == 0 {
		request.ScheduleMonthDay = item.ScheduleMonthDay
	}
	if request.BackupMode == "" {
		request.BackupMode = item.BackupMode
	}
	if request.FullBackupIntervalDays == 0 {
		request.FullBackupIntervalDays = item.FullBackupIntervalDays
	}
	if !request.RestoreDrillEnabled && request.RestoreDrillTargetID == "" {
		request.RestoreDrillEnabled = item.RestoreDrillEnabled
		request.RestoreDrillTargetID = item.RestoreDrillTargetID
	}
	if request.RetentionMonths == 0 {
		request.RetentionMonths = item.RetentionMonths
	}
	if request.RetentionDays == 0 {
		request.RetentionDays = item.RetentionDays
	}
	normalizeBackupSchedule(&request)
	return request
}

func scheduleClockMatches(request model.BackupRequest, value time.Time) bool {
	hour, minute, second, err := parseScheduleTime(request.ScheduleTime)
	if err != nil {
		return true
	}
	local := value.In(appLocation)
	return local.Hour() == hour && local.Minute() == minute && local.Second() == second
}

func (s *Server) triggerScheduledBackup(item model.BackupJob, message string) (model.BackupJob, error) {
	now := time.Now().In(appLocation)
	if item.ScheduleMode != "scheduled" || item.Status == model.StatusRunning || item.Status == model.StatusPending {
		return item, fmt.Errorf("仅已计划的定时备份可以触发")
	}
	request, err := s.backfillBackupRequest(item)
	if err != nil {
		_ = s.store.UpdateBackup(item.ID, func(job *model.BackupJob) {
			job.Status = model.StatusFailed
			job.Stage = "failed"
			job.Error = err.Error()
			appendJobEvent(&job.Events, model.Event{Time: now, Level: "ERROR", Message: err.Error()})
		})
		return item, err
	}
	source, err := s.store.Connection(request.SourceID)
	if err != nil {
		err = fmt.Errorf("未找到源端连接，请重新配置定时备份")
		_ = s.store.UpdateBackup(item.ID, func(job *model.BackupJob) {
			job.Status = model.StatusFailed
			job.Stage = "failed"
			job.Error = err.Error()
			appendJobEvent(&job.Events, model.Event{Time: now, Level: "ERROR", Message: err.Error()})
		})
		return item, err
	}
	request, err = s.hydrateBackupRequest(request)
	if err != nil {
		_ = s.store.UpdateBackup(item.ID, func(job *model.BackupJob) {
			job.Status = model.StatusFailed
			job.Stage = "failed"
			job.Error = userFacingErrorMessage(errorContextCreateBackup, err)
			appendJobEvent(&job.Events, model.Event{Time: now, Level: "ERROR", Message: err.Error()})
		})
		return item, err
	}
	if err := s.normalizeMongoOplogBackupRequest(context.Background(), &request, source); err != nil {
		_ = s.store.UpdateBackup(item.ID, func(job *model.BackupJob) {
			job.Error = err.Error()
			appendJobEvent(&job.Events, model.Event{Time: now, Level: "WARN", Message: "定时增量备份未启动：" + err.Error()})
		})
		return item, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	if err := s.store.SetBackupCancel(item.ID, cancel); err != nil {
		cancel()
		return item, err
	}
	if err := s.store.UpdateBackup(item.ID, func(job *model.BackupJob) {
		job.Request = request
		job.Status = model.StatusPending
		job.Stage = "pending"
		job.Progress = 0
		job.Error = ""
		appendJobEvent(&job.Events, model.Event{Time: now, Level: "INFO", Message: message})
	}); err != nil {
		cancel()
		s.store.ClearBackupCancel(item.ID)
		return item, err
	}
	updated, err := s.store.Backup(item.ID)
	if err != nil {
		cancel()
		return item, err
	}
	go s.runBackup(ctx, item.ID, source, request)
	return updated, nil
}

func (s *Server) hydrateBackupRequest(request model.BackupRequest) (model.BackupRequest, error) {
	if _, err := s.hydrateBackupTargets(&request); err != nil {
		return request, err
	}
	return request, nil
}

func backupRequestHasDestination(request model.BackupRequest) bool {
	if strings.TrimSpace(request.Destination) != "" {
		return true
	}
	for _, target := range request.BackupTargets {
		if strings.TrimSpace(target.StorageID) != "" {
			return true
		}
	}
	return false
}

func (s *Server) hydrateBackupTargets(request *model.BackupRequest) ([]model.RemoteStorage, error) {
	targets := normalizeBackupTargets(*request)
	if len(targets) == 0 && strings.TrimSpace(request.Destination) != "" {
		if request.DestinationType == "local" && strings.TrimSpace(request.PrimaryDestinationID) == "" {
			request.TargetStorages = nil
			return nil, nil
		}
		targets = []model.BackupTarget{{StorageID: strings.TrimSpace(request.Destination), Role: "primary", Order: 0}}
	}
	if len(targets) == 1 && request.DestinationType == "local" && strings.TrimSpace(request.PrimaryDestinationID) == "" {
		if storage, err := s.store.Storage(targets[0].StorageID); err == nil {
			request.BackupTargets = []model.BackupTarget{{StorageID: storage.ID, Role: "primary", Order: 0}}
			request.PrimaryDestinationID = storage.ID
			request.Destination = storage.ID
			request.TargetStorages = []model.RemoteStorage{storage}
			return request.TargetStorages, nil
		}
		request.TargetStorages = nil
		return nil, nil
	}
	if len(targets) == 0 {
		return nil, fmt.Errorf("未找到备份目标存储，请重新选择")
	}
	storages := make([]model.RemoteStorage, 0, len(targets))
	for index := range targets {
		storage, err := s.store.Storage(targets[index].StorageID)
		if err != nil {
			return nil, fmt.Errorf("未找到备份目标存储，请重新选择")
		}
		if storage.Type != "object" && storage.Type != "webdav" && storage.Type != "one_drive" && storage.Type != "local" {
			return nil, fmt.Errorf("存储 %q 暂不支持备份写入", storage.Name)
		}
		targets[index].StorageID = storage.ID
		storages = append(storages, storage)
	}
	request.BackupTargets = targets
	request.TargetStorages = storages
	request.PrimaryDestinationID = primaryBackupTargetID(targets)
	request.Destination = request.PrimaryDestinationID
	request.Storage = nil
	if len(storages) == 1 {
		storage := storages[0]
		request.DestinationType = storage.Type
		if storage.Type == "object" || storage.Type == "webdav" || storage.Type == "one_drive" {
			request.Storage = &storage
		}
		return storages, nil
	}
	request.DestinationType = "multi"
	return storages, nil
}

func normalizeBackupTargets(request model.BackupRequest) []model.BackupTarget {
	seen := map[string]bool{}
	targets := make([]model.BackupTarget, 0, len(request.BackupTargets)+1)
	for _, target := range request.BackupTargets {
		id := strings.TrimSpace(target.StorageID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		target.StorageID = id
		target.Role = strings.TrimSpace(strings.ToLower(target.Role))
		targets = append(targets, target)
	}
	if len(targets) == 0 && strings.TrimSpace(request.Destination) != "" {
		targets = append(targets, model.BackupTarget{StorageID: strings.TrimSpace(request.Destination)})
	}
	primary := strings.TrimSpace(request.PrimaryDestinationID)
	if primary == "" {
		for _, target := range targets {
			if target.Role == "primary" {
				primary = target.StorageID
				break
			}
		}
	}
	if primary == "" && len(targets) > 0 {
		primary = targets[0].StorageID
	}
	for index := range targets {
		if targets[index].StorageID == primary {
			targets[index].Role = "primary"
			targets[index].Order = 0
			continue
		}
		targets[index].Role = "secondary"
		if targets[index].Order <= 0 {
			targets[index].Order = index + 1
		}
	}
	sortBackupTargets(targets)
	return targets
}

func sortBackupTargets(targets []model.BackupTarget) {
	sort.SliceStable(targets, func(i, j int) bool {
		if targets[i].Role == "primary" && targets[j].Role != "primary" {
			return true
		}
		if targets[j].Role == "primary" && targets[i].Role != "primary" {
			return false
		}
		return targets[i].Order < targets[j].Order
	})
}

func primaryBackupTargetID(targets []model.BackupTarget) string {
	for _, target := range targets {
		if target.Role == "primary" {
			return target.StorageID
		}
	}
	if len(targets) > 0 {
		return targets[0].StorageID
	}
	return ""
}

func backupTargetsLabel(storages []model.RemoteStorage) string {
	if len(storages) == 0 {
		return ""
	}
	labels := make([]string, 0, len(storages))
	for index, storage := range storages {
		label := storage.Name
		if storage.Type == "object" && storage.Bucket != "" {
			label += " / " + storage.Bucket
		} else if storage.Type == "local" && storage.Path != "" {
			label += " / " + storage.Path
		}
		if index == 0 {
			label += "（主）"
		}
		labels = append(labels, label)
	}
	return strings.Join(labels, "、")
}

func (s *Server) backfillBackupRequest(item model.BackupJob) (model.BackupRequest, error) {
	request := item.Request
	if request.SourceID == "" {
		connections, err := s.store.Connections()
		if err != nil {
			return request, fmt.Errorf("读取源端连接失败")
		}
		for _, connection := range connections {
			if connection.Name == item.SourceName {
				request.SourceID = connection.ID
				break
			}
		}
	}
	if request.SourceDatabase == "" {
		request.SourceDatabase = item.SourceDatabase
	}
	if len(request.SourceDatabases) == 0 {
		request.SourceDatabases = backupDatabases(request)
	}
	if request.DestinationType == "" {
		request.DestinationType = item.DestinationType
	}
	if request.Destination == "" {
		storages, err := s.store.Storages()
		if err != nil {
			return request, fmt.Errorf("读取备份目标失败")
		}
		for _, storage := range storages {
			if item.Destination == storage.Name+" / "+storage.Bucket || item.Destination == storage.Name+" / "+storage.Path || item.Destination == storage.Name {
				request.Destination = storage.ID
				break
			}
		}
	}
	if len(request.BackupTargets) == 0 && strings.TrimSpace(request.Destination) != "" {
		request.BackupTargets = []model.BackupTarget{{StorageID: strings.TrimSpace(request.Destination), Role: "primary", Order: 0}}
	}
	if request.PrimaryDestinationID == "" && len(request.BackupTargets) > 0 {
		request.PrimaryDestinationID = primaryBackupTargetID(normalizeBackupTargets(request))
	}
	if request.ScheduleMode == "" {
		request.ScheduleMode = item.ScheduleMode
	}
	if request.ScheduleFrequency == "" {
		request.ScheduleFrequency = item.ScheduleFrequency
	}
	if request.ScheduleTime == "" {
		request.ScheduleTime = item.ScheduleTime
	}
	if request.ScheduleWeekday == 0 {
		request.ScheduleWeekday = item.ScheduleWeekday
	}
	if request.ScheduleMonthDay == 0 {
		request.ScheduleMonthDay = item.ScheduleMonthDay
	}
	if request.RetentionMonths == 0 {
		request.RetentionMonths = item.RetentionMonths
	}
	if request.RetentionDays == 0 {
		request.RetentionDays = item.RetentionDays
	}
	if !request.Notification.Enabled && request.Notification.Method == "" && request.Notification.Target == "" {
		request.Notification.Enabled = item.Notification.Enabled || item.NotificationEnabled
		if item.Notification.Method != "" || item.Notification.Target != "" {
			request.Notification.Method = item.Notification.Method
			request.Notification.Target = item.Notification.Target
		} else {
			request.Notification.Method = item.NotificationMethod
			request.Notification.Target = item.NotificationTarget
		}
	}
	if len(request.Notification.RecipientIDs) == 0 && len(item.NotificationRecipientIDs) > 0 {
		request.Notification.RecipientIDs = append([]string(nil), item.NotificationRecipientIDs...)
	}
	if request.Notification.Trigger == "" && request.NotificationTrigger == "" {
		if item.Notification.Trigger != "" {
			request.Notification.Trigger = item.Notification.Trigger
		} else {
			request.Notification.Trigger = item.NotificationTrigger
		}
	}
	if request.Name == "" {
		request.Name = item.Name
	}
	if request.SourceID == "" {
		return request, fmt.Errorf("未找到源端连接，请重新配置定时备份")
	}
	if request.Destination == "" {
		return request, fmt.Errorf("未找到备份目标存储，请重新配置定时备份")
	}
	if _, err := s.hydrateBackupTargets(&request); err != nil {
		return request, err
	}
	return request, nil
}

func normalizeBackupSchedule(request *model.BackupRequest) {
	if request.ScheduleMode != "scheduled" {
		request.ScheduleMode = "once"
		request.ScheduleFrequency = ""
		request.ScheduleTime = ""
		request.ScheduleWeekday = 0
		request.ScheduleMonthDay = 0
		request.RetentionDays = 0
		request.RetentionMonths = 0
		request.Notification = model.BackupNotification{}
		request.NotificationEnabled = false
		request.NotificationMethod = ""
		request.NotificationTarget = ""
		request.NotificationRecipientIDs = nil
		request.NotificationTrigger = ""
		return
	}
	if request.ScheduleFrequency == "" {
		request.ScheduleFrequency = "daily"
	}
	if request.ScheduleTime == "" {
		request.ScheduleTime = "02:00:00"
	}
	if request.ScheduleWeekday < 0 || request.ScheduleWeekday > 6 {
		request.ScheduleWeekday = 1
	}
	if request.ScheduleMonthDay < 1 || request.ScheduleMonthDay > 28 {
		request.ScheduleMonthDay = 1
	}
	request.RetentionDays = backupRetentionDays(request.RetentionDays, request.RetentionMonths)
	if request.RetentionDays <= 0 {
		request.RetentionDays = 30
	}
	request.RetentionMonths = retentionMonthsForCompatibility(request.RetentionDays)
	normalizeBackupNotification(request)
	if request.Notification.Enabled {
		if len(request.Notification.RecipientIDs) == 0 && request.Notification.Method != "webhook" && request.Notification.Method != "email" {
			request.Notification.Method = "webhook"
		}
		request.Notification.Target = strings.TrimSpace(request.Notification.Target)
		if request.Notification.Target == "" && len(request.Notification.RecipientIDs) == 0 {
			request.Notification.Enabled = false
		}
	}
	request.NotificationEnabled = request.Notification.Enabled
	request.NotificationMethod = request.Notification.Method
	request.NotificationTarget = request.Notification.Target
	request.NotificationRecipientIDs = append([]string(nil), request.Notification.RecipientIDs...)
	request.NotificationTrigger = request.Notification.Trigger
}

func (s *Server) normalizeMongoOplogBackupRequest(ctx context.Context, request *model.BackupRequest, source model.Connection) error {
	request.BackupMode = strings.ToLower(strings.TrimSpace(request.BackupMode))
	if request.BackupMode == "" || request.BackupMode == "full" {
		request.BackupMode = ""
		request.FullBackupIntervalDays = 0
		return nil
	}
	if request.BackupMode != mongoOplogBackupMode {
		return fmt.Errorf("不支持的备份策略")
	}
	if source.Type != "MongoDB" {
		return fmt.Errorf("oplog 增量备份仅支持 MongoDB 副本集")
	}
	if request.ScheduleMode != "scheduled" {
		return fmt.Errorf("MongoDB oplog 增量备份必须创建为定时任务")
	}
	if request.FullBackupIntervalDays <= 0 {
		request.FullBackupIntervalDays = defaultMongoFullBackupIntervalDays
	}
	if request.FullBackupIntervalDays < 1 || request.FullBackupIntervalDays > 30 {
		return fmt.Errorf("MongoDB 全量基线间隔必须在 1 到 30 天之间")
	}
	if request.RestoreDrillEnabled {
		target, err := s.store.Connection(strings.TrimSpace(request.RestoreDrillTargetID))
		if err != nil || target.Type != "MongoDB" || target.ID == source.ID {
			return fmt.Errorf("恢复演练必须选择不同于源端的 MongoDB 目标副本集")
		}
	}
	checkCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	info, err := mongo.IncrementalBackupInfo(checkCtx, source)
	if err != nil {
		return userFacingMongoOplogPrecheckError(err)
	}
	if mongo.OplogTimestampCompare(info.Oldest, info.Newest) >= 0 {
		return fmt.Errorf("oplog 没有可用的时间窗口，请确认副本集处于正常写入状态")
	}
	if windowHours, requiredHours := mongoOplogWindowHours(info), mongoOplogRequirementHours(*request, 0); windowHours < requiredHours {
		return fmt.Errorf("oplog 窗口仅 %.1f 小时，低于当前备份策略要求的 %.1f 小时；请扩容 oplog 后再启用增量备份", windowHours, requiredHours)
	}
	// MongoDB only guarantees replayable oplog backups for a complete replica
	// set dump. Keep this explicit in the task metadata and the UI.
	request.SourceDatabase = mongoReplicaSetScope
	request.SourceDatabases = []string{mongoReplicaSetScope}
	return nil
}

func userFacingMongoOplogPrecheckError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("MongoDB oplog 增量备份预检失败：%w", err)
}

func mongoRPOHours(request model.BackupRequest) float64 {
	switch request.ScheduleFrequency {
	case "hourly":
		return 1
	case "daily":
		return 24
	case "weekly":
		return 7 * 24
	case "monthly":
		return 28 * 24
	default:
		return 24
	}
}

func mongoOplogRequirementHours(request model.BackupRequest, fullDuration time.Duration) float64 {
	required := mongoRPOHours(request) + fullDuration.Hours() + mongoOplogSafetyBufferHours
	if required < minimumMongoOplogWindowHours {
		return minimumMongoOplogWindowHours
	}
	return required
}

func mongoOplogWindowHours(info mongo.OplogBackupInfo) float64 {
	if info.Newest.Seconds < info.Oldest.Seconds {
		return 0
	}
	return float64(info.Newest.Seconds-info.Oldest.Seconds) / 3600
}

func normalizeBackupNotification(request *model.BackupRequest) {
	if len(request.Notification.RecipientIDs) == 0 && len(request.NotificationRecipientIDs) > 0 {
		request.Notification.RecipientIDs = append([]string(nil), request.NotificationRecipientIDs...)
	}
	if len(request.Notification.RecipientIDs) > 0 {
		request.Notification.Enabled = true
	}
	if request.Notification.Enabled || request.Notification.Method != "" || request.Notification.Target != "" {
		if request.Notification.Method == "" {
			request.Notification.Method = request.NotificationMethod
		}
		if request.Notification.Target == "" {
			request.Notification.Target = request.NotificationTarget
		}
		request.Notification.Enabled = request.Notification.Enabled || request.NotificationEnabled
	}
	if request.Notification.Trigger == "" {
		request.Notification.Trigger = request.NotificationTrigger
	}
	if request.Notification.Method == "" {
		request.Notification.Method = request.NotificationMethod
	}
	if request.Notification.Target == "" {
		request.Notification.Target = request.NotificationTarget
	}
	if request.Notification.Enabled == false {
		request.Notification.Enabled = request.NotificationEnabled
	}
	request.Notification.Trigger = normalizeBackupNotificationTrigger(request.Notification.Trigger)
	request.NotificationEnabled = request.Notification.Enabled
	request.NotificationMethod = request.Notification.Method
	request.NotificationTarget = request.Notification.Target
	request.NotificationRecipientIDs = append([]string(nil), request.Notification.RecipientIDs...)
	request.NotificationTrigger = request.Notification.Trigger
}

func nextBackupRun(request model.BackupRequest, after time.Time) (time.Time, error) {
	after = after.In(appLocation)
	hour, minute, second, err := parseScheduleTime(request.ScheduleTime)
	if err != nil {
		return time.Time{}, err
	}
	location := appLocation
	candidate := time.Date(after.Year(), after.Month(), after.Day(), hour, minute, second, 0, location)
	switch request.ScheduleFrequency {
	case "hourly":
		candidate = time.Date(after.Year(), after.Month(), after.Day(), after.Hour(), minute, second, 0, location)
		if !candidate.After(after) {
			candidate = candidate.Add(time.Hour)
		}
		return candidate, nil
	case "daily":
		if !candidate.After(after) {
			candidate = candidate.AddDate(0, 0, 1)
		}
		return candidate, nil
	case "weekly":
		weekday := time.Weekday(request.ScheduleWeekday)
		days := (int(weekday) - int(candidate.Weekday()) + 7) % 7
		candidate = candidate.AddDate(0, 0, days)
		if !candidate.After(after) {
			candidate = candidate.AddDate(0, 0, 7)
		}
		return candidate, nil
	case "monthly":
		day := request.ScheduleMonthDay
		candidate = time.Date(after.Year(), after.Month(), min(day, daysInMonth(after.Year(), after.Month())), hour, minute, second, 0, location)
		if !candidate.After(after) {
			nextMonth := after.AddDate(0, 1, 0)
			candidate = time.Date(nextMonth.Year(), nextMonth.Month(), min(day, daysInMonth(nextMonth.Year(), nextMonth.Month())), hour, minute, second, 0, location)
		}
		return candidate, nil
	default:
		return time.Time{}, fmt.Errorf("不支持的定时频率")
	}
}

func parseScheduleTime(value string) (int, int, int, error) {
	parts := strings.Split(value, ":")
	if len(parts) != 2 && len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("请填写有效的执行时间")
	}
	hour, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("请填写有效的执行时间")
	}
	minute, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, 0, fmt.Errorf("请填写有效的执行时间")
	}
	second := 0
	if len(parts) == 3 {
		second, err = strconv.Atoi(parts[2])
		if err != nil {
			return 0, 0, 0, fmt.Errorf("请填写有效的执行时间")
		}
	}
	if hour < 0 || hour > 23 || minute < 0 || minute > 59 || second < 0 || second > 59 {
		return 0, 0, 0, fmt.Errorf("请填写有效的执行时间")
	}
	return hour, minute, second, nil
}

func daysInMonth(year int, month time.Month) int {
	return time.Date(year, month+1, 0, 0, 0, 0, 0, appLocation).Day()
}

func (s *Server) getBackup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, err := s.store.Backup(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "backup not found")
		return
	}
	s.reconcileScheduledBackupRun(item, time.Now().In(appLocation))
	if compactBackupResponse(r) {
		item = compactBackupJob(item)
	}
	writeJSON(w, http.StatusOK, item)
}

func compactBackupResponse(r *http.Request) bool {
	value := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("compact")))
	return value == "1" || value == "true"
}

func compactBackupJob(job model.BackupJob) model.BackupJob {
	if job.SourceID == "" {
		job.SourceID = job.Request.SourceID
	}
	job.Request = model.BackupRequest{}
	job.Events = compactBackupSummaryEvents(job.Events)
	return job
}

func compactBackupListJob(job model.BackupJob) model.BackupJob {
	if job.SourceID == "" {
		job.SourceID = job.Request.SourceID
	}
	job.Request = model.BackupRequest{}
	job.Events = compactEvents(job.Events)
	return job
}

func appendJobEvent(events *[]model.Event, event model.Event) {
	if strings.TrimSpace(event.Message) == "" {
		return
	}
	event.Message = truncateEventMessage(event.Message)
	*events = append(*events, event)
	if len(*events) > maxStoredEvents {
		*events = (*events)[len(*events)-maxStoredEvents:]
	}
}

func compactEvents(events []model.Event) []model.Event {
	if len(events) <= maxCompactEvents {
		return compactEventMessages(events)
	}
	return compactEventMessages(events[len(events)-maxCompactEvents:])
}

func compactBackupSummaryEvents(events []model.Event) []model.Event {
	var items []model.Event
	for _, event := range events {
		if isBackupRunSummaryEvent(event) {
			items = append(items, event)
		}
	}
	if len(items) == 0 {
		return compactEvents(events)
	}
	return compactEventMessages(items)
}

func isBackupRunSummaryEvent(event model.Event) bool {
	return strings.Contains(event.Message, "定时备份已触发") ||
		strings.Contains(event.Message, "手动触发定时备份") ||
		strings.Contains(event.Message, "备份任务完成") ||
		strings.Contains(event.Message, "下一次备份将在") ||
		strings.Contains(event.Message, "任务已取消") ||
		event.Level == "ERROR"
}

func compactEventMessages(events []model.Event) []model.Event {
	if len(events) == 0 {
		return events
	}
	items := make([]model.Event, len(events))
	for index, event := range events {
		event.Message = truncateEventMessage(event.Message)
		items[index] = event
	}
	return items
}

func truncateEventMessage(message string) string {
	message = strings.TrimSpace(message)
	runes := []rune(message)
	if len(runes) <= maxStoredEventMessage {
		return message
	}
	const marker = "\n...中间内容已截断，已保留开头和末尾...\n"
	markerLength := len([]rune(marker))
	headLength := maxStoredEventMessage / 4
	tailLength := maxStoredEventMessage - headLength - markerLength
	if tailLength <= 0 {
		return string(runes[len(runes)-maxStoredEventMessage:])
	}
	return strings.TrimSpace(string(runes[:headLength])) + marker + strings.TrimSpace(string(runes[len(runes)-tailLength:]))
}

func (s *Server) updateBackup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	current, err := s.store.Backup(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "backup not found")
		return
	}
	if current.ScheduleMode != "scheduled" || current.Status == model.StatusRunning || current.Status == model.StatusPending {
		writeError(w, http.StatusBadRequest, "invalid_request", "仅已计划的定时备份可以编辑")
		return
	}
	var input model.BackupRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	request, err := s.backfillBackupRequest(current)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if strings.TrimSpace(input.Name) != "" {
		request.Name = strings.TrimSpace(input.Name)
	}
	if strings.TrimSpace(input.SourceID) != "" {
		source, err := s.store.Connection(input.SourceID)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_source", "未找到源端连接，请重新选择")
			return
		}
		request.SourceID = source.ID
	}
	if len(input.SourceDatabases) > 0 {
		request.SourceDatabases = append([]string(nil), input.SourceDatabases...)
		request.SourceDatabase = strings.Join(backupDatabases(request), ", ")
	} else if strings.TrimSpace(input.SourceDatabase) != "" {
		request.SourceDatabase = strings.TrimSpace(input.SourceDatabase)
		request.SourceDatabases = splitDatabaseList(input.SourceDatabase)
	}
	if len(input.BackupTargets) > 0 || strings.TrimSpace(input.Destination) != "" {
		request.DestinationType = input.DestinationType
		request.Destination = input.Destination
		request.PrimaryDestinationID = input.PrimaryDestinationID
		request.BackupTargets = append([]model.BackupTarget(nil), input.BackupTargets...)
		request.Storage = nil
		request.TargetStorages = nil
		if _, err := s.hydrateBackupTargets(&request); err != nil {
			writeError(w, http.StatusBadRequest, "invalid_destination", err.Error())
			return
		}
	}
	request.ScheduleMode = "scheduled"
	request.ScheduleFrequency = input.ScheduleFrequency
	request.ScheduleTime = input.ScheduleTime
	request.ScheduleWeekday = input.ScheduleWeekday
	request.ScheduleMonthDay = input.ScheduleMonthDay
	if input.BackupMode != "" {
		request.BackupMode = input.BackupMode
	}
	if input.FullBackupIntervalDays != 0 {
		request.FullBackupIntervalDays = input.FullBackupIntervalDays
	}
	request.RestoreDrillEnabled = input.RestoreDrillEnabled
	request.RestoreDrillTargetID = input.RestoreDrillTargetID
	request.RetentionDays = input.RetentionDays
	request.RetentionMonths = input.RetentionMonths
	request.NotificationEnabled = input.NotificationEnabled
	request.NotificationMethod = input.NotificationMethod
	request.NotificationTarget = input.NotificationTarget
	request.NotificationRecipientIDs = append([]string(nil), input.NotificationRecipientIDs...)
	request.NotificationTrigger = input.NotificationTrigger
	request.Notification = input.Notification
	normalizeBackupSchedule(&request)
	now := time.Now().In(appLocation)
	next, err := nextBackupRun(request, now)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_schedule", err.Error())
		return
	}
	source, err := s.store.Connection(request.SourceID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_source", "未找到源端连接，请重新选择")
		return
	}
	if err := s.normalizeMongoOplogBackupRequest(r.Context(), &request, source); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_backup_mode", err.Error())
		return
	}
	if err := s.store.UpdateBackup(id, func(item *model.BackupJob) {
		item.Name = request.Name
		item.SourceName = source.Name
		item.SourceID = source.ID
		item.SourceType = source.Type
		item.SourceDatabase = request.SourceDatabase
		item.DestinationType = request.DestinationType
		item.BackupTargets = append([]model.BackupTarget(nil), request.BackupTargets...)
		item.PrimaryDestinationID = request.PrimaryDestinationID
		if len(request.TargetStorages) > 0 {
			item.Destination = backupTargetsLabel(request.TargetStorages)
		}
		item.Request = request
		item.ScheduleMode = request.ScheduleMode
		item.ScheduleFrequency = request.ScheduleFrequency
		item.ScheduleTime = request.ScheduleTime
		item.ScheduleWeekday = request.ScheduleWeekday
		item.ScheduleMonthDay = request.ScheduleMonthDay
		item.BackupMode = request.BackupMode
		item.FullBackupIntervalDays = request.FullBackupIntervalDays
		item.RestoreDrillEnabled = request.RestoreDrillEnabled
		item.RestoreDrillTargetID = request.RestoreDrillTargetID
		item.RetentionDays = request.RetentionDays
		item.RetentionMonths = request.RetentionMonths
		item.Notification = request.Notification
		item.NotificationEnabled = request.Notification.Enabled
		item.NotificationMethod = request.Notification.Method
		item.NotificationTarget = request.Notification.Target
		item.NotificationRecipientIDs = append([]string(nil), request.Notification.RecipientIDs...)
		item.NotificationTrigger = request.Notification.Trigger
		if item.SchedulePaused {
			item.NextRunAt = nil
		} else {
			item.NextRunAt = &next
		}
		appendJobEvent(&item.Events, model.Event{Time: now, Level: "INFO", Message: "定时备份计划已更新"})
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateBackup, err))
		return
	}
	updated, err := s.store.Backup(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateBackup, err))
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) triggerBackup(w http.ResponseWriter, r *http.Request) {
	item, err := s.store.Backup(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "backup not found")
		return
	}
	updated, err := s.triggerScheduledBackup(item, "手动触发定时备份")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	writeJSON(w, http.StatusAccepted, updated)
}

func (s *Server) pauseBackupSchedule(w http.ResponseWriter, r *http.Request) {
	s.setBackupSchedulePaused(w, r, true)
}

func (s *Server) resumeBackupSchedule(w http.ResponseWriter, r *http.Request) {
	s.setBackupSchedulePaused(w, r, false)
}

func (s *Server) setBackupSchedulePaused(w http.ResponseWriter, r *http.Request, paused bool) {
	id := r.PathValue("id")
	item, err := s.store.Backup(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "backup not found")
		return
	}
	if item.ScheduleMode != "scheduled" {
		writeError(w, http.StatusBadRequest, "invalid_request", "仅定时备份可以暂停或恢复调度")
		return
	}
	if item.SchedulePaused == paused {
		writeJSON(w, http.StatusOK, item)
		return
	}
	var next *time.Time
	if !paused {
		request := backupScheduleRequest(item)
		value, err := nextBackupRun(request, time.Now().In(appLocation))
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_schedule", err.Error())
			return
		}
		next = &value
	}
	now := time.Now().In(appLocation)
	if err := s.store.UpdateBackup(id, func(job *model.BackupJob) {
		job.SchedulePaused = paused
		job.NextRunAt = next
		message := "定时备份调度已恢复"
		if paused {
			message = "定时备份调度已暂停"
		}
		appendJobEvent(&job.Events, model.Event{Time: now, Level: "INFO", Message: message})
	}); err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateBackup, err))
		return
	}
	updated, err := s.store.Backup(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateBackup, err))
		return
	}
	writeJSON(w, http.StatusOK, updated)
}

func (s *Server) restoreBackup(w http.ResponseWriter, r *http.Request) {
	sourceJob, err := s.store.Backup(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "backup not found")
		return
	}
	var input model.RestoreBackupRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.TargetDatabase = strings.TrimSpace(input.TargetDatabase)
	plan, err := s.buildRestorePlan(sourceJob, input)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	if plan.TargetID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "请选择恢复目标连接")
		return
	}
	restoreJob, err := backupJobForArtifactVersion(sourceJob, plan.ArtifactVersionID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_artifact", err.Error())
		return
	}
	if !backupArtifactRestorable(restoreJob) {
		writeError(w, http.StatusBadRequest, "invalid_request", backupArtifactUnavailableMessage(restoreJob))
		return
	}
	restoreSources, err := s.restoreSourcesForPlan(sourceJob, plan)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_artifact", err.Error())
		return
	}
	restoreSource := restoreSources[len(restoreSources)-1]
	target, err := s.store.Connection(plan.TargetID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_target", "未找到目标连接，请重新选择")
		return
	}
	if target.Type != sourceJob.SourceType {
		writeError(w, http.StatusBadRequest, "invalid_target", "恢复目标类型必须与备份源类型一致")
		return
	}
	precheck := s.runRestorePrecheck(r.Context(), sourceJob, target, plan)
	if !precheck.OK {
		writeJSON(w, http.StatusConflict, map[string]any{
			"code":    "restore_precheck_failed",
			"message": "恢复前检查未通过，请处理后再重试",
			"checks":  precheck.Checks,
		})
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	now := time.Now().In(appLocation)
	summaryArtifact := restoreArtifactSummary(restoreJob)
	value := model.BackupJob{
		ID: store.NewID(), Name: fmt.Sprintf("恢复 %s 到 %s", strings.Join(plan.SourceDatabases, ", "), plan.TargetLabel),
		Status: model.StatusPending, Stage: "pending", Progress: 0, StartedAt: now,
		SourceName: sourceJob.SourceName, SourceID: sourceJob.SourceID, SourceType: sourceJob.SourceType, SourceDatabase: strings.Join(plan.SourceDatabases, ", "),
		DestinationType: "restore", Destination: target.Name + " / " + plan.TargetLabel,
		ArtifactName: summaryArtifact.Name, ArtifactSize: summaryArtifact.Size, ArtifactPath: restoreSource.Path,
		Events: []model.Event{{Time: now, Level: "INFO", Message: "恢复任务已创建，来源备份任务 " + sourceJob.ID}, {Time: now, Level: "INFO", Message: "恢复备份版本：" + restoreVersionLabel(restoreJob, plan.ArtifactVersionID)}, {Time: now, Level: "INFO", Message: "恢复策略：" + restoreStrategyLabel(plan.Strategy)}},
	}
	if err := s.store.AddBackup(value, cancel); err != nil {
		cancel()
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateBackup, err))
		return
	}
	go s.runRestore(ctx, value.ID, target, plan.SourceDatabases, plan.TargetDatabases, plan.Strategy, restoreSources, plan.MongoReplicaSet)
	writeJSON(w, http.StatusAccepted, value)
}

func (s *Server) restoreSourcesForPlan(sourceJob model.BackupJob, plan restorePlan) ([]restoreSource, error) {
	if !plan.MongoReplicaSet {
		restoreJob, err := backupJobForArtifactVersion(sourceJob, plan.ArtifactVersionID)
		if err != nil {
			return nil, err
		}
		source, err := s.restoreSource(restoreJob)
		if err != nil {
			return nil, err
		}
		return []restoreSource{source}, nil
	}
	sources := make([]restoreSource, 0, len(plan.MongoVersionIDs))
	for _, versionID := range plan.MongoVersionIDs {
		versionJob, err := backupJobForArtifactVersion(sourceJob, versionID)
		if err != nil {
			return nil, err
		}
		if !backupArtifactRestorable(versionJob) {
			return nil, fmt.Errorf("%s", backupArtifactUnavailableMessage(versionJob))
		}
		source, err := s.restoreSource(versionJob)
		if err != nil {
			return nil, err
		}
		sources = append(sources, source)
	}
	if len(sources) == 0 {
		return nil, fmt.Errorf("MongoDB 备份链没有可恢复产物")
	}
	return sources, nil
}

func (s *Server) restoreBackupPrecheck(w http.ResponseWriter, r *http.Request) {
	sourceJob, err := s.store.Backup(r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "backup not found")
		return
	}
	var input model.RestoreBackupRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.TargetDatabase = strings.TrimSpace(input.TargetDatabase)
	plan, err := s.buildRestorePlan(sourceJob, input)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	target, targetErr := s.store.Connection(plan.TargetID)
	if targetErr != nil {
		writeJSON(w, http.StatusOK, model.RestorePrecheckResult{OK: false, Checks: []model.RestorePrecheckItem{{
			Key: "target", Label: "目标连接", Status: "failed", Message: "未找到目标连接，请重新选择",
		}}})
		return
	}
	if target.Type != sourceJob.SourceType {
		writeJSON(w, http.StatusOK, model.RestorePrecheckResult{OK: false, Checks: []model.RestorePrecheckItem{{
			Key: "target_type", Label: "目标类型", Status: "failed", Message: "恢复目标类型必须与备份源类型一致",
		}}})
		return
	}
	result := s.runRestorePrecheck(r.Context(), sourceJob, target, plan)
	writeJSON(w, http.StatusOK, result)
}

type restorePlan struct {
	TargetID          string
	ArtifactVersionID string
	SourceDatabases   []string
	TargetDatabases   map[string]string
	Strategy          string
	TargetLabel       string
	MongoReplicaSet   bool
	MongoVersionIDs   []string
}

func (s *Server) buildRestorePlan(sourceJob model.BackupJob, input model.RestoreBackupRequest) (restorePlan, error) {
	if sourceJob.SourceType == "etcd" {
		return restorePlan{}, fmt.Errorf("etcd 快照暂不支持在 DataBridge 内自动恢复，请下载产物后按 etcdutl 离线恢复流程操作")
	}
	if isMongoOplogBackup(sourceJob) {
		return buildMongoOplogRestorePlan(sourceJob, input)
	}
	strategy := normalizeRestoreStrategy(input.Strategy)
	restoreJob, err := backupJobForArtifactVersion(sourceJob, strings.TrimSpace(input.ArtifactVersionID))
	if err != nil {
		return restorePlan{}, err
	}
	sourceDatabases := restoreSourceDatabasesForJob(restoreJob)
	if len(sourceDatabases) == 0 {
		sourceDatabases = splitDatabaseList(restoreJob.SourceDatabase)
	}
	selectedDatabases := input.SelectedDatabases
	if len(selectedDatabases) == 0 {
		selectedDatabases = input.SourceDatabases
	}
	selected, err := selectedRestoreDatabases(sourceDatabases, selectedDatabases)
	if err != nil {
		return restorePlan{}, err
	}
	targetDatabases, err := restoreTargetDatabases(selected, input)
	if err != nil {
		return restorePlan{}, err
	}
	return restorePlan{
		TargetID:          strings.TrimSpace(input.TargetID),
		ArtifactVersionID: strings.TrimSpace(input.ArtifactVersionID),
		SourceDatabases:   selected,
		TargetDatabases:   targetDatabases,
		Strategy:          strategy,
		TargetLabel:       restoreTargetLabel(selected, targetDatabases),
	}, nil
}

func isMongoOplogBackup(job model.BackupJob) bool {
	return job.SourceType == "MongoDB" && job.BackupMode == mongoOplogBackupMode
}

func buildMongoOplogRestorePlan(sourceJob model.BackupJob, input model.RestoreBackupRequest) (restorePlan, error) {
	if strings.TrimSpace(input.TargetID) == "" {
		return restorePlan{}, fmt.Errorf("请选择恢复目标连接")
	}
	if input.TargetID == sourceJob.SourceID {
		return restorePlan{}, fmt.Errorf("MongoDB 副本集备份不能恢复到源端连接，请选择独立的目标副本集")
	}
	versions, selectedID, err := mongoOplogRestoreVersions(sourceJob, strings.TrimSpace(input.ArtifactVersionID))
	if err != nil {
		return restorePlan{}, err
	}
	return restorePlan{
		TargetID:          strings.TrimSpace(input.TargetID),
		ArtifactVersionID: selectedID,
		SourceDatabases:   []string{mongoReplicaSetScope},
		TargetDatabases:   map[string]string{mongoReplicaSetScope: mongoReplicaSetScope},
		Strategy:          "recreate",
		TargetLabel:       mongoReplicaSetScope,
		MongoReplicaSet:   true,
		MongoVersionIDs:   versions,
	}, nil
}

func mongoOplogRestoreVersions(job model.BackupJob, requestedID string) ([]string, string, error) {
	completed := make([]model.BackupArtifactVersion, 0, len(job.ArtifactVersions))
	for _, version := range job.ArtifactVersions {
		if version.Status == model.StatusCompleted && len(version.Artifacts) > 0 && version.ChainID != "" {
			completed = append(completed, version)
		}
	}
	if len(completed) == 0 {
		return nil, "", fmt.Errorf("尚未找到完成的 MongoDB 全量基线或增量版本")
	}
	sort.Slice(completed, func(i, j int) bool { return completed[i].StartedAt.Before(completed[j].StartedAt) })
	selected := completed[len(completed)-1]
	if requestedID != "" && requestedID != "current" {
		found := false
		for _, version := range completed {
			if version.ID == requestedID {
				selected = version
				found = true
				break
			}
		}
		if !found {
			return nil, "", fmt.Errorf("未找到选择的备份版本")
		}
	}
	chain := make([]model.BackupArtifactVersion, 0)
	for _, version := range completed {
		if version.ChainID == selected.ChainID && !version.StartedAt.After(selected.StartedAt) {
			chain = append(chain, version)
		}
	}
	if len(chain) == 0 || chain[0].BackupMode != "full" {
		return nil, "", fmt.Errorf("MongoDB 备份链缺少可恢复的全量基线")
	}
	ids := make([]string, 0, len(chain))
	previous := ""
	for index, version := range chain {
		if index > 0 && (version.BackupMode != "incremental" || version.ParentID != previous) {
			return nil, "", fmt.Errorf("MongoDB 备份链不连续，不能恢复")
		}
		ids = append(ids, version.ID)
		previous = version.ID
	}
	return ids, selected.ID, nil
}

func (s *Server) runRestorePrecheck(ctx context.Context, sourceJob model.BackupJob, target model.Connection, plan restorePlan) model.RestorePrecheckResult {
	checks := []model.RestorePrecheckItem{}
	add := func(key, label, status, message string) {
		checks = append(checks, model.RestorePrecheckItem{Key: key, Label: label, Status: status, Message: message})
	}
	restoreJob, versionErr := backupJobForArtifactVersion(sourceJob, plan.ArtifactVersionID)
	if versionErr != nil {
		add("artifact", "备份产物", "failed", versionErr.Error())
		return restorePrecheckResult(checks)
	}
	if !backupArtifactRestorable(restoreJob) {
		add("artifact", "备份产物", "failed", backupArtifactUnavailableMessage(restoreJob))
		return restorePrecheckResult(checks)
	}
	if _, err := s.restoreSource(restoreJob); err != nil {
		add("artifact", "备份产物", "failed", err.Error())
	} else {
		add("artifact", "备份产物", "passed", "已找到可恢复的备份产物："+restoreVersionLabel(restoreJob, plan.ArtifactVersionID))
	}
	if err := restoreClientToolAvailable(target.Type); err != nil {
		add("client_tool", "客户端工具", "failed", userFacingErrorMessage(errorContextBackupRuntime, err))
	} else {
		add("client_tool", "客户端工具", "passed", restoreClientToolLabel(target.Type)+" 可用")
	}
	timeoutCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	if _, err := serverVersion(timeoutCtx, target); err != nil {
		add("target_connection", "目标连接", "failed", userFacingErrorMessage(errorContextConnectionTest, err))
		return restorePrecheckResult(checks)
	}
	add("target_connection", "目标连接", "passed", target.Name+" 连接可用")
	if plan.MongoReplicaSet {
		if _, err := s.restoreSourcesForPlan(sourceJob, plan); err != nil {
			add("backup_chain", "备份链", "failed", err.Error())
		} else {
			add("backup_chain", "备份链", "passed", fmt.Sprintf("已找到全量基线和 %d 个前序增量", len(plan.MongoVersionIDs)-1))
		}
		if _, err := mongo.IncrementalBackupInfo(timeoutCtx, target); err != nil {
			add("target_replica_set", "目标副本集", "failed", userFacingMongoOplogPrecheckError(err).Error())
		} else {
			add("target_replica_set", "目标副本集", "passed", "目标副本集和 oplog replay 所需访问已验证")
		}
		add("recreate_replica_set", "恢复策略", "warning", "将以副本集全量基线覆盖目标中的同名数据，并顺序重放增量日志")
		return restorePrecheckResult(checks)
	}
	if warning := s.neo4jRestoreVersionWarning(timeoutCtx, sourceJob, target); warning != "" {
		add("neo4j_version_compatibility", "版本兼容性", "warning", warning)
	}
	for _, sourceDatabase := range plan.SourceDatabases {
		targetDatabase := strings.TrimSpace(plan.TargetDatabases[sourceDatabase])
		if !validRestoreDatabaseNameForType(target.Type, targetDatabase) {
			add("database_name_"+sourceDatabase, "目标库名", "failed", fmt.Sprintf("目标数据库 %s 名称不合法", targetDatabase))
			continue
		}
		if target.Type == "MongoDB" {
			if plan.Strategy == "append" {
				if _, err := mongo.CollectionNames(timeoutCtx, target, targetDatabase); err != nil {
					add("target_database_"+sourceDatabase, "目标数据库", "failed", fmt.Sprintf("%s：%s", targetDatabase, userFacingErrorMessage(errorContextDatabaseDiscovery, err)))
				} else {
					add("target_database_"+sourceDatabase, "目标数据库", "passed", targetDatabase+" 可访问")
				}
			} else {
				add("recreate_"+sourceDatabase, "重建策略", "passed", targetDatabase+" 将在恢复前清空并重新导入")
			}
			continue
		}
		if target.Type == "Neo4j" {
			if _, err := neo4j.LabelsAndRelationshipTypes(timeoutCtx, target, targetDatabase); err != nil {
				add("target_database_"+sourceDatabase, "目标数据库", "failed", fmt.Sprintf("%s：%s", targetDatabase, userFacingErrorMessage(errorContextDatabaseDiscovery, err)))
			} else if plan.Strategy == "append" {
				add("target_database_"+sourceDatabase, "目标数据库", "passed", targetDatabase+" 可访问")
			} else {
				add("recreate_"+sourceDatabase, "清空策略", "passed", targetDatabase+" 将在恢复前清空图数据并重新导入")
			}
			continue
		}
		if plan.Strategy == "append" {
			if _, err := tables(timeoutCtx, target, targetDatabase); err != nil {
				add("target_database_"+sourceDatabase, "目标数据库", "failed", fmt.Sprintf("%s：%s", targetDatabase, userFacingErrorMessage(errorContextTableDiscovery, err)))
			} else {
				add("target_database_"+sourceDatabase, "目标数据库", "passed", targetDatabase+" 可访问")
			}
			continue
		}
		if err := checkRestoreRecreatePrivilege(timeoutCtx, target, targetDatabase); err != nil {
			add("recreate_"+sourceDatabase, "重建权限", "failed", fmt.Sprintf("%s：%s", targetDatabase, userFacingErrorMessage(errorContextBackupRuntime, err)))
		} else {
			add("recreate_"+sourceDatabase, "重建权限", "passed", targetDatabase+" 可执行重建前置检查")
		}
	}
	return restorePrecheckResult(checks)
}

func (s *Server) neo4jRestoreVersionWarning(ctx context.Context, sourceJob model.BackupJob, target model.Connection) string {
	if sourceJob.SourceType != "Neo4j" || target.Type != "Neo4j" {
		return ""
	}
	targetVersion := strings.TrimSpace(target.Version)
	if targetVersion == "" {
		version, err := serverVersion(ctx, target)
		if err != nil {
			return ""
		}
		targetVersion = version
	}
	sourceVersion := ""
	sourceConnectionID := strings.TrimSpace(sourceJob.Request.SourceID)
	if sourceConnectionID != "" {
		if source, err := s.store.Connection(sourceConnectionID); err == nil && source.Type == "Neo4j" {
			sourceVersion = strings.TrimSpace(source.Version)
		}
	}
	sourceMajor, sourceOK := majorVersion(sourceVersion)
	targetMajor, targetOK := majorVersion(targetVersion)
	if !sourceOK || !targetOK || sourceMajor == targetMajor {
		return ""
	}
	return fmt.Sprintf("当前备份来自 Neo4j %s，目标连接是 Neo4j %s。跨大版本恢复可能因 Cypher、约束或 APOC 导出语法差异而失败，建议优先恢复到相同大版本后再升级。", sourceVersion, targetVersion)
}

func majorVersion(version string) (int, bool) {
	return migrate.ParseMajorVersion(version)
}

func restorePrecheckResult(checks []model.RestorePrecheckItem) model.RestorePrecheckResult {
	result := model.RestorePrecheckResult{OK: true, Checks: checks}
	for _, check := range checks {
		if check.Status == "failed" {
			result.OK = false
			break
		}
		if check.Status == "warning" {
			result.Warnings = append(result.Warnings, model.RestoreWarning{
				Key:     check.Key,
				Label:   check.Label,
				Message: check.Message,
			})
		}
	}
	return result
}

func backupArtifactRestorable(job model.BackupJob) bool {
	if job.Status == model.StatusRunning || job.Status == model.StatusPending {
		return false
	}
	if strings.TrimSpace(job.ArtifactName) != "" {
		return true
	}
	for _, artifact := range job.Artifacts {
		if strings.TrimSpace(artifact.Name) != "" {
			return true
		}
	}
	return false
}

func backupArtifactUnavailableMessage(job model.BackupJob) string {
	if job.Status == model.StatusRunning || job.Status == model.StatusPending {
		return "任务正在执行，当前备份产物暂不可恢复"
	}
	return "只有已生成备份文件的任务可以恢复"
}

func backupJobForArtifactVersion(job model.BackupJob, versionID string) (model.BackupJob, error) {
	versionID = strings.TrimSpace(versionID)
	if versionID == "" || versionID == "current" {
		return job, nil
	}
	for _, version := range job.ArtifactVersions {
		if version.ID != versionID {
			continue
		}
		if version.Status != model.StatusCompleted {
			return model.BackupJob{}, fmt.Errorf("选择的备份版本未成功完成，不能用于恢复")
		}
		if len(version.Artifacts) == 0 {
			return model.BackupJob{}, fmt.Errorf("选择的备份版本没有可恢复产物")
		}
		next := job
		next.Artifacts = version.Artifacts
		next.ArtifactName = ""
		next.ArtifactSize = 0
		next.ArtifactPath = ""
		next.StartedAt = version.StartedAt
		next.FinishedAt = version.FinishedAt
		if len(version.Artifacts) > 0 {
			next.ArtifactName = version.Artifacts[0].Name
			next.ArtifactSize = version.Artifacts[0].Size
			next.ArtifactPath = version.Artifacts[0].Path
		}
		return next, nil
	}
	return model.BackupJob{}, fmt.Errorf("未找到选择的备份版本")
}

func restoreSourceDatabasesForJob(job model.BackupJob) []string {
	seen := map[string]bool{}
	var databases []string
	for _, artifact := range job.Artifacts {
		database := strings.TrimSpace(artifact.Database)
		if database != "" && !seen[database] {
			seen[database] = true
			databases = append(databases, database)
		}
	}
	if len(databases) > 0 {
		return databases
	}
	databases = backupDatabases(job.Request)
	if len(databases) > 0 {
		return databases
	}
	return splitDatabaseList(job.SourceDatabase)
}

func restoreVersionLabel(job model.BackupJob, versionID string) string {
	if strings.TrimSpace(versionID) == "" || versionID == "current" {
		if job.FinishedAt != nil {
			return job.FinishedAt.In(appLocation).Format("2006-01-02 15:04:05")
		}
		return "当前最新产物"
	}
	if job.FinishedAt != nil {
		return job.FinishedAt.In(appLocation).Format("2006-01-02 15:04:05")
	}
	return versionID
}

func validRestoreDatabaseName(name string) bool {
	if name == "" || len(name) > 63 {
		return false
	}
	for _, value := range name {
		if value < 32 || value == 127 || value == '/' || value == '\\' || value == '\x00' {
			return false
		}
	}
	return true
}

func validRestoreDatabaseNameForType(databaseType, name string) bool {
	if databaseType == "MongoDB" {
		return validMongoDatabaseName(name)
	}
	if databaseType == "Neo4j" {
		return validDatabaseName(name)
	}
	return validRestoreDatabaseName(name)
}

func restoreClientToolAvailable(databaseType string) error {
	switch databaseType {
	case "PostgreSQL":
		_, err := backup.ToolPathForPrecheck("psql")
		return err
	case "MySQL":
		_, err := backup.ToolPathForPrecheck("mysql")
		return err
	case "MongoDB":
		_, err := backup.ToolPathForPrecheck("mongorestore")
		return err
	case "Neo4j":
		_, err := backup.ToolPathForPrecheck("cypher-shell")
		return err
	default:
		return fmt.Errorf("unsupported restore target type %q", databaseType)
	}
}

func restoreClientToolLabel(databaseType string) string {
	if databaseType == "MySQL" {
		return "mysql"
	}
	if databaseType == "MongoDB" {
		return "mongorestore"
	}
	if databaseType == "Neo4j" {
		return "cypher-shell"
	}
	return "psql"
}

func checkRestoreRecreatePrivilege(ctx context.Context, target model.Connection, database string) error {
	switch target.Type {
	case "PostgreSQL":
		maintenanceDatabase := "postgres"
		if strings.EqualFold(strings.TrimSpace(database), maintenanceDatabase) {
			maintenanceDatabase = "template1"
		}
		conn, err := postgres.Connect(ctx, target, maintenanceDatabase)
		if err != nil {
			return err
		}
		defer conn.Close(ctx)
		var canCreate bool
		if err := conn.QueryRow(ctx, `SELECT rolsuper OR rolcreatedb FROM pg_roles WHERE rolname = current_user`).Scan(&canCreate); err != nil {
			return err
		}
		if !canCreate {
			return fmt.Errorf("permission denied: current PostgreSQL user does not have CREATEDB privilege")
		}
		var canDrop bool
		if err := conn.QueryRow(ctx, `SELECT NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1) OR EXISTS (
			SELECT 1 FROM pg_database d JOIN pg_roles r ON r.oid = d.datdba
			WHERE d.datname = $1 AND (r.rolname = current_user OR (SELECT rolsuper FROM pg_roles WHERE rolname = current_user))
		)`, database).Scan(&canDrop); err != nil {
			return err
		}
		if !canDrop {
			return fmt.Errorf("permission denied: current PostgreSQL user is not owner of target database %s", database)
		}
		return nil
	case "MySQL":
		conn, err := mysql.Connect(ctx, target, "")
		if err != nil {
			return err
		}
		defer conn.Close()
		rows, err := conn.QueryContext(ctx, `SHOW GRANTS`)
		if err != nil {
			return err
		}
		defer rows.Close()
		grants := ""
		for rows.Next() {
			var grant string
			if err := rows.Scan(&grant); err != nil {
				return err
			}
			grants += strings.ToUpper(grant) + "\n"
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if strings.Contains(grants, "ALL PRIVILEGES") || (strings.Contains(grants, "CREATE") && strings.Contains(grants, "DROP")) {
			return nil
		}
		return fmt.Errorf("permission denied: current MySQL user does not have CREATE and DROP privileges")
	default:
		return fmt.Errorf("unsupported restore target type %q", target.Type)
	}
}

type restoreSource struct {
	Type      string
	Path      string
	Object    string
	Storage   *model.RemoteStorage
	Artifacts map[string]model.BackupArtifact
	Copies    map[string][]restoreArtifactCopy
}

type restoreArtifactCopy struct {
	Type    string
	Path    string
	Object  string
	Storage *model.RemoteStorage
	Label   string
}

func (s *Server) restoreSource(job model.BackupJob) (restoreSource, error) {
	switch job.DestinationType {
	case "local":
		artifactPath := restoreArtifactPath(job)
		if artifactPath == "" {
			return restoreSource{}, fmt.Errorf("未找到本地备份文件路径")
		}
		if stat, err := os.Stat(artifactPath); err != nil || stat.IsDir() {
			return restoreSource{}, fmt.Errorf("本地备份文件不存在，请检查备份目录")
		}
		return restoreSource{Type: "local", Path: artifactPath, Artifacts: backupArtifactMap(job), Copies: s.restoreCopyMap(job)}, nil
	case "object":
		request, err := s.backfillBackupRequest(job)
		if err != nil {
			return restoreSource{}, err
		}
		objectName := restoreArtifactObject(job)
		if request.Destination == "" || objectName == "" {
			return restoreSource{}, fmt.Errorf("未找到对象存储备份位置")
		}
		storage, err := s.store.Storage(request.Destination)
		if err != nil {
			return restoreSource{}, fmt.Errorf("未找到备份目标对象存储")
		}
		return restoreSource{Type: "object", Object: objectName, Storage: &storage, Artifacts: backupArtifactMap(job), Copies: s.restoreCopyMap(job)}, nil
	case "webdav":
		request, err := s.backfillBackupRequest(job)
		if err != nil {
			return restoreSource{}, err
		}
		objectName := restoreArtifactObject(job)
		if request.Destination == "" || objectName == "" {
			return restoreSource{}, fmt.Errorf("未找到 WebDAV 备份位置")
		}
		storage, err := s.store.Storage(request.Destination)
		if err != nil {
			return restoreSource{}, fmt.Errorf("未找到备份目标 WebDAV 存储")
		}
		return restoreSource{Type: "webdav", Object: objectName, Storage: &storage, Artifacts: backupArtifactMap(job), Copies: s.restoreCopyMap(job)}, nil
	case "one_drive":
		request, err := s.backfillBackupRequest(job)
		if err != nil {
			return restoreSource{}, err
		}
		objectName := restoreArtifactObject(job)
		if request.Destination == "" || objectName == "" {
			return restoreSource{}, fmt.Errorf("未找到 OneDrive 备份位置")
		}
		storage, err := s.store.Storage(request.Destination)
		if err != nil {
			return restoreSource{}, fmt.Errorf("未找到备份目标 OneDrive 存储")
		}
		return restoreSource{Type: "one_drive", Object: objectName, Storage: &storage, Artifacts: backupArtifactMap(job), Copies: s.restoreCopyMap(job)}, nil
	case "multi":
		copies := s.restoreCopyMap(job)
		if len(copies) == 0 {
			return restoreSource{}, fmt.Errorf("未找到可用备份副本")
		}
		return restoreSource{Type: "multi", Artifacts: backupArtifactMap(job), Copies: copies}, nil
	default:
		return restoreSource{}, fmt.Errorf("该备份目标暂不支持恢复")
	}
}

func (s *Server) restoreCopyMap(job model.BackupJob) map[string][]restoreArtifactCopy {
	items := map[string][]restoreArtifactCopy{}
	for _, artifact := range job.Artifacts {
		candidates := s.restoreCopiesForArtifact(job, artifact)
		if len(candidates) > 0 && strings.TrimSpace(artifact.Database) != "" {
			items[artifact.Database] = candidates
		}
	}
	return items
}

func (s *Server) restoreCopiesForArtifact(job model.BackupJob, artifact model.BackupArtifact) []restoreArtifactCopy {
	copies := make([]model.BackupArtifactCopy, 0, len(artifact.Copies)+1)
	for _, copy := range artifact.Copies {
		if copy.Status != "" && copy.Status != "completed" {
			continue
		}
		copies = append(copies, copy)
	}
	if len(copies) == 0 && (strings.TrimSpace(artifact.Name) != "" || strings.TrimSpace(artifact.Path) != "") {
		copies = append(copies, model.BackupArtifactCopy{Name: artifact.Name, Size: artifact.Size, Path: artifact.Path, Type: job.DestinationType, Role: "primary"})
	}
	sortBackupArtifactCopies(copies)
	result := make([]restoreArtifactCopy, 0, len(copies))
	for _, copy := range copies {
		switch copy.Type {
		case "local":
			path := copy.Path
			if path == "" && job.Request.Destination != "" && copy.Name != "" {
				path = filepath.Join(job.Request.Destination, filepath.FromSlash(copy.Name))
			}
			if path != "" {
				result = append(result, restoreArtifactCopy{Type: "local", Path: path, Label: backupCopyLabel(copy)})
			}
		case "object", "webdav", "one_drive":
			if strings.TrimSpace(copy.Name) == "" {
				continue
			}
			storage, err := s.store.Storage(copy.StorageID)
			if err != nil {
				continue
			}
			result = append(result, restoreArtifactCopy{Type: copy.Type, Object: copy.Name, Storage: &storage, Label: backupCopyLabel(copy)})
		default:
			if (job.DestinationType == "object" || job.DestinationType == "webdav" || job.DestinationType == "one_drive") && strings.TrimSpace(copy.Name) != "" {
				storage, err := s.backupArtifactStorage(job)
				if err == nil {
					result = append(result, restoreArtifactCopy{Type: job.DestinationType, Object: copy.Name, Storage: &storage, Label: backupCopyLabel(copy)})
				}
			} else if job.DestinationType == "local" {
				path := copy.Path
				if path == "" && job.Request.Destination != "" && copy.Name != "" {
					path = filepath.Join(job.Request.Destination, filepath.FromSlash(copy.Name))
				}
				if path != "" {
					result = append(result, restoreArtifactCopy{Type: "local", Path: path, Label: backupCopyLabel(copy)})
				}
			}
		}
	}
	return result
}

func backupCopyLabel(copy model.BackupArtifactCopy) string {
	if strings.TrimSpace(copy.StorageName) != "" {
		if copy.Role == "primary" {
			return copy.StorageName + "（主）"
		}
		return copy.StorageName
	}
	if copy.Role == "primary" {
		return "主备份节点"
	}
	return "备用备份节点"
}

func restoreArtifactObject(job model.BackupJob) string {
	if job.ArtifactName != "" {
		return job.ArtifactName
	}
	for _, artifact := range job.Artifacts {
		if strings.TrimSpace(artifact.Name) != "" {
			return artifact.Name
		}
	}
	return ""
}

func restoreArtifactPath(job model.BackupJob) string {
	if job.ArtifactPath != "" {
		return job.ArtifactPath
	}
	for _, artifact := range job.Artifacts {
		if strings.TrimSpace(artifact.Path) != "" {
			return artifact.Path
		}
	}
	if job.DestinationType == "local" && job.Request.Destination != "" && job.ArtifactName != "" {
		return filepath.Join(job.Request.Destination, filepath.FromSlash(job.ArtifactName))
	}
	for _, artifact := range job.Artifacts {
		if job.DestinationType == "local" && job.Request.Destination != "" && strings.TrimSpace(artifact.Name) != "" {
			return filepath.Join(job.Request.Destination, filepath.FromSlash(artifact.Name))
		}
	}
	return ""
}

func restoreArtifactSummary(job model.BackupJob) model.BackupArtifact {
	if job.ArtifactName != "" {
		return model.BackupArtifact{Name: job.ArtifactName, Size: job.ArtifactSize, Path: job.ArtifactPath}
	}
	for _, artifact := range job.Artifacts {
		if strings.TrimSpace(artifact.Name) != "" {
			return artifact
		}
	}
	return model.BackupArtifact{}
}

func backupArtifactMap(job model.BackupJob) map[string]model.BackupArtifact {
	items := map[string]model.BackupArtifact{}
	for _, artifact := range job.Artifacts {
		if strings.TrimSpace(artifact.Database) != "" && strings.TrimSpace(artifact.Name) != "" {
			items[artifact.Database] = artifact
		}
	}
	return items
}

func (s *Server) runRestore(ctx context.Context, id string, target model.Connection, sourceDatabases []string, targetDatabases map[string]string, strategy string, sources []restoreSource, mongoReplicaSet bool) {
	if mongoReplicaSet {
		s.runMongoOplogRestore(ctx, id, target, sources)
		return
	}
	if len(sources) == 0 {
		return
	}
	s.runRestoreSingle(ctx, id, target, sourceDatabases, targetDatabases, strategy, sources[0])
}

func (s *Server) runRestoreSingle(ctx context.Context, id string, target model.Connection, sourceDatabases []string, targetDatabases map[string]string, strategy string, source restoreSource) {
	defer s.store.ClearBackupCancel(id)
	log.Printf("restore[%s] started: %s -> %s/%s", id, restoreSourceLabel(source), target.Name, restoreTargetLabel(sourceDatabases, targetDatabases))
	started := time.Now()
	_ = s.store.UpdateBackup(id, func(item *model.BackupJob) {
		item.Status = model.StatusRunning
		item.Stage = "restore"
		item.Progress = 15
		item.StartedAt = started
		item.LastRunAt = &started
		item.FinishedAt = nil
		item.Error = ""
	})
	reporter := func(progress backup.Progress) {
		_ = s.store.UpdateBackup(id, func(item *model.BackupJob) {
			item.Stage = progress.Stage
			item.Progress = backupProgress(progress.Stage, progress.Progress)
			appendJobEvent(&item.Events, model.Event{Time: time.Now(), Level: progress.EventLevel, Message: progress.EventMessage})
		})
	}
	var err error
	for index, sourceDatabase := range sourceDatabases {
		if err == nil {
			targetDatabase := targetDatabases[sourceDatabase]
			reporter(backup.Progress{Stage: "restore", EventLevel: "INFO", EventMessage: fmt.Sprintf("开始恢复数据库 %s 到 %s（%d/%d）", sourceDatabase, targetDatabase, index+1, len(sourceDatabases)), Progress: 20 + index*70/len(sourceDatabases)})
			artifactPath, cleanup, prepareErr := s.prepareRestoreArtifact(ctx, restoreArtifactForDatabase(source, sourceDatabase), reporter)
			if prepareErr != nil {
				err = prepareErr
				if cleanup != nil {
					cleanup()
				}
				break
			}
			if restoreErr := s.backup.Restore(ctx, target, targetDatabase, artifactPathForRestoreDatabase(artifactPath, sourceDatabase), backup.RestoreOptions{Strategy: strategy, SourceDatabase: sourceDatabase}, func(progress backup.Progress) {
				if len(sourceDatabases) > 1 {
					progress.Progress = 20 + index*70/len(sourceDatabases) + backupProgress(progress.Stage, progress.Progress)/len(sourceDatabases)
				}
				reporter(progress)
			}); restoreErr != nil {
				err = restoreErr
			}
			if cleanup != nil {
				cleanup()
			}
		}
	}
	if err == nil {
		reporter(backup.Progress{Stage: "validation", EventLevel: "INFO", EventMessage: "正在校验恢复后的目标库"})
		for _, sourceDatabase := range sourceDatabases {
			targetDatabase := targetDatabases[sourceDatabase]
			switch target.Type {
			case "MongoDB":
				collections, collectionErr := mongo.CollectionNames(ctx, target, targetDatabase)
				if collectionErr != nil {
					err = fmt.Errorf("restore validation: %w", collectionErr)
					break
				}
				reporter(backup.Progress{Stage: "validation", EventLevel: "INFO", EventMessage: fmt.Sprintf("恢复校验完成，目标库 %s 发现 %d 个集合", targetDatabase, len(collections)), Progress: 95})
			case "Neo4j":
				objects, objectErr := neo4j.LabelsAndRelationshipTypes(ctx, target, targetDatabase)
				if objectErr != nil {
					err = fmt.Errorf("restore validation: %w", objectErr)
					break
				}
				reporter(backup.Progress{Stage: "validation", EventLevel: "INFO", EventMessage: fmt.Sprintf("恢复校验完成，目标库 %s 发现 %d 个标签或关系类型", targetDatabase, len(objects)), Progress: 95})
			default:
				if tables, tableErr := tables(ctx, target, targetDatabase); tableErr != nil {
					err = fmt.Errorf("restore validation: %w", tableErr)
					break
				} else {
					reporter(backup.Progress{Stage: "validation", EventLevel: "INFO", EventMessage: fmt.Sprintf("恢复校验完成，目标库 %s 发现 %d 张普通表", targetDatabase, len(tables)), Progress: 95})
				}
			}
		}
	}
	finished := time.Now()
	_ = s.store.UpdateBackup(id, func(item *model.BackupJob) {
		item.FinishedAt = &finished
		if err == nil {
			item.Status = model.StatusCompleted
			item.Stage = "completed"
			item.Progress = 100
			appendJobEvent(&item.Events, model.Event{Time: finished, Level: "INFO", Message: "恢复任务完成"})
			return
		}
		if errors.Is(err, context.Canceled) {
			item.Status = model.StatusCancelled
			item.Stage = "cancelled"
			appendJobEvent(&item.Events, model.Event{Time: finished, Level: "WARN", Message: "恢复任务已取消"})
		} else {
			item.Status = model.StatusFailed
			item.Stage = "failed"
			item.Error = userFacingErrorMessage(errorContextBackupRuntime, err)
			appendJobEvent(&item.Events, model.Event{Time: finished, Level: "ERROR", Message: err.Error()})
		}
	})
}

func (s *Server) runMongoOplogRestore(ctx context.Context, id string, target model.Connection, sources []restoreSource) {
	defer s.store.ClearBackupCancel(id)
	started := time.Now()
	_ = s.store.UpdateBackup(id, func(item *model.BackupJob) {
		item.Status = model.StatusRunning
		item.Stage = "restore"
		item.Progress = 15
		item.StartedAt = started
		item.LastRunAt = &started
		item.FinishedAt = nil
		item.Error = ""
	})
	reporter := func(progress backup.Progress) {
		_ = s.store.UpdateBackup(id, func(item *model.BackupJob) {
			item.Stage = progress.Stage
			item.Progress = backupProgress(progress.Stage, progress.Progress)
			appendJobEvent(&item.Events, model.Event{Time: time.Now(), Level: progress.EventLevel, Message: progress.EventMessage})
		})
	}
	var restoreErr error
	for index, source := range sources {
		step := "全量基线"
		if index > 0 {
			step = "oplog 增量"
		}
		reporter(backup.Progress{Stage: "restore", EventLevel: "INFO", EventMessage: fmt.Sprintf("正在恢复 MongoDB %s（%d/%d）", step, index+1, len(sources)), Progress: 20 + index*70/len(sources)})
		artifactPath, cleanup, err := s.prepareRestoreArtifact(ctx, restoreArtifactForDatabase(source, mongoReplicaSetScope), reporter)
		if err == nil {
			err = s.backup.Restore(ctx, target, "", artifactPath, backup.RestoreOptions{Strategy: "recreate", MongoReplicaSet: true, MongoOplogOnly: index > 0}, reporter)
		}
		if cleanup != nil {
			cleanup()
		}
		if err != nil {
			restoreErr = err
			break
		}
	}
	if restoreErr == nil {
		databases, err := mongo.Databases(ctx, target)
		if err != nil {
			restoreErr = fmt.Errorf("restore validation: %w", err)
		} else {
			reporter(backup.Progress{Stage: "validation", EventLevel: "INFO", EventMessage: fmt.Sprintf("MongoDB 副本集恢复校验完成，发现 %d 个业务数据库", len(databases)), Progress: 95})
		}
	}
	finished := time.Now()
	_ = s.store.UpdateBackup(id, func(item *model.BackupJob) {
		item.FinishedAt = &finished
		if restoreErr == nil {
			item.Status = model.StatusCompleted
			item.Stage = "completed"
			item.Progress = 100
			appendJobEvent(&item.Events, model.Event{Time: finished, Level: "INFO", Message: "MongoDB 副本集恢复完成"})
			return
		}
		if errors.Is(restoreErr, context.Canceled) {
			item.Status = model.StatusCancelled
			item.Stage = "cancelled"
			appendJobEvent(&item.Events, model.Event{Time: finished, Level: "WARN", Message: "恢复任务已取消"})
			return
		}
		item.Status = model.StatusFailed
		item.Stage = "failed"
		item.Error = userFacingErrorMessage(errorContextBackupRuntime, restoreErr)
		appendJobEvent(&item.Events, model.Event{Time: finished, Level: "ERROR", Message: restoreErr.Error()})
	})
}

func restoreSourceLabel(source restoreSource) string {
	if source.Type == "multi" {
		return "multi-destination"
	}
	if source.Type == "object" {
		return "object/" + source.Object
	}
	return "local/" + source.Path
}

func restoreArtifactForDatabase(source restoreSource, database string) restoreSource {
	artifact, ok := source.Artifacts[database]
	if !ok {
		if copies := source.Copies[database]; len(copies) > 0 {
			next := source
			next.Copies = map[string][]restoreArtifactCopy{database: copies}
			return next
		}
		return source
	}
	next := source
	if copies := source.Copies[database]; len(copies) > 0 {
		next.Copies = map[string][]restoreArtifactCopy{database: copies}
		next.Type = "multi"
	}
	if source.Type == "object" && artifact.Name != "" {
		next.Object = artifact.Name
	}
	if source.Type == "local" && artifact.Path != "" {
		next.Path = artifact.Path
	}
	return next
}

func (s *Server) prepareRestoreArtifact(ctx context.Context, source restoreSource, reporter func(backup.Progress)) (string, func(), error) {
	if len(source.Copies) > 0 {
		for _, copies := range source.Copies {
			if len(copies) == 0 {
				continue
			}
			return s.prepareRestoreArtifactFromCopies(ctx, copies, reporter)
		}
	}
	if source.Type == "local" {
		return source.Path, nil, nil
	}
	if (source.Type != "object" && source.Type != "webdav" && source.Type != "one_drive") || source.Storage == nil {
		return "", nil, fmt.Errorf("unsupported restore source %q", source.Type)
	}
	tempDir, err := backup.MkdirTemp("databridge-restore-artifact-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tempDir) }
	targetPath := filepath.Join(tempDir, filepath.Base(source.Object))
	reporter(backup.Progress{Stage: "download", EventLevel: "INFO", EventMessage: "正在从" + remoteStorageTypeLabel(source.Type) + "下载备份文件"})
	size, err := downloadRemoteBackupArtifact(ctx, source.Type, *source.Storage, source.Object, targetPath)
	if err != nil {
		cleanup()
		return "", nil, err
	}
	reporter(backup.Progress{Stage: "download", EventLevel: "INFO", EventMessage: fmt.Sprintf("备份文件已下载到临时目录，大小 %s", formatBytes(size)), Progress: 20})
	return targetPath, cleanup, nil
}

func (s *Server) prepareRestoreArtifactFromCopies(ctx context.Context, copies []restoreArtifactCopy, reporter func(backup.Progress)) (string, func(), error) {
	var failures []string
	for index, copy := range copies {
		if index > 0 {
			reporter(backup.Progress{Stage: "download", EventLevel: "WARN", EventMessage: "主备份节点不可用，正在尝试备用节点：" + copy.Label})
		}
		switch copy.Type {
		case "local":
			if stat, err := os.Stat(copy.Path); err == nil && !stat.IsDir() {
				reporter(backup.Progress{Stage: "download", EventLevel: "INFO", EventMessage: "已选择备份副本：" + copy.Label})
				return copy.Path, nil, nil
			} else {
				failures = append(failures, fmt.Sprintf("%s：本地备份文件不可用", copy.Label))
			}
		case "object", "webdav", "one_drive":
			if copy.Storage == nil || copy.Object == "" {
				failures = append(failures, fmt.Sprintf("%s：%s配置不完整", copy.Label, remoteStorageTypeLabel(copy.Type)))
				continue
			}
			tempDir, err := backup.MkdirTemp("databridge-restore-artifact-*")
			if err != nil {
				return "", nil, err
			}
			cleanup := func() { _ = os.RemoveAll(tempDir) }
			targetPath := filepath.Join(tempDir, filepath.Base(copy.Object))
			reporter(backup.Progress{Stage: "download", EventLevel: "INFO", EventMessage: "正在从备份节点下载：" + copy.Label})
			size, err := downloadRemoteBackupArtifact(ctx, copy.Type, *copy.Storage, copy.Object, targetPath)
			if err != nil {
				cleanup()
				failures = append(failures, fmt.Sprintf("%s：%v", copy.Label, err))
				continue
			}
			reporter(backup.Progress{Stage: "download", EventLevel: "INFO", EventMessage: fmt.Sprintf("备份文件已从 %s 下载，大小 %s", copy.Label, formatBytes(size)), Progress: 20})
			return targetPath, cleanup, nil
		default:
			failures = append(failures, fmt.Sprintf("%s：不支持的备份副本类型", copy.Label))
		}
	}
	if len(failures) == 0 {
		return "", nil, fmt.Errorf("未找到可用备份副本")
	}
	return "", nil, fmt.Errorf("所有备份副本均不可用：%s", strings.Join(failures, "；"))
}

func (s *Server) downloadBackupArtifact(w http.ResponseWriter, r *http.Request) {
	item, artifact, err := s.backupArtifactForRequest(r)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", err.Error())
		return
	}
	fileName := filepath.Base(artifact.Name)
	if fileName == "." || fileName == string(filepath.Separator) || fileName == "" {
		fileName = "databridge-backup.tar.gz"
	}
	copies := s.restoreCopiesForArtifact(item, artifact)
	if len(copies) > 0 {
		s.downloadBackupArtifactFromCopies(w, r, fileName, copies)
		return
	}
	switch item.DestinationType {
	case "local":
		path := artifact.Path
		if path == "" {
			path = restoreArtifactPath(item)
		}
		file, err := os.Open(path)
		if err != nil {
			writeError(w, http.StatusNotFound, "not_found", "本地备份文件不存在，请检查备份目录")
			return
		}
		defer file.Close()
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitizeDownloadFileName(fileName)))
		if stat, err := file.Stat(); err == nil && !stat.IsDir() {
			w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
		}
		if _, err := io.Copy(w, file); err != nil {
			log.Printf("backup artifact download failed: %v", err)
		}
	case "object", "webdav", "one_drive":
		storage, err := s.backupArtifactStorage(item)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_artifact", err.Error())
			return
		}
		tempDir, err := backup.MkdirTemp("databridge-artifact-download-*")
		if err != nil {
			writeError(w, http.StatusInternalServerError, "download_failed", "准备下载备份文件失败")
			return
		}
		defer os.RemoveAll(tempDir)
		tempPath := filepath.Join(tempDir, filepath.Base(artifact.Name))
		size, err := downloadRemoteBackupArtifact(r.Context(), item.DestinationType, storage, artifact.Name, tempPath)
		if err != nil {
			writeError(w, http.StatusBadRequest, "download_failed", userFacingErrorMessage(errorContextBackupRuntime, err))
			return
		}
		w.Header().Set("Content-Type", "application/gzip")
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitizeDownloadFileName(fileName)))
		w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
		http.ServeFile(w, r, tempPath)
	default:
		writeError(w, http.StatusBadRequest, "invalid_artifact", "该备份目标暂不支持下载")
	}
}

func (s *Server) downloadBackupArtifactFromCopies(w http.ResponseWriter, r *http.Request, fileName string, copies []restoreArtifactCopy) {
	var failures []string
	for _, copy := range copies {
		switch copy.Type {
		case "local":
			file, err := os.Open(copy.Path)
			if err != nil {
				failures = append(failures, fmt.Sprintf("%s：本地备份文件不存在", copy.Label))
				continue
			}
			defer file.Close()
			w.Header().Set("Content-Type", "application/gzip")
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitizeDownloadFileName(fileName)))
			w.Header().Set("X-DataBridge-Backup-Node", copy.Label)
			if stat, err := file.Stat(); err == nil && !stat.IsDir() {
				w.Header().Set("Content-Length", strconv.FormatInt(stat.Size(), 10))
			}
			if _, err := io.Copy(w, file); err != nil {
				log.Printf("backup artifact download failed: %v", err)
			}
			return
		case "object", "webdav", "one_drive":
			if copy.Storage == nil || copy.Object == "" {
				failures = append(failures, fmt.Sprintf("%s：%s配置不完整", copy.Label, remoteStorageTypeLabel(copy.Type)))
				continue
			}
			tempDir, err := backup.MkdirTemp("databridge-artifact-download-*")
			if err != nil {
				writeError(w, http.StatusInternalServerError, "download_failed", "准备下载备份文件失败")
				return
			}
			defer os.RemoveAll(tempDir)
			tempPath := filepath.Join(tempDir, filepath.Base(copy.Object))
			size, err := downloadRemoteBackupArtifact(r.Context(), copy.Type, *copy.Storage, copy.Object, tempPath)
			if err != nil {
				failures = append(failures, fmt.Sprintf("%s：%v", copy.Label, err))
				continue
			}
			w.Header().Set("Content-Type", "application/gzip")
			w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, sanitizeDownloadFileName(fileName)))
			w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
			w.Header().Set("X-DataBridge-Backup-Node", copy.Label)
			http.ServeFile(w, r, tempPath)
			return
		}
	}
	writeError(w, http.StatusBadRequest, "download_failed", "所有备份副本均不可用："+strings.Join(failures, "；"))
}

func downloadRemoteBackupArtifact(ctx context.Context, storageType string, storage model.RemoteStorage, objectName, targetPath string) (int64, error) {
	if storageType == "webdav" {
		return remotestorage.DownloadWebDAV(ctx, storage, objectName, targetPath)
	}
	if storageType == "one_drive" {
		return remotestorage.DownloadOneDrive(ctx, storage, objectName, targetPath)
	}
	return remotestorage.DownloadObject(ctx, storage, objectName, targetPath)
}

func remoteStorageTypeLabel(storageType string) string {
	if storageType == "webdav" {
		return "WebDAV"
	}
	if storageType == "one_drive" {
		return "OneDrive"
	}
	return "对象存储"
}

func (s *Server) deleteBackupArtifact(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusMethodNotAllowed, "artifact_delete_disabled", "备份文件删除已禁用，请在存储系统中按运维流程处理")
}

func (s *Server) backupArtifactForRequest(r *http.Request) (model.BackupJob, model.BackupArtifact, error) {
	item, err := s.store.Backup(r.PathValue("id"))
	if err != nil {
		return model.BackupJob{}, model.BackupArtifact{}, fmt.Errorf("backup not found")
	}
	versionID := strings.TrimSpace(r.URL.Query().Get("artifactVersionId"))
	if versionID != "" {
		item, err = backupJobForArtifactVersion(item, versionID)
		if err != nil {
			return model.BackupJob{}, model.BackupArtifact{}, err
		}
	}
	database := strings.TrimSpace(r.PathValue("database"))
	for _, artifact := range backupArtifactsForDisplay(item) {
		if artifact.Database == database {
			return item, artifact, nil
		}
	}
	return model.BackupJob{}, model.BackupArtifact{}, fmt.Errorf("backup artifact not found")
}

func (s *Server) backupArtifactStorage(item model.BackupJob) (model.RemoteStorage, error) {
	request, err := s.backfillBackupRequest(item)
	if err != nil {
		return model.RemoteStorage{}, err
	}
	if request.Destination == "" {
		return model.RemoteStorage{}, fmt.Errorf("未找到对象存储备份位置")
	}
	storage, err := s.store.Storage(request.Destination)
	if err != nil {
		return model.RemoteStorage{}, fmt.Errorf("未找到备份目标对象存储")
	}
	return storage, nil
}

func backupArtifactsForDisplay(job model.BackupJob) []model.BackupArtifact {
	if len(job.Artifacts) > 0 {
		return job.Artifacts
	}
	if job.ArtifactName == "" {
		return nil
	}
	database := strings.TrimSpace(job.SourceDatabase)
	if database == "" {
		database = "default"
	}
	return []model.BackupArtifact{{Database: database, Name: job.ArtifactName, Size: job.ArtifactSize, Path: restoreArtifactPath(job)}}
}

func sanitizeDownloadFileName(value string) string {
	value = strings.Map(func(character rune) rune {
		switch character {
		case '"', '\\', '/', ':', '*', '?', '<', '>', '|':
			return '_'
		case 0, '\n', '\r', '\t':
			return -1
		default:
			return character
		}
	}, strings.TrimSpace(value))
	value = strings.Trim(value, ". ")
	if strings.TrimSpace(value) == "" {
		return "databridge-backup.tar.gz"
	}
	return value
}

func (s *Server) cancelBackup(w http.ResponseWriter, r *http.Request) {
	if err := s.store.CancelBackup(r.PathValue("id")); err != nil {
		writeError(w, http.StatusNotFound, "not_found", "backup not found")
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]bool{"ok": true})
}

func (s *Server) deleteBackup(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	item, err := s.store.Backup(id)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "backup not found")
		return
	}
	if item.Status == model.StatusRunning || item.Status == model.StatusPending {
		writeError(w, http.StatusConflict, "task_running", "任务正在执行，请先停止后再删除")
		return
	}
	if err := s.store.DeleteBackup(id); err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateBackup, err))
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) updateBackupFavorite(w http.ResponseWriter, r *http.Request) {
	var input model.FavoriteInput
	if !decodeJSON(w, r, &input) {
		return
	}
	item, err := s.store.SetBackupFavorite(r.PathValue("id"), input.Favorite)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "backup not found")
			return
		}
		writeError(w, http.StatusInternalServerError, "storage_error", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, compactBackupListJob(item))
}

func (s *Server) listBackups(w http.ResponseWriter, _ *http.Request) {
	s.reconcileScheduledBackupRuns()
	items, err := s.store.Backups()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateBackup, err))
		return
	}
	for index := range items {
		items[index] = compactBackupListJob(items[index])
	}
	writeJSON(w, http.StatusOK, items)
}
