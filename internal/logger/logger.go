package logger

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"wxcode/internal/config"
)

// ============================================================
// 级别
// ============================================================

type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

func (l Level) String() string {
	switch l {
	case LevelDebug:
		return "DEBUG"
	case LevelInfo:
		return "INFO"
	case LevelWarn:
		return "WARN"
	case LevelError:
		return "ERROR"
	}
	return "INFO"
}

// parseLogLevel 解析 LOG_LEVEL；认不出来就当 info（宁可多打也别瞎猜成 debug）。
func parseLogLevel(s string) Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return LevelDebug
	case "warn", "warning":
		return LevelWarn
	case "error", "err":
		return LevelError
	default:
		return LevelInfo
	}
}

// ============================================================
// 配置
// ============================================================

type LogConfig struct {
	level  Level
	format string // text | json
	output string // stdout | stderr | both
	access bool   // 是否打印访问日志
	skip   map[string]bool
}

// loadLogConfig 从环境变量读配置。读不到就用默认值，
// 保证裸跑二进制（不经过容器）也能正常出日志。
func LoadLogConfig() LogConfig {
	cfg := LogConfig{
		level:  parseLogLevel(os.Getenv("LOG_LEVEL")),
		format: strings.ToLower(strings.TrimSpace(os.Getenv("LOG_FORMAT"))),
		output: strings.ToLower(strings.TrimSpace(os.Getenv("LOG_OUTPUT"))),
		access: config.EnvOnDefault("LOG_ACCESS", true),
		skip:   map[string]bool{},
	}
	switch cfg.format {
	case "json":
		cfg.format = "json"
	default:
		cfg.format = "text"
	}
	switch cfg.output {
	case "stdout":
		cfg.output = "stdout"
	case "stderr":
		cfg.output = "stderr"
	default:
		cfg.output = "both"
	}
	// 健康检查默认跳过：Docker HEALTHCHECK 每 30s 打一次，
	// 不降噪的话日志里九成都是 /api/health。
	paths := os.Getenv("LOG_SKIP_PATHS")
	if strings.TrimSpace(paths) == "" {
		paths = "/api/health"
	}
	for _, p := range strings.Split(paths, ",") {
		if p = strings.TrimSpace(p); p != "" {
			cfg.skip[p] = true
		}
	}
	return cfg
}

// ============================================================
// 全局状态
// ============================================================

var (
	LogMu sync.Mutex // 保证一整行日志不会被并发请求拆开

	LogCfg              = LoadLogConfig()
	LogStdout io.Writer = os.Stdout
	LogStderr io.Writer = os.Stderr
)

// setLogConfig 测试用：替换全局日志配置。
func SetLogConfig(cfg LogConfig) {
	LogMu.Lock()
	LogCfg = cfg
	LogMu.Unlock()
}

// currentLogConfig 返回当前配置副本（测试断言用）。
// SetLevel 返回一个只改了级别的副本。
func (c LogConfig) SetLevel(l Level) LogConfig { c.level = l; return c }

// SetFormat 返回一个只改了输出格式的副本。
func (c LogConfig) SetFormat(f string) LogConfig { c.format = f; return c }

// SetAccess 返回一个只改了「是否记录访问日志」的副本。
func (c LogConfig) SetAccess(on bool) LogConfig { c.access = on; return c }

// SetSkipPaths 返回一个只改了跳过路径集合的副本。
func (c LogConfig) SetSkipPaths(m map[string]bool) LogConfig { c.skip = m; return c }

// SetOutput 返回一个只改了输出目标的副本。
func (c LogConfig) SetOutput(o string) LogConfig { c.output = o; return c }

// Level 返回当前日志级别。
func (c LogConfig) Level() Level { return c.level }

// Format 返回输出格式（text / json）。
func (c LogConfig) Format() string { return c.format }

// Output 返回输出目标（stdout / stderr / both / file）。
func (c LogConfig) Output() string { return c.output }

// SkipPaths 返回跳过访问日志的路径集合。
func (c LogConfig) SkipPaths() map[string]bool { return c.skip }

// AccessLog 报告是否输出访问日志。
func (c LogConfig) AccessLog() bool { return c.access }

// SkipPath 报告某个路径是否跳过访问日志。
func (c LogConfig) SkipPath(p string) bool { return c.skip[p] }

func CurrentLogConfig() LogConfig {
	LogMu.Lock()
	defer LogMu.Unlock()
	return LogCfg
}

// ============================================================
// 写日志
// ============================================================

// logAt 是所有日志函数的底座。
//
// msg 是人读的主消息，kv 是可选的键值对（"key", value, "key2", value2……），
// 奇数个参数时最后那个会被忽略，不会 panic。
func LogAt(ctx context.Context, lvl Level, msg string, kv ...any) {
	LogMu.Lock()
	cfg := LogCfg
	LogMu.Unlock()

	if lvl < cfg.level {
		return
	}

	// request_id 从 context 里自动带，业务日志不用每次手写。
	// 已经显式传过 request_id 就不重复加（否则字段会出现两次）。
	if ctx != nil {
		if rid := requestIDFrom(ctx); rid != "" && !hasKey(kv, "request_id") {
			kv = append(kv, "request_id", rid)
		}
	}

	rec := logRecord{
		TS:    time.Now().Format("2006-01-02T15:04:05.000Z07:00"),
		Level: lvl.String(),
		Msg:   msg,
	}
	for i := 0; i+1 < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok {
			key = fmt.Sprint(kv[i])
		}
		rec.add(key, kv[i+1])
	}

	var line string
	if cfg.format == "json" {
		line = rec.jsonLine()
	} else {
		line = rec.textLine()
	}

	LogMu.Lock()
	defer LogMu.Unlock()
	w := logWriterFor(cfg.output, lvl)
	_, _ = w.Write([]byte(line))
}

// logWriterFor 按级别与配置决定写到哪个流。
func logWriterFor(output string, lvl Level) io.Writer {
	switch output {
	case "stdout":
		return LogStdout
	case "stderr":
		return LogStderr
	default: // both
		if lvl >= LevelWarn {
			return LogStderr
		}
		return LogStdout
	}
}

// hasKey 判断键值对里是否已有某个 key。
func hasKey(kv []any, key string) bool {
	for i := 0; i+1 < len(kv); i += 2 {
		if k, ok := kv[i].(string); ok && k == key {
			return true
		}
	}
	return false
}

// logRecord 是一条待输出的日志。
type logRecord struct {
	TS    string
	Level string
	Msg   string
	keys  []string
	vals  []any
}

func (r *logRecord) add(k string, v any) {
	r.keys = append(r.keys, k)
	r.vals = append(r.vals, v)
}

// textLine 渲染成人类可读的一行：
//
//	2026-11-09T09:55:12.345+08:00 INFO  服务已启动 version=1.0.0 pid=1234
func (r *logRecord) textLine() string {
	var b strings.Builder
	b.WriteString(r.TS)
	b.WriteByte(' ')
	b.WriteString(fmt.Sprintf("%-5s", r.Level))
	b.WriteByte(' ')
	b.WriteString(r.Msg)
	for i, k := range r.keys {
		b.WriteByte(' ')
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(textValue(r.vals[i]))
	}
	b.WriteByte('\n')
	return b.String()
}

// textValue 把值转成一行内可读的文本；含空格/引号的值加引号。
func textValue(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		if strings.ContainsAny(t, " \t\"=") {
			return strconv.Quote(t)
		}
		return t
	case error:
		return strconv.Quote(t.Error())
	default:
		return fmt.Sprint(v)
	}
}

// jsonLine 渲染成一行 JSON，字段顺序固定，便于采集端解析。
func (r *logRecord) jsonLine() string {
	var b strings.Builder
	b.WriteString(`{"ts":`)
	b.WriteString(jsonString(r.TS))
	b.WriteString(`,"level":`)
	b.WriteString(jsonString(r.Level))
	b.WriteString(`,"msg":`)
	b.WriteString(jsonString(r.Msg))
	for i, k := range r.keys {
		b.WriteByte(',')
		b.WriteString(jsonString(k))
		b.WriteByte(':')
		b.WriteString(jsonValue(r.vals[i]))
	}
	b.WriteString("}\n")
	return b.String()
}

// jsonString 把任意值转成 JSON 片段；出错就退化成带引号的字符串。
func jsonString(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return strconv.Quote(fmt.Sprint(v))
	}
	return string(b)
}

// jsonValue 保留原始类型（数字还是数字、布尔还是布尔），
// 这样采集端可以直接按数值字段做聚合。
func jsonValue(v any) string {
	switch t := v.(type) {
	case nil:
		return "null"
	case error:
		return jsonString(t.Error())
	case string, bool,
		int, int8, int16, int32, int64,
		uint, uint8, uint16, uint32, uint64,
		float32, float64:
		return jsonString(t)
	default:
		return jsonString(fmt.Sprint(v))
	}
}

// ============================================================
// 对外接口
// ============================================================

func Debug(msg string, kv ...any) { LogAt(context.Background(), LevelDebug, msg, kv...) }

func Info(msg string, kv ...any) { LogAt(context.Background(), LevelInfo, msg, kv...) }

func Warn(msg string, kv ...any) { LogAt(context.Background(), LevelWarn, msg, kv...) }

func Error(msg string, kv ...any) { LogAt(context.Background(), LevelError, msg, kv...) }

// logCtx 按级别输出一条带上下文的日志（自动附 request_id）。
func LogCtx(ctx context.Context, lvl Level, msg string, kv ...any) {
	LogAt(ctx, lvl, msg, kv...)
}

// logRaw 原样输出多行文本（启动横幅用），不加时间戳与级别前缀。
// 走的是同一把锁，保证不会和别的日志交错。
func Raw(s string) {
	LogMu.Lock()
	defer LogMu.Unlock()
	w := LogStdout
	if LogCfg.output == "stderr" {
		w = LogStderr
	}
	_, _ = io.WriteString(w, s)
}

// ============================================================
// 请求 ID
// ============================================================

// reqIDCtxKey 是本包私有的 context 键（每个包各自定义，互不干扰）。
const ReqIDCtxKey ctxKey = "wxcode-reqid"

// newRequestID 生成 8 位十六进制随机串。
func NewRequestID() string {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		// 随机数失败不该让请求挂掉，用时间戳顶上。
		return strconv.FormatInt(time.Now().UnixNano()&0xffffffff, 16)
	}
	return hex.EncodeToString(b)
}

// sanitizeRequestID 清洗客户端传进来的 X-Request-ID：
// 只留字母数字、连字符、下划线，最多 64 位。
// 不校验的话，攻击者能把换行塞进日志里伪造出一整行假日志。
func SanitizeRequestID(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > 64 {
		return ""
	}
	var b strings.Builder
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '-', c == '_':
			b.WriteRune(c)
		}
	}
	out := b.String()
	if out == "" {
		return ""
	}
	return out
}

// requestIDFrom 从 context 里取 request_id。
func requestIDFrom(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if v, ok := ctx.Value(ReqIDCtxKey).(string); ok {
		return v
	}
	return ""
}

// withReqIDContext 往 context 里塞一个 request_id（测试与内部调用用）。
func WithReqIDContext(ctx context.Context, rid string) context.Context {
	return context.WithValue(ctx, ReqIDCtxKey, rid)
}

// requestIDOf 从请求的 context 里取 request_id。
func RequestIDOf(r *http.Request) string {
	if r == nil {
		return ""
	}
	return requestIDFrom(r.Context())
}

// ============================================================
// panic 兜底
// ============================================================

// stackTrace 取当前堆栈，给 panic 日志用。
// 只取本 goroutine 的栈（debug.Stack 会带上别的 goroutine，噪音大）。
func StackTrace() string {
	buf := make([]byte, 8<<10)
	n := runtime.Stack(buf, false)
	return string(buf[:n])
}

type ctxKey string
