package dbtls

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"strings"

	"databridge/internal/model"
)

func Config(host string, config model.ConnectionTLSConfig, verifyServerName bool) (*tls.Config, error) {
	config = Normalize(config)
	if config.Empty() && !verifyServerName {
		return nil, nil
	}
	result := &tls.Config{
		MinVersion: tls.VersionTLS12,
		ServerName: strings.TrimSpace(host),
	}
	if config.CACert != "" {
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM([]byte(config.CACert)) {
			return nil, fmt.Errorf("CA 证书 PEM 解析失败")
		}
		result.RootCAs = roots
	}
	if config.ClientCert != "" || config.ClientKey != "" {
		if config.ClientCert == "" || config.ClientKey == "" {
			return nil, fmt.Errorf("客户端证书和私钥需要同时填写")
		}
		cert, err := tls.X509KeyPair([]byte(config.ClientCert), []byte(config.ClientKey))
		if err != nil {
			return nil, fmt.Errorf("客户端证书或私钥解析失败: %w", err)
		}
		result.Certificates = []tls.Certificate{cert}
	}
	return result, nil
}

func Normalize(config model.ConnectionTLSConfig) model.ConnectionTLSConfig {
	return model.ConnectionTLSConfig{
		CACert:     strings.TrimSpace(config.CACert),
		ClientCert: strings.TrimSpace(config.ClientCert),
		ClientKey:  strings.TrimSpace(config.ClientKey),
	}
}
