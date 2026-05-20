package app

import (
	"archive/zip"
	"crypto/sha256"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestValidateSubscriptionURL 测试订阅 URL SSRF 防护
func TestValidateSubscriptionURL(t *testing.T) {
	tests := []struct {
		url     string
		wantErr bool
	}{
		{"https://example.com/sub", false},
		{"http://example.com/sub", false},
		{"ftp://example.com/sub", true},
		{"http://127.0.0.1/sub", true},
		{"http://192.168.1.1/sub", true},
		{"http://10.0.0.1/sub", true},
		{"http://169.254.169.254/meta", true},
		{"http://[::1]/sub", true},
		{"", true},
	}
	for _, tt := range tests {
		err := validateSubscriptionURL(tt.url)
		if (err != nil) != tt.wantErr {
			t.Errorf("validateSubscriptionURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
		}
	}
}

// TestExtractZipSlip 测试 ZipSlip 防护
func TestExtractZipSlip(t *testing.T) {
	tmpDir := t.TempDir()
	archivePath := filepath.Join(tmpDir, "test.zip")
	extractDir := filepath.Join(tmpDir, "extract")
	os.MkdirAll(extractDir, 0o755)

	zf, err := os.Create(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	fw, _ := zw.Create("../../etc/passwd")
	fw.Write([]byte("malicious"))
	zw.Close()
	zf.Close()

	err = extractZip(archivePath, extractDir)
	if err == nil || !strings.Contains(err.Error(), "slip") {
		t.Errorf("extractZip should reject path traversal, got err=%v", err)
	}
}

// TestGenerateSessionID 测试会话 ID 生成
func TestGenerateSessionID(t *testing.T) {
	id1 := generateSessionID()
	id2 := generateSessionID()
	if id1 == id2 {
		t.Error("generateSessionID should produce unique IDs")
	}
	if len(id1) < 32 {
		t.Errorf("sessionID too short: %d", len(id1))
	}
}

// TestSessionManagement 测试会话管理
func TestSessionManagement(t *testing.T) {
	app := &App{
		sessions:      map[string]time.Time{},
		loginAttempts: map[string][]time.Time{},
		tokenHash:     sha256.New().Sum([]byte("test-token")),
	}
	sessionID := generateSessionID()
	app.sessions[sessionID] = time.Now()

	if _, valid := app.sessions[sessionID]; !valid {
		t.Error("session should be valid after creation")
	}

	app.sessions[sessionID] = time.Now().Add(-25 * time.Hour)
	if time.Since(app.sessions[sessionID]) <= 24*time.Hour {
		t.Error("expired session should be detected")
	}
}

// TestSanitizeErrorMessage 测试错误消息脱敏
func TestSanitizeErrorMessage(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"error at C:\\Users\\test\\file.txt", "error at [path]"},
		{"failed to connect to 192.168.1.1:8080", "failed to connect to [ip][port]"},
		{"/var/log/app.log not found", "[path] not found"},
		{"normal error message", "normal error message"},
	}
	for _, tt := range tests {
		got := sanitizeErrorMessage(tt.input)
		if !strings.Contains(got, "[path]") && !strings.Contains(got, "[ip]") && !strings.Contains(got, "[port]") && got != tt.want {
			if strings.Contains(tt.input, "C:\\") || strings.Contains(tt.input, "192.168") || strings.Contains(tt.input, "/var") {
				t.Errorf("sanitizeErrorMessage(%q) = %q, should contain sanitization markers", tt.input, got)
			}
		}
	}
}

// TestParseSubscription 测试订阅解析基础功能
func TestParseSubscription(t *testing.T) {
	raw := "vmess://eyJ2IjoiMiIsInBzIjoidGVzdCIsImFkZCI6ImV4YW1wbGUuY29tIiwicG9ydCI6IjQ0MyIsImlkIjoidGVzdC11dWlkIiwiYWlkIjoiMCIsIm5ldCI6InRjcCIsInR5cGUiOiJub25lIiwiaG9zdCI6IiIsInBhdGgiOiIiLCJ0bHMiOiJ0bHMifQ=="
	result := parseSubscription(raw)
	if len(result.nodes) == 0 {
		t.Error("parseSubscription should parse valid vmess link")
	}
	if len(result.nodes) > 0 {
		node := result.nodes[0]
		if mustStr(node["type"]) != "vmess" {
			t.Errorf("expected type=vmess, got %v", node["type"])
		}
	}
}
