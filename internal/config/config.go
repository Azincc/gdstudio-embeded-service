package config

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config 应用配置
type Config struct {
	Server    ServerConfig    `mapstructure:"server"`
	GDStudio  GDStudioConfig  `mapstructure:"gdstudio"`
	Navidrome NavidromeConfig `mapstructure:"navidrome"`
	Storage   StorageConfig   `mapstructure:"storage"`
	Worker    WorkerConfig    `mapstructure:"worker"`
	Database  DatabaseConfig  `mapstructure:"database"`
	Redis     RedisConfig     `mapstructure:"redis"`
	Security  SecurityConfig  `mapstructure:"security"`
	Logging   LoggingConfig   `mapstructure:"logging"`
	Metrics   MetricsConfig   `mapstructure:"metrics"`
}

type ServerConfig struct {
	Port int    `mapstructure:"port"`
	Mode string `mapstructure:"mode"` // debug / release
}

type GDStudioConfig struct {
	BaseURL    string            `mapstructure:"base_url"`
	Mirrors    map[string]string `mapstructure:"mirrors"`
	Timeout    time.Duration     `mapstructure:"timeout"`
	RetryCount int               `mapstructure:"retry_count"`
}

type NavidromeConfig struct {
	BaseURL     string        `mapstructure:"base_url"`
	Username    string        `mapstructure:"username"`
	Password    string        `mapstructure:"password"`
	APIVersion  string        `mapstructure:"api_version"`
	ScanTimeout time.Duration `mapstructure:"scan_timeout"`
}

type StorageConfig struct {
	WorkDir           string   `mapstructure:"work_dir"`
	MusicDir          string   `mapstructure:"music_dir"`
	PathTemplate      string   `mapstructure:"path_template"`
	AllowedExtensions []string `mapstructure:"allowed_extensions"`
}

type WorkerConfig struct {
	MaxConcurrent    int           `mapstructure:"max_concurrent"`
	DownloadTimeout  time.Duration `mapstructure:"download_timeout"`
	TagWriteTimeout  time.Duration `mapstructure:"tag_write_timeout"`
	MoveTimeout      time.Duration `mapstructure:"move_timeout"`
	ScanTimeout      time.Duration `mapstructure:"scan_timeout"`
	RetryMaxAttempts int           `mapstructure:"retry_max_attempts"`
	RetryDelay       time.Duration `mapstructure:"retry_delay"`
}

type DatabaseConfig struct {
	Driver          string        `mapstructure:"driver"` // sqlite / postgres
	DSN             string        `mapstructure:"dsn"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns"`
	MaxOpenConns    int           `mapstructure:"max_open_conns"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
}

type RedisConfig struct {
	URL        string `mapstructure:"url"`
	DB         int    `mapstructure:"db"`
	MaxRetries int    `mapstructure:"max_retries"`
}

type SecurityConfig struct {
	APIKeys              []APIKey  `mapstructure:"api_keys"`
	RateLimit            RateLimit `mapstructure:"rate_limit"`
	AllowedDownloadHosts []string  `mapstructure:"allowed_download_hosts"`
}

type APIKey struct {
	Key  string `mapstructure:"key"`
	Name string `mapstructure:"name"`
}

type RateLimit struct {
	Enabled           bool `mapstructure:"enabled"`
	RequestsPerMinute int  `mapstructure:"requests_per_minute"`
}

type LoggingConfig struct {
	Level    string `mapstructure:"level"`  // debug / info / warn / error
	Format   string `mapstructure:"format"` // json / console
	Output   string `mapstructure:"output"` // stdout / file
	FilePath string `mapstructure:"file_path"`
}

type MetricsConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Port    int    `mapstructure:"port"`
	Path    string `mapstructure:"path"`
}

// Load 加载配置
func Load(configPath string) (*Config, error) {
	v := viper.New()

	// 设置配置文件
	if configPath != "" {
		v.SetConfigFile(configPath)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath("./configs")
		v.AddConfigPath(".")
	}

	// 自动读取环境变量
	v.AutomaticEnv()

	// 环境变量覆盖
	v.BindEnv("server.port", "PORT")
	v.BindEnv("gdstudio.base_url", "GD_API_BASE")
	v.BindEnv("navidrome.base_url", "NAVIDROME_BASE_URL")
	v.BindEnv("navidrome.username", "NAVIDROME_USER")
	v.BindEnv("navidrome.password", "NAVIDROME_PASSWORD")
	v.BindEnv("database.dsn", "DATABASE_URL")
	v.BindEnv("redis.url", "REDIS_URL")
	v.BindEnv("worker.max_concurrent", "MAX_CONCURRENT_JOBS")
	v.BindEnv("worker.download_timeout", "DOWNLOAD_TIMEOUT")
	v.BindEnv("logging.level", "LOG_LEVEL")

	// 读取配置文件
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	// 兼容纯数字秒值（例如 DOWNLOAD_TIMEOUT=600）
	normalizeDurationValues(v, []string{
		"gdstudio.timeout",
		"navidrome.scan_timeout",
		"worker.download_timeout",
		"worker.tag_write_timeout",
		"worker.move_timeout",
		"worker.scan_timeout",
		"worker.retry_delay",
		"database.conn_max_lifetime",
	})

	// 解析配置
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// 应用默认值
	setDefaults(&cfg)

	// 兼容 REDIS_URL 同时支持 host:port 与 redis://host:port/db
	if err := normalizeRedisAddress(&cfg.Redis); err != nil {
		return nil, fmt.Errorf("failed to parse redis config: %w", err)
	}

	return &cfg, nil
}

func setDefaults(cfg *Config) {
	if cfg.Server.Port == 0 {
		cfg.Server.Port = 8080
	}
	if cfg.Server.Mode == "" {
		cfg.Server.Mode = "release"
	}
	if cfg.GDStudio.Timeout == 0 {
		cfg.GDStudio.Timeout = 15 * time.Second
	}
	if cfg.Navidrome.APIVersion == "" {
		cfg.Navidrome.APIVersion = "1.16.1"
	}
	if cfg.Worker.MaxConcurrent == 0 {
		cfg.Worker.MaxConcurrent = 3
	}
	if cfg.Worker.DownloadTimeout == 0 {
		cfg.Worker.DownloadTimeout = 600 * time.Second
	}
	if cfg.Logging.Level == "" {
		cfg.Logging.Level = "info"
	}
	if cfg.Logging.Format == "" {
		cfg.Logging.Format = "json"
	}
	if cfg.Logging.Output == "" {
		cfg.Logging.Output = "stdout"
	}
}

func normalizeDurationValues(v *viper.Viper, keys []string) {
	for _, key := range keys {
		raw := strings.TrimSpace(v.GetString(key))
		if raw == "" {
			continue
		}
		if _, err := time.ParseDuration(raw); err == nil {
			continue
		}
		if isDigits(raw) {
			v.Set(key, raw+"s")
		}
	}
}

func isDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

func normalizeRedisAddress(redisCfg *RedisConfig) error {
	raw := strings.TrimSpace(redisCfg.URL)
	if raw == "" {
		return nil
	}

	// asynq 的 Addr 需要 host:port；若已经是该格式则直接使用
	if !strings.Contains(raw, "://") {
		redisCfg.URL = raw
		return nil
	}

	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("invalid REDIS_URL %q: %w", raw, err)
	}

	if u.Scheme != "redis" && u.Scheme != "rediss" {
		return fmt.Errorf("unsupported REDIS_URL scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("invalid REDIS_URL %q: missing host", raw)
	}

	redisCfg.URL = u.Host

	// 若未单独配置 DB，则尝试从 /<db> 提取
	if redisCfg.DB != 0 {
		return nil
	}
	path := strings.Trim(u.Path, "/")
	if path == "" {
		return nil
	}

	db, err := strconv.Atoi(path)
	if err != nil || db < 0 {
		return fmt.Errorf("invalid REDIS_URL database index %q", path)
	}
	redisCfg.DB = db

	return nil
}
