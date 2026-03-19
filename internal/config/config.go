package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

// Config 应用配置
type Config struct {
	Server      ServerConfig      `mapstructure:"server"`
	GDStudio    GDStudioConfig    `mapstructure:"gdstudio"`
	Navidrome   NavidromeConfig   `mapstructure:"navidrome"`
	Storage     StorageConfig     `mapstructure:"storage"`
	Worker      WorkerConfig      `mapstructure:"worker"`
	Database    DatabaseConfig    `mapstructure:"database"`
	Security    SecurityConfig    `mapstructure:"security"`
	Logging     LoggingConfig     `mapstructure:"logging"`
	Metrics     MetricsConfig     `mapstructure:"metrics"`
	MusicBrainz MusicBrainzConfig `mapstructure:"musicbrainz"`
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
	PollInterval     time.Duration `mapstructure:"poll_interval"`
	DownloadTimeout  time.Duration `mapstructure:"download_timeout"`
	TagWriteTimeout  time.Duration `mapstructure:"tag_write_timeout"`
	MoveTimeout      time.Duration `mapstructure:"move_timeout"`
	ScanTimeout      time.Duration `mapstructure:"scan_timeout"`
	RetryMaxAttempts int           `mapstructure:"retry_max_attempts"`
	RetryDelay       time.Duration `mapstructure:"retry_delay"`
}

type DatabaseConfig struct {
	Driver          string        `mapstructure:"driver"` // sqlite
	DSN             string        `mapstructure:"dsn"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns"`
	MaxOpenConns    int           `mapstructure:"max_open_conns"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
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

// MusicBrainzConfig MusicBrainz 元数据查询配置（可选）
type MusicBrainzConfig struct {
	Enabled        bool          `mapstructure:"enabled"`
	BaseURL        string        `mapstructure:"base_url"`
	CoverArtURL    string        `mapstructure:"cover_art_url"`
	AcoustIDURL    string        `mapstructure:"acoustid_url"`
	AcoustIDClient string        `mapstructure:"acoustid_client"`
	UserAgent      string        `mapstructure:"user_agent"`
	RateLimitMs    int           `mapstructure:"rate_limit_ms"`
	Timeout        time.Duration `mapstructure:"timeout"`
	RetryCount     int           `mapstructure:"retry_count"`
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
	v.BindEnv("worker.max_concurrent", "MAX_CONCURRENT_JOBS")
	v.BindEnv("worker.poll_interval", "JOB_POLL_INTERVAL")
	v.BindEnv("worker.download_timeout", "DOWNLOAD_TIMEOUT")
	v.BindEnv("logging.level", "LOG_LEVEL")
	v.BindEnv("musicbrainz.enabled", "MUSICBRAINZ_ENABLED")
	v.BindEnv("musicbrainz.base_url", "MUSICBRAINZ_BASE_URL")
	v.BindEnv("musicbrainz.cover_art_url", "COVERARTARCHIVE_BASE_URL")
	v.BindEnv("musicbrainz.acoustid_url", "ACOUSTID_BASE_URL")
	v.BindEnv("musicbrainz.acoustid_client", "ACOUSTID_CLIENT")
	v.BindEnv("musicbrainz.user_agent", "MUSICBRAINZ_USER_AGENT")
	v.BindEnv("musicbrainz.retry_count", "MUSICBRAINZ_RETRY_COUNT")

	// 读取配置文件
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("failed to read config: %w", err)
	}

	// 兼容纯数字秒值（例如 DOWNLOAD_TIMEOUT=600）
	normalizeDurationValues(v, []string{
		"gdstudio.timeout",
		"navidrome.scan_timeout",
		"worker.download_timeout",
		"worker.poll_interval",
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

	// 从环境变量覆盖首个 API Key，便于 Docker Compose 从 .env 注入。
	applyAPIKeyOverride(v, &cfg)

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
	if cfg.Worker.PollInterval == 0 {
		cfg.Worker.PollInterval = 2 * time.Second
	}
	if cfg.Worker.DownloadTimeout == 0 {
		cfg.Worker.DownloadTimeout = 600 * time.Second
	}
	if cfg.Database.Driver == "" {
		cfg.Database.Driver = "sqlite"
	}
	if cfg.Database.DSN == "" {
		cfg.Database.DSN = "file:/work/data/embed.db?_journal_mode=WAL&_busy_timeout=5000"
	}
	if cfg.Database.MaxIdleConns == 0 {
		cfg.Database.MaxIdleConns = 1
	}
	if cfg.Database.MaxOpenConns == 0 {
		cfg.Database.MaxOpenConns = 1
	}
	if cfg.Database.ConnMaxLifetime == 0 {
		cfg.Database.ConnMaxLifetime = time.Hour
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
	if cfg.MusicBrainz.BaseURL == "" {
		cfg.MusicBrainz.BaseURL = "https://musicbrainz.org/ws/2"
	}
	if cfg.MusicBrainz.CoverArtURL == "" {
		cfg.MusicBrainz.CoverArtURL = "https://coverartarchive.org"
	}
	if cfg.MusicBrainz.AcoustIDURL == "" {
		cfg.MusicBrainz.AcoustIDURL = "https://api.acoustid.org/v2"
	}
	if cfg.MusicBrainz.UserAgent == "" {
		cfg.MusicBrainz.UserAgent = "EchoEmbedService/1.0 (https://github.com/Azincc/gdstudio-embeded-service)"
	}
	if cfg.MusicBrainz.RateLimitMs == 0 {
		cfg.MusicBrainz.RateLimitMs = 1100
	}
	if cfg.MusicBrainz.Timeout == 0 {
		cfg.MusicBrainz.Timeout = 10 * time.Second
	}
	if cfg.MusicBrainz.RetryCount == 0 {
		cfg.MusicBrainz.RetryCount = 3
	}
}

func applyAPIKeyOverride(v *viper.Viper, cfg *Config) {
	apiKey := strings.TrimSpace(v.GetString("API_KEY"))
	if apiKey == "" {
		return
	}

	keyName := strings.TrimSpace(v.GetString("API_KEY_NAME"))
	if keyName == "" {
		keyName = "echo-client"
		if len(cfg.Security.APIKeys) > 0 {
			existingName := strings.TrimSpace(cfg.Security.APIKeys[0].Name)
			if existingName != "" {
				keyName = existingName
			}
		}
	}

	if len(cfg.Security.APIKeys) == 0 {
		cfg.Security.APIKeys = []APIKey{{Key: apiKey, Name: keyName}}
		return
	}

	cfg.Security.APIKeys[0].Key = apiKey
	if strings.TrimSpace(cfg.Security.APIKeys[0].Name) == "" {
		cfg.Security.APIKeys[0].Name = keyName
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
