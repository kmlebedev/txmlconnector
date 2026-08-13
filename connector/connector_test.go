package connector

import (
	"path/filepath"
	"testing"
)

func TestConfigFromEnv(t *testing.T) {
	t.Setenv("TC_DLL_VER", "1.2.3")
	t.Setenv("TC_DLL_PATH", filepath.Join("vendor", "custom.dll"))
	t.Setenv("TC_DLL_LOG_DIR", filepath.Join("var", "log", "transaq"))
	t.Setenv("TC_DLL_LOG_LEVEL", "3")

	config := ConfigFromEnv()
	if config.DLLPath != filepath.Join("vendor", "custom.dll") {
		t.Fatalf("DLLPath = %q", config.DLLPath)
	}
	if config.LogDir != filepath.Join("var", "log", "transaq") {
		t.Fatalf("LogDir = %q", config.LogDir)
	}
	if config.LogLevel != 3 {
		t.Fatalf("LogLevel = %d", config.LogLevel)
	}
}

func TestConfigFromEnvUsesVersionWhenPathIsEmpty(t *testing.T) {
	t.Setenv("TC_DLL_VER", "1.2.3")
	t.Setenv("TC_DLL_PATH", "")

	config := ConfigFromEnv()
	if config.DLLPath != "txmlconnector64-1.2.3.dll" {
		t.Fatalf("DLLPath = %q", config.DLLPath)
	}
}
