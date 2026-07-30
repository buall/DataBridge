package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"

	"databridge/internal/license"
)

const maxLicenseUploadBytes = 1024 * 1024

func (s *Server) licenseActive() bool {
	return s.license == nil || s.license.Active()
}

func (s *Server) licenseStatus(w http.ResponseWriter, _ *http.Request) {
	if s.license == nil {
		writeJSON(w, http.StatusOK, license.Status{
			Activated: true,
			State:     "active",
			Message:   "许可证校验未启用",
		})
		return
	}
	writeJSON(w, http.StatusOK, s.license.Status())
}

func (s *Server) licenseRequest(w http.ResponseWriter, _ *http.Request) {
	if s.license == nil {
		writeError(w, http.StatusNotFound, "license_not_enabled", "许可证校验未启用")
		return
	}
	data, err := s.license.Request()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "license_request_failed", err.Error())
		return
	}
	status := s.license.Status()
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="databridge-license-request-%s.json"`, status.DeploymentID))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) activateLicense(w http.ResponseWriter, r *http.Request) {
	if s.license == nil {
		writeError(w, http.StatusNotFound, "license_not_enabled", "许可证校验未启用")
		return
	}
	var input struct {
		ActivationCode string `json:"activationCode"`
	}
	if !decodeJSON(w, r, &input) {
		return
	}
	if err := s.license.Activate(r.Context(), input.ActivationCode); err != nil {
		writeError(w, http.StatusBadRequest, "license_activation_failed", err.Error())
		return
	}
	s.reconcileScheduledBackupRuns()
	writeJSON(w, http.StatusOK, s.license.Status())
}

func (s *Server) importLicense(w http.ResponseWriter, r *http.Request) {
	if s.license == nil {
		writeError(w, http.StatusNotFound, "license_not_enabled", "许可证校验未启用")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxLicenseUploadBytes)
	if err := r.ParseMultipartForm(maxLicenseUploadBytes); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_license_upload", "无法读取许可证文件")
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "license_file_required", "请选择许可证文件")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxLicenseUploadBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_license_upload", "无法读取许可证文件")
		return
	}
	if len(data) > maxLicenseUploadBytes {
		writeError(w, http.StatusRequestEntityTooLarge, "license_file_too_large", "许可证文件不能超过 1 MB")
		return
	}
	if err := s.license.Import(data); err != nil {
		code := "invalid_license"
		if errors.Is(err, license.ErrInvalid) {
			code = "invalid_license_signature"
		}
		writeError(w, http.StatusBadRequest, code, err.Error())
		return
	}
	s.reconcileScheduledBackupRuns()
	writeJSON(w, http.StatusOK, s.license.Status())
}

func (s *Server) licenseMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.license.Active() {
			writeError(w, http.StatusForbidden, "license_required", "当前部署尚未激活，请先导入有效许可证")
			return
		}
		next.ServeHTTP(w, r)
	})
}
