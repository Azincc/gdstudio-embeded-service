package navidrome

import (
	"crypto/md5"
	"fmt"
	"strings"
	"time"

	"github.com/azin/gdstudio-embed-service/internal/config"
	"github.com/go-resty/resty/v2"
	"go.uber.org/zap"
)

// Client Navidrome/Subsonic API 客户端
type Client struct {
	cfg    *config.NavidromeConfig
	client *resty.Client
	logger *zap.Logger
	token  string
	salt   string
}

// NewClient 创建客户端
func NewClient(cfg *config.NavidromeConfig, logger *zap.Logger) *Client {
	client := resty.New().
		SetTimeout(30 * time.Second).
		SetHeader("User-Agent", "echo-embed/1.0")

	// 生成 token 和 salt
	salt := generateSalt()
	token := generateToken(cfg.Password, salt)

	return &Client{
		cfg:    cfg,
		client: client,
		logger: logger,
		token:  token,
		salt:   salt,
	}
}

// ScanStatus 扫描状态
type ScanStatus struct {
	Scanning bool  `json:"scanning"`
	Count    int64 `json:"count"`
}

// StartScan 触发扫描
func (c *Client) StartScan() error {
	c.logger.Info("starting navidrome scan")

	var result struct {
		SubsonicResponse struct {
			Status  string `json:"status"`
			Version string `json:"version"`
		} `json:"subsonic-response"`
	}

	resp, err := c.client.R().
		SetQueryParams(c.authParams()).
		SetResult(&result).
		Get(c.cfg.BaseURL + "/rest/startScan")

	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode() != 200 {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode())
	}

	if result.SubsonicResponse.Status != "ok" {
		return fmt.Errorf("scan start failed: status=%s", result.SubsonicResponse.Status)
	}

	c.logger.Info("scan started successfully")
	return nil
}

// GetScanStatus 查询扫描状态
func (c *Client) GetScanStatus() (*ScanStatus, error) {
	c.logger.Debug("getting scan status")

	var result struct {
		SubsonicResponse struct {
			Status     string     `json:"status"`
			ScanStatus ScanStatus `json:"scanStatus"`
		} `json:"subsonic-response"`
	}

	resp, err := c.client.R().
		SetQueryParams(c.authParams()).
		SetResult(&result).
		Get(c.cfg.BaseURL + "/rest/getScanStatus")

	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode())
	}

	if result.SubsonicResponse.Status != "ok" {
		return nil, fmt.Errorf("get scan status failed: status=%s", result.SubsonicResponse.Status)
	}

	c.logger.Debug("scan status retrieved",
		zap.Bool("scanning", result.SubsonicResponse.ScanStatus.Scanning),
		zap.Int64("count", result.SubsonicResponse.ScanStatus.Count))

	return &result.SubsonicResponse.ScanStatus, nil
}

// WaitForScan 等待扫描完成
func (c *Client) WaitForScan(timeout time.Duration) error {
	c.logger.Info("waiting for scan to complete", zap.Duration("timeout", timeout))

	deadline := time.Now().Add(timeout)
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-time.After(time.Until(deadline)):
			return fmt.Errorf("scan timeout after %v", timeout)
		case <-ticker.C:
			status, err := c.GetScanStatus()
			if err != nil {
				c.logger.Warn("failed to get scan status", zap.Error(err))
				continue
			}

			if !status.Scanning {
				c.logger.Info("scan completed", zap.Int64("count", status.Count))
				return nil
			}

			c.logger.Debug("scan in progress", zap.Int64("count", status.Count))
		}
	}
}

// Ping 测试连接
func (c *Client) Ping() error {
	c.logger.Debug("pinging navidrome")

	var result struct {
		SubsonicResponse struct {
			Status  string `json:"status"`
			Version string `json:"version"`
		} `json:"subsonic-response"`
	}

	resp, err := c.client.R().
		SetQueryParams(c.authParams()).
		SetResult(&result).
		Get(c.cfg.BaseURL + "/rest/ping")

	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode() != 200 {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode())
	}

	if result.SubsonicResponse.Status != "ok" {
		return fmt.Errorf("ping failed: status=%s", result.SubsonicResponse.Status)
	}

	c.logger.Info("ping successful", zap.String("version", result.SubsonicResponse.Version))
	return nil
}

// authParams 生成认证参数
func (c *Client) authParams() map[string]string {
	return map[string]string{
		"u": c.cfg.Username,
		"t": c.token,
		"s": c.salt,
		"v": c.cfg.APIVersion,
		"c": "echo-embed",
		"f": "json",
	}
}

// generateSalt 生成随机 salt
func generateSalt() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// generateToken 生成认证 token
func generateToken(password, salt string) string {
	hash := md5.Sum([]byte(password + salt))
	return fmt.Sprintf("%x", hash)
}

// NormalizeURL 标准化 URL（移除尾部斜杠）
func NormalizeURL(urlStr string) string {
	return strings.TrimRight(urlStr, "/")
}
