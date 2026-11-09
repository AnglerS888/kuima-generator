package config

// Version 版本号，可在构建时注入：-ldflags "-X main.Version=1.0.1"
var Version = "1.0.0"

// BuildTime 构建时间，可在构建时注入：-ldflags "-X main.BuildTime=2026-01-01T00:00:00Z"
var BuildTime = "unknown"
