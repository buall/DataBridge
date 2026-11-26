package api

import (
	"bytes"
	"crypto/tls"
	"databridge/internal/model"
	"databridge/internal/store"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net"
	"net/http"
	"net/mail"
	"net/smtp"
	"net/url"
	"strconv"
	"strings"
	"time"
)

func normalizeBackupNotificationTrigger(value string) string {
	switch strings.TrimSpace(value) {
	case "success":
		return "success"
	case "failure", "failed":
		return "failure"
	default:
		return "all"
	}
}

func backupNotificationShouldSend(notification model.BackupNotification, success bool) bool {
	if !notification.Enabled {
		return false
	}
	switch normalizeBackupNotificationTrigger(notification.Trigger) {
	case "success":
		return success
	case "failure":
		return !success
	default:
		return true
	}
}

func (s *Server) sendBackupNotification(job model.BackupJob, success bool) error {
	if !job.Notification.Enabled {
		return nil
	}
	if err := s.applyDefaultNotificationTitle(&job); err != nil {
		return err
	}
	if len(job.Notification.RecipientIDs) > 0 {
		errors := make([]string, 0)
		for _, id := range job.Notification.RecipientIDs {
			recipient, err := s.store.NotificationRecipient(id)
			if err != nil {
				errors = append(errors, id+": 接收者不存在")
				continue
			}
			if !recipient.Enabled {
				continue
			}
			if err := s.sendBackupNotificationToRecipient(job, success, recipient); err != nil {
				errors = append(errors, recipient.Name+": "+err.Error())
			}
		}
		if len(errors) > 0 {
			return fmt.Errorf("%s", strings.Join(errors, "；"))
		}
		return nil
	}
	switch job.Notification.Method {
	case "webhook":
		return s.sendBackupWebhook(job, success)
	case "email":
		return s.sendBackupEmail(job, success)
	default:
		return fmt.Errorf("不支持的通知方式")
	}
}

func (s *Server) applyDefaultNotificationTitle(job *model.BackupJob) error {
	if strings.TrimSpace(job.Notification.Title) != "" {
		return nil
	}
	settings, err := s.store.NotificationSettings()
	if err != nil {
		return err
	}
	job.Notification.Title = settings.MessageTitle
	return nil
}

func (s *Server) sendBackupNotificationToRecipient(job model.BackupJob, success bool, recipient model.NotificationRecipient) error {
	copy := job
	copy.Notification.RecipientIDs = nil
	copy.Notification.Target = recipient.Target
	if strings.TrimSpace(recipient.MessageTitle) != "" {
		copy.Notification.Title = recipient.MessageTitle
	}
	if recipient.Type == "telegram" {
		return sendTelegramMessage(&http.Client{Timeout: 10 * time.Second}, "https://api.telegram.org", recipient.Target, backupNotificationHTML(copy, success))
	}
	if recipient.Type == "email" {
		copy.Notification.Method = "email"
		return s.sendBackupEmail(copy, success)
	}
	copy.Notification.Method = "webhook"
	return s.sendBackupWebhook(copy, success)
}

type telegramRecipientConfig struct {
	BotToken string `json:"botToken"`
	ChatID   string `json:"chatId"`
}

func sendTelegramMessage(client *http.Client, apiBaseURL string, target string, message string) error {
	var config telegramRecipientConfig
	if err := json.Unmarshal([]byte(target), &config); err != nil || strings.TrimSpace(config.BotToken) == "" || strings.TrimSpace(config.ChatID) == "" {
		return fmt.Errorf("Telegram 配置无效")
	}
	body, err := json.Marshal(map[string]string{"chat_id": strings.TrimSpace(config.ChatID), "text": message, "parse_mode": "HTML"})
	if err != nil {
		return fmt.Errorf("构造 Telegram 通知失败")
	}
	endpoint := strings.TrimRight(apiBaseURL, "/") + "/bot" + strings.TrimSpace(config.BotToken) + "/sendMessage"
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("创建 Telegram 请求失败")
	}
	req.Header.Set("Content-Type", "application/json")
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("发送 Telegram 通知失败: %w", err)
	}
	defer resp.Body.Close()
	var result struct {
		OK          bool   `json:"ok"`
		Description string `json:"description"`
	}
	_ = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&result)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !result.OK {
		if result.Description != "" {
			return fmt.Errorf("Telegram 返回错误: %s", result.Description)
		}
		return fmt.Errorf("Telegram 返回 %d", resp.StatusCode)
	}
	return nil
}

func (s *Server) sendBackupWebhook(job model.BackupJob, success bool) error {
	target, err := url.Parse(strings.TrimSpace(job.Notification.Target))
	if err != nil || target.Scheme == "" || target.Host == "" {
		return fmt.Errorf("webhook 地址无效")
	}
	message := backupNotificationText(job, success)
	markdownMessage := backupNotificationMarkdown(job, success)
	payload := map[string]any{
		"jobId":          job.ID,
		"jobName":        job.Name,
		"success":        success,
		"status":         backupNotificationStatus(success),
		"text":           message,
		"markdown":       markdownMessage,
		"sourceName":     job.SourceName,
		"sourceType":     job.SourceType,
		"sourceDatabase": job.SourceDatabase,
		"destination":    job.Destination,
		"finishedAt":     time.Now().Format(time.RFC3339),
	}
	payload = webhookPayloadForTarget(target.Host, payload, message, markdownMessage, success)
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("构造 webhook 通知失败")
	}
	req, err := http.NewRequest(http.MethodPost, target.String(), bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("创建 webhook 请求失败")
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("发送 webhook 失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("webhook 返回 %d", resp.StatusCode)
	}
	return nil
}

func webhookPayloadForTarget(host string, payload map[string]any, message string, markdownMessage string, success bool) map[string]any {
	lowerHost := strings.ToLower(host)
	switch {
	case strings.Contains(lowerHost, "dingtalk.com"):
		title := strings.SplitN(message, "\n", 2)[0]
		content := fmt.Sprintf("### %s %s\n%s", notificationStatusIcon(success), title, notificationDetailsMarkdown(markdownMessage))
		return map[string]any{"msgtype": "markdown", "markdown": map[string]string{"title": title, "text": dingtalkMarkdown(content)}}
	case strings.Contains(lowerHost, "feishu.cn"), strings.Contains(lowerHost, "larksuite.com"):
		title := strings.SplitN(message, "\n", 2)[0]
		return map[string]any{"msg_type": "interactive", "card": map[string]any{
			"schema": "2.0",
			"header": map[string]any{
				"title":    map[string]string{"tag": "plain_text", "content": title},
				"template": notificationCardTemplate(success),
				"padding":  "12px 12px 12px 12px",
			},
			"body": map[string]any{
				"direction": "vertical",
				"padding":   "12px 12px 12px 12px",
				"elements": []map[string]any{{
					"tag": "markdown", "content": feishuCardMarkdown(markdownMessage),
					"text_align": "left", "margin": "0px 0px 0px 0px",
				}},
			},
		}}
	case strings.Contains(lowerHost, "qyapi.weixin.qq.com"):
		return map[string]any{"msgtype": "markdown", "markdown": map[string]string{"content": markdownMessage}}
	case strings.Contains(lowerHost, "hooks.slack.com"):
		return map[string]any{"text": markdownMessage}
	default:
		return payload
	}
}

func notificationStatusIcon(success bool) string {
	if success {
		return "🟢"
	}
	return "🔴"
}

func notificationCardTemplate(success bool) string {
	if success {
		return "green"
	}
	return "red"
}

func notificationDetailsMarkdown(message string) string {
	lines := strings.Split(strings.ReplaceAll(message, "\r\n", "\n"), "\n")
	if len(lines) > 0 {
		lines = lines[1:]
	}
	formatted := make([]string, 0, len(lines)+2)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == "备份文件：" {
			formatted = append(formatted, "---", "**备份文件**")
			continue
		}
		if strings.HasPrefix(line, "- ") {
			formatted = append(formatted, line)
			continue
		}
		if parts := strings.SplitN(line, "：", 2); len(parts) == 2 {
			formatted = append(formatted, fmt.Sprintf("**%s：** %s", parts[0], parts[1]))
			continue
		}
		formatted = append(formatted, line)
	}
	return strings.Join(formatted, "\n")
}

func feishuCardMarkdown(message string) string {
	return strings.ReplaceAll(notificationDetailsMarkdown(message), "\n", "\n\n")
}

func dingtalkMarkdown(message string) string {
	return strings.ReplaceAll(strings.ReplaceAll(message, "\r\n", "\n"), "\n", "\n\n")
}

func (s *Server) sendBackupEmail(job model.BackupJob, success bool) error {
	settings, err := s.store.SMTPSettings()
	if err != nil {
		return err
	}
	if !settings.Enabled || settings.Host == "" || settings.Port <= 0 || settings.From == "" {
		return fmt.Errorf("未配置邮件服务")
	}
	to := strings.TrimSpace(job.Notification.Target)
	if to == "" {
		return fmt.Errorf("邮件收件人不能为空")
	}
	addr := net.JoinHostPort(settings.Host, strconv.Itoa(settings.Port))
	subject := fmt.Sprintf("%s：%s", backupNotificationTitle(job, success), job.Name)
	msg := strings.Join([]string{
		"To: " + to,
		"From: " + settings.From,
		"Subject: " + subject,
		"MIME-Version: 1.0",
		"Content-Type: text/plain; charset=UTF-8",
		"",
		backupNotificationText(job, success),
	}, "\r\n")
	var auth smtp.Auth
	if settings.Username != "" {
		auth = smtp.PlainAuth("", settings.Username, settings.Password, settings.Host)
	}
	return sendSMTPMail(addr, auth, settings.From, []string{to}, []byte(msg))
}

func sendSMTPMail(addr string, auth smtp.Auth, from string, to []string, msg []byte) error {
	envelopeFrom, err := smtpEnvelopeAddress(from)
	if err != nil {
		return err
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("解析 SMTP 地址失败: %w", err)
	}
	if port != "465" {
		return smtpSendMail(addr, auth, envelopeFrom, to, msg)
	}
	conn, err := smtpTLSDial("tcp", addr, smtpTLSConfig(host))
	if err != nil {
		return fmt.Errorf("连接 SMTP TLS 服务失败: %w", err)
	}
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		_ = conn.Close()
		return fmt.Errorf("初始化 SMTP 客户端失败: %w", err)
	}
	defer client.Close()
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("SMTP 认证失败: %w", err)
		}
	}
	if err := client.Mail(envelopeFrom); err != nil {
		return fmt.Errorf("设置发件人失败: %w", err)
	}
	for _, recipient := range to {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("设置收件人失败: %w", err)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("创建邮件内容失败: %w", err)
	}
	if _, err := writer.Write(msg); err != nil {
		_ = writer.Close()
		return fmt.Errorf("写入邮件内容失败: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("提交邮件内容失败: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("结束 SMTP 会话失败: %w", err)
	}
	return nil
}

func smtpEnvelopeAddress(from string) (string, error) {
	from = strings.TrimSpace(from)
	if from == "" {
		return "", fmt.Errorf("邮件发件人不能为空")
	}
	address, err := mail.ParseAddress(from)
	if err != nil {
		return "", fmt.Errorf("邮件发件人格式无效: %w", err)
	}
	return address.Address, nil
}

var (
	smtpSendMail  = smtp.SendMail
	smtpTLSDial   = tls.Dial
	smtpTLSConfig = func(host string) *tls.Config {
		return &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}
	}
)

func backupNotificationStatus(success bool) string {
	if success {
		return "成功"
	}
	return "失败"
}

func backupNotificationText(job model.BackupJob, success bool) string {
	return strings.Join(backupNotificationLines(job, success), "\n")
}

func backupNotificationMarkdown(job model.BackupJob, success bool) string {
	lines := backupNotificationLines(job, success)
	if len(lines) > 0 {
		lines[0] = fmt.Sprintf("**%s**", lines[0])
	}
	return strings.Join(lines, "\n")
}

func backupNotificationHTML(job model.BackupJob, success bool) string {
	lines := backupNotificationLines(job, success)
	if len(lines) == 0 {
		return ""
	}
	formatted := []string{fmt.Sprintf("<b>%s %s</b>", notificationStatusIcon(success), html.EscapeString(lines[0]))}
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		switch {
		case line == "":
			continue
		case line == "备份文件：":
			formatted = append(formatted, "", "<b>备份文件</b>")
		case strings.HasPrefix(line, "- "):
			formatted = append(formatted, "• "+html.EscapeString(strings.TrimPrefix(line, "- ")))
		default:
			parts := strings.SplitN(line, "：", 2)
			if len(parts) == 2 {
				formatted = append(formatted, fmt.Sprintf("<b>%s：</b> %s", html.EscapeString(parts[0]), html.EscapeString(parts[1])))
			} else {
				formatted = append(formatted, html.EscapeString(line))
			}
		}
	}
	return strings.Join(formatted, "\n")
}

func backupNotificationLines(job model.BackupJob, success bool) []string {
	lines := []string{
		backupNotificationTitle(job, success),
		fmt.Sprintf("任务名称：%s", job.Name),
	}
	if finishedAt := backupNotificationFinishedAt(job); finishedAt != "" {
		lines = append(lines, fmt.Sprintf("备份时间：%s", finishedAt))
	}
	lines = append(lines,
		fmt.Sprintf("源数据库：%s", job.SourceDatabase),
		fmt.Sprintf("目标位置：%s", job.Destination),
	)
	lines = append(lines, backupNotificationArtifactLines(job)...)
	return lines
}

func backupNotificationTitle(job model.BackupJob, success bool) string {
	title := strings.TrimSpace(job.Notification.Title)
	if title == "" {
		title = "DataBridge"
	}
	return fmt.Sprintf("%s 备份%s", title, backupNotificationStatus(success))
}

func backupNotificationFinishedAt(job model.BackupJob) string {
	if job.FinishedAt == nil || job.FinishedAt.IsZero() {
		return ""
	}
	return job.FinishedAt.In(appLocation).Format("2006-01-02 15:04:05")
}

func backupNotificationArtifactLines(job model.BackupJob) []string {
	artifacts := make([]model.BackupArtifact, 0, len(job.Artifacts)+1)
	for _, artifact := range job.Artifacts {
		if strings.TrimSpace(artifact.Name) == "" {
			continue
		}
		artifacts = append(artifacts, artifact)
	}
	if len(artifacts) == 0 && strings.TrimSpace(job.ArtifactName) != "" {
		artifacts = append(artifacts, model.BackupArtifact{
			Database: job.SourceDatabase,
			Name:     job.ArtifactName,
			Size:     job.ArtifactSize,
			Path:     job.ArtifactPath,
		})
	}
	if len(artifacts) == 0 {
		return nil
	}
	lines := make([]string, 0, len(artifacts)+1)
	lines = append(lines, "备份文件：")
	for _, artifact := range artifacts {
		name := strings.TrimSpace(artifact.Name)
		if name == "" {
			continue
		}
		label := strings.TrimSpace(artifact.Database)
		if label == "" {
			label = job.SourceDatabase
		}
		size := formatNotificationBytes(artifact.Size)
		lines = append(lines, fmt.Sprintf("- %s：%s（%s）", label, name, size))
	}
	return lines
}

func formatNotificationBytes(value int64) string {
	if value <= 0 {
		return "未知大小"
	}
	units := []string{"B", "KB", "MB", "GB", "TB"}
	size := float64(value)
	unit := 0
	for size >= 1024 && unit < len(units)-1 {
		size /= 1024
		unit++
	}
	if unit == 0 {
		return fmt.Sprintf("%d %s", value, units[unit])
	}
	return fmt.Sprintf("%.2f %s", size, units[unit])
}

func (s *Server) testBackupNotification(w http.ResponseWriter, r *http.Request) {
	var input model.BackupNotificationTestRequest
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Method = strings.TrimSpace(input.Method)
	input.Target = strings.TrimSpace(input.Target)
	if input.Method != "webhook" && input.Method != "email" {
		writeError(w, http.StatusBadRequest, "invalid_request", "不支持的通知方式")
		return
	}
	if input.Target == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "请填写通知目标")
		return
	}
	now := time.Now()
	job := model.BackupJob{
		ID:             "notification-test",
		Name:           "通知测试",
		SourceName:     "DataBridge",
		SourceType:     "System",
		SourceDatabase: "system",
		Destination:    "notification-test",
		FinishedAt:     &now,
		Notification: model.BackupNotification{
			Enabled: true,
			Method:  input.Method,
			Target:  input.Target,
		},
	}
	if err := s.sendBackupNotification(job, true); err != nil {
		writeError(w, http.StatusBadRequest, "notification_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "测试通知已发送"})
}

func (s *Server) getSMTPSettings(w http.ResponseWriter, _ *http.Request) {
	settings, err := s.store.SMTPSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateBackup, err))
		return
	}
	settings.Password = ""
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) updateSMTPSettings(w http.ResponseWriter, r *http.Request) {
	var input model.SMTPSettings
	if !decodeJSON(w, r, &input) {
		return
	}
	input.Host = strings.TrimSpace(input.Host)
	input.Username = strings.TrimSpace(input.Username)
	input.From = strings.TrimSpace(input.From)
	if input.Enabled {
		if input.Host == "" || input.Port <= 0 || input.From == "" {
			writeError(w, http.StatusBadRequest, "invalid_request", "请完整填写 SMTP 主机、端口和发件人")
			return
		}
	}
	settings, err := s.store.UpdateSMTPSettings(input)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", userFacingErrorMessage(errorContextCreateBackup, err))
		return
	}
	settings.Password = ""
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) getNotificationSettings(w http.ResponseWriter, _ *http.Request) {
	settings, err := s.store.NotificationSettings()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "读取通知配置失败")
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) updateNotificationSettings(w http.ResponseWriter, r *http.Request) {
	var input model.NotificationSettings
	if !decodeJSON(w, r, &input) {
		return
	}
	settings, err := s.store.UpdateNotificationSettings(input)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "保存通知配置失败")
		return
	}
	writeJSON(w, http.StatusOK, settings)
}

func (s *Server) listNotificationRecipients(w http.ResponseWriter, _ *http.Request) {
	items, err := s.store.NotificationRecipients()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "读取通知接收者失败")
		return
	}
	writeJSON(w, http.StatusOK, items)
}

func (s *Server) createNotificationRecipient(w http.ResponseWriter, r *http.Request) {
	var input model.NotificationRecipientInput
	if !decodeJSON(w, r, &input) {
		return
	}
	input, message := prepareNotificationRecipientInput(input, "", false)
	if message != "" {
		writeError(w, http.StatusBadRequest, "invalid_request", message)
		return
	}
	item, err := s.store.AddNotificationRecipient(input)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "保存通知接收者失败")
		return
	}
	writeJSON(w, http.StatusCreated, item)
}

func (s *Server) updateNotificationRecipient(w http.ResponseWriter, r *http.Request) {
	var input model.NotificationRecipientInput
	if !decodeJSON(w, r, &input) {
		return
	}
	current, err := s.store.NotificationRecipient(r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "通知接收者不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "读取通知接收者失败")
		return
	}
	input, message := prepareNotificationRecipientInput(input, current.Target, current.Type == input.Type)
	if message != "" {
		writeError(w, http.StatusBadRequest, "invalid_request", message)
		return
	}
	item, err := s.store.UpdateNotificationRecipient(r.PathValue("id"), input)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "通知接收者不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "更新通知接收者失败")
		return
	}
	writeJSON(w, http.StatusOK, item)
}

func (s *Server) deleteNotificationRecipient(w http.ResponseWriter, r *http.Request) {
	if err := s.store.DeleteNotificationRecipient(r.PathValue("id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "通知接收者不存在")
			return
		}
		writeError(w, http.StatusInternalServerError, "storage_error", "删除通知接收者失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) testNotificationRecipient(w http.ResponseWriter, r *http.Request) {
	recipient, err := s.store.NotificationRecipient(r.PathValue("id"))
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found", "通知接收者不存在")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "storage_error", "读取通知接收者失败")
		return
	}
	if err := s.sendNotificationRecipientTest(recipient); err != nil {
		writeError(w, http.StatusBadRequest, "notification_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "测试通知已发送"})
}

func (s *Server) testNotificationRecipientInput(w http.ResponseWriter, r *http.Request) {
	var input model.NotificationRecipientTestInput
	if !decodeJSON(w, r, &input) {
		return
	}
	currentTarget := ""
	allowSavedTarget := false
	if strings.TrimSpace(input.ID) != "" {
		current, err := s.store.NotificationRecipient(strings.TrimSpace(input.ID))
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "通知接收者不存在")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "storage_error", "读取通知接收者失败")
			return
		}
		if current.Type == input.Type {
			currentTarget = current.Target
			allowSavedTarget = true
		}
	}
	recipientInput, message := prepareNotificationRecipientInput(model.NotificationRecipientInput{Name: input.Name, Type: input.Type, Target: input.Target, MessageTitle: input.MessageTitle, BotToken: input.BotToken, ChatID: input.ChatID, Enabled: true}, currentTarget, allowSavedTarget)
	if message != "" {
		writeError(w, http.StatusBadRequest, "invalid_request", message)
		return
	}
	recipient := model.NotificationRecipient{Name: strings.TrimSpace(input.Name), Type: input.Type, Target: recipientInput.Target, MessageTitle: recipientInput.MessageTitle, Enabled: true}
	if err := s.sendNotificationRecipientTest(recipient); err != nil {
		writeError(w, http.StatusBadRequest, "notification_failed", err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"message": "测试通知已发送"})
}

func (s *Server) sendNotificationRecipientTest(recipient model.NotificationRecipient) error {
	now := time.Now()
	job := model.BackupJob{ID: "notification-test", Name: "通知测试", SourceName: "DataBridge", SourceType: "System", SourceDatabase: "system", Destination: "notification-test", FinishedAt: &now}
	if err := s.applyDefaultNotificationTitle(&job); err != nil {
		return err
	}
	return s.sendBackupNotificationToRecipient(job, true, recipient)
}

func prepareNotificationRecipientInput(input model.NotificationRecipientInput, currentTarget string, allowSavedTarget bool) (model.NotificationRecipientInput, string) {
	input.MessageTitle = strings.TrimSpace(input.MessageTitle)
	if strings.TrimSpace(input.Name) == "" {
		return input, "请填写接收者名称"
	}
	switch input.Type {
	case "feishu", "dingtalk", "wechat_work", "telegram", "webhook", "email":
	default:
		return input, "不支持的接收者类型"
	}
	if input.Type == "telegram" {
		config := telegramRecipientConfig{BotToken: strings.TrimSpace(input.BotToken), ChatID: strings.TrimSpace(input.ChatID)}
		if allowSavedTarget && (config.BotToken == "" || config.ChatID == "") {
			var saved telegramRecipientConfig
			if json.Unmarshal([]byte(currentTarget), &saved) == nil {
				if config.BotToken == "" {
					config.BotToken = saved.BotToken
				}
				if config.ChatID == "" {
					config.ChatID = saved.ChatID
				}
			}
		}
		if config.BotToken == "" {
			return input, "请填写 Bot Token"
		}
		if config.ChatID == "" {
			return input, "请填写 Chat ID"
		}
		encoded, err := json.Marshal(config)
		if err != nil {
			return input, "Telegram 配置无效"
		}
		input.Target = string(encoded)
		input.BotToken = ""
		input.ChatID = ""
		return input, ""
	}
	target := strings.TrimSpace(input.Target)
	if target == "" && allowSavedTarget {
		target = strings.TrimSpace(currentTarget)
		input.Target = target
	}
	if target == "" {
		return input, "请填写通知目标"
	}
	if input.Type == "email" {
		if !strings.Contains(target, "@") {
			return input, "邮箱地址格式不正确"
		}
		return input, ""
	}
	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return input, "Webhook 地址格式不正确"
	}
	return input, ""
}
