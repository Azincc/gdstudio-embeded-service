package gdstudio

import (
	"crypto/md5"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/azin/gdstudio-embed-service/internal/config"
	"github.com/go-resty/resty/v2"
	"go.uber.org/zap"
)

// Client GDStudio API 客户端
type Client struct {
	cfg    *config.GDStudioConfig
	client *resty.Client
	logger *zap.Logger
}

// NewClient 创建客户端
func NewClient(cfg *config.GDStudioConfig, logger *zap.Logger) *Client {
	client := resty.New().
		SetTimeout(cfg.Timeout).
		SetRetryCount(cfg.RetryCount).
		SetHeader("User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit/605.1.15")

	return &Client{
		cfg:    cfg,
		client: client,
		logger: logger,
	}
}

// URLResult 音频 URL 结果
type URLResult struct {
	URL       string `json:"url"`
	Bitrate   int    `json:"br"`
	Size      int64  `json:"size"`
	Extension string `json:"-"`
}

// PicResult 封面结果
type PicResult struct {
	URL string `json:"url"`
}

// LyricResult 歌词结果
type LyricResult struct {
	Lyric       string `json:"lyric"`
	Translation string `json:"tlyric"`
}

// ResolveURL 解析播放链接
func (c *Client) ResolveURL(source, trackID string, br int) (*URLResult, error) {
	c.logger.Info("resolving url",
		zap.String("source", source),
		zap.String("track_id", trackID),
		zap.Int("bitrate", br))

	baseURL := c.selectBaseURL(source)
	sig := c.generateSignature(trackID)

	var result map[string]interface{}
	resp, err := c.client.R().
		SetQueryParams(map[string]string{
			"types":  "url",
			"source": source,
			"id":     trackID,
			"br":     fmt.Sprintf("%d", br),
			"s":      sig,
		}).
		SetResult(&result).
		Get(baseURL + "/api.php")

	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode())
	}

	rawURL, _ := result["url"].(string)
	if rawURL == "" || rawURL == "err" {
		if msg, ok := result["msg"].(string); ok && strings.TrimSpace(msg) != "" {
			return nil, fmt.Errorf("url resolution failed: %s", msg)
		}
		if errMsg, ok := result["error"].(string); ok && strings.TrimSpace(errMsg) != "" {
			return nil, fmt.Errorf("url resolution failed: %s", errMsg)
		}
		if code, ok := result["code"]; ok {
			return nil, fmt.Errorf("url resolution failed: code=%v", code)
		}
		return nil, fmt.Errorf("url resolution failed: empty or error response")
	}

	urlResult := &URLResult{
		URL: sanitizeURL(rawURL),
	}

	if brVal, ok := result["br"].(float64); ok {
		urlResult.Bitrate = int(brVal)
	}
	if sizeVal, ok := result["size"].(float64); ok {
		urlResult.Size = int64(sizeVal)
	}

	urlResult.Extension = extractExtension(urlResult.URL)

	c.logger.Info("url resolved",
		zap.String("url", urlResult.URL),
		zap.Int("bitrate", urlResult.Bitrate),
		zap.String("extension", urlResult.Extension))

	return urlResult, nil
}

// ResolveCover 解析封面
func (c *Client) ResolveCover(source, picID string) (string, error) {
	if picID == "" {
		return "", nil
	}

	// 某些源对不同尺寸支持不一致，按尺寸回退尝试。
	sizes := []int{1000, 640, 500, 300}
	var lastErr error
	for _, size := range sizes {
		coverURL, err := c.resolveCoverWithSize(source, picID, size)
		if err == nil {
			return coverURL, nil
		}
		lastErr = err
	}

	if lastErr != nil {
		return "", lastErr
	}

	return "", fmt.Errorf("cover url not found")
}

func (c *Client) resolveCoverWithSize(source, picID string, size int) (string, error) {
	c.logger.Debug("resolving cover",
		zap.String("source", source),
		zap.String("pic_id", picID),
		zap.Int("size", size))

	baseURL := c.selectBaseURL(source)
	sig := c.generateSignature(picID)

	var result map[string]interface{}
	resp, err := c.client.R().
		SetQueryParams(map[string]string{
			"types":  "pic",
			"source": source,
			"id":     picID,
			"size":   strconv.Itoa(size),
			"s":      sig,
		}).
		SetResult(&result).
		Get(baseURL + "/api.php")

	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode() != 200 {
		return "", fmt.Errorf("unexpected status code: %d", resp.StatusCode())
	}

	rawURL, _ := result["url"].(string)
	if rawURL == "" {
		return "", fmt.Errorf("cover url not found")
	}

	coverURL := sanitizeURL(rawURL)
	c.logger.Debug("cover resolved", zap.String("url", coverURL))

	return coverURL, nil
}

// ResolveLyrics 解析歌词
func (c *Client) ResolveLyrics(source, lyricID string) (*LyricResult, error) {
	if lyricID == "" {
		return nil, nil
	}

	c.logger.Debug("resolving lyrics",
		zap.String("source", source),
		zap.String("lyric_id", lyricID))

	baseURL := c.selectBaseURL(source)
	sig := c.generateSignature(lyricID)

	var result map[string]interface{}
	resp, err := c.client.R().
		SetQueryParams(map[string]string{
			"types":  "lyric",
			"source": source,
			"id":     lyricID,
			"s":      sig,
		}).
		SetResult(&result).
		Get(baseURL + "/api.php")

	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	if resp.StatusCode() != 200 {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode())
	}

	lyric, _ := result["lyric"].(string)
	if lyric == "" {
		return nil, fmt.Errorf("lyrics not found")
	}

	lyricResult := &LyricResult{
		Lyric: lyric,
	}

	if tlyric, ok := result["tlyric"].(string); ok {
		lyricResult.Translation = tlyric
	}

	c.logger.Debug("lyrics resolved",
		zap.Int("lyric_length", len(lyricResult.Lyric)),
		zap.Bool("has_translation", lyricResult.Translation != ""))

	return lyricResult, nil
}

// DownloadCover 下载封面数据
func (c *Client) DownloadCover(source, coverURL string) ([]byte, error) {
	if coverURL == "" {
		return nil, nil
	}

	referer := coverReferer(source)
	candidates := buildCoverCandidates(coverURL)
	var lastErr error

	for _, candidate := range candidates {
		req := c.client.R().
			SetHeader("Accept", "image/avif,image/webp,image/apng,image/*,*/*;q=0.8")
		if referer != "" {
			req.SetHeader("Referer", referer)
		}

		c.logger.Debug("downloading cover", zap.String("url", candidate))

		resp, err := req.Get(candidate)
		if err != nil {
			lastErr = fmt.Errorf("download failed: %w", err)
			continue
		}
		if resp.StatusCode() != 200 {
			lastErr = fmt.Errorf("unexpected status code: %d", resp.StatusCode())
			c.logger.Debug("cover download attempt failed",
				zap.String("url", candidate),
				zap.Int("status", resp.StatusCode()))
			continue
		}
		if len(resp.Body()) == 0 {
			lastErr = fmt.Errorf("empty cover response")
			continue
		}

		c.logger.Debug("cover downloaded",
			zap.String("url", candidate),
			zap.Int("size", len(resp.Body())))

		return resp.Body(), nil
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("cover download failed")
}

// selectBaseURL 根据 source 选择合适的 API 入口
func (c *Client) selectBaseURL(source string) string {
	source = strings.ToLower(source)

	// 根据 source 选择镜像
	switch source {
	case "migu", "kugou", "ximalaya":
		if cnURL, ok := c.cfg.Mirrors["cn"]; ok {
			return cnURL
		}
	case "joox":
		if hkURL, ok := c.cfg.Mirrors["hk"]; ok {
			return hkURL
		}
	case "qobuz", "ytmusic":
		if usURL, ok := c.cfg.Mirrors["us"]; ok {
			return usURL
		}
	}

	return c.cfg.BaseURL
}

// generateSignature 生成签名
func (c *Client) generateSignature(id string) string {
	hostname := "music.gdstudio.xyz"
	version := "20251104" // 2025.11.4 -> 20251104
	ts9 := fmt.Sprintf("%d", time.Now().UnixMilli())[:9]
	src := fmt.Sprintf("%s|%s|%s|%s", hostname, version, ts9, url.QueryEscape(id))

	hash := md5.Sum([]byte(src))
	full := fmt.Sprintf("%x", hash)

	// 取后 8 位并转大写
	if len(full) >= 8 {
		return strings.ToUpper(full[len(full)-8:])
	}

	return strings.ToUpper(full)
}

// sanitizeURL 清理 URL
func sanitizeURL(raw string) string {
	return strings.NewReplacer(
		"&amp;", "&",
		"&quot;", "\"",
		"&#x27;", "'",
	).Replace(strings.TrimSpace(raw))
}

func coverReferer(source string) string {
	switch strings.ToLower(strings.TrimSpace(source)) {
	case "netease":
		return "https://music.163.com/"
	case "qq":
		return "https://y.qq.com/"
	case "kuwo":
		return "https://www.kuwo.cn/"
	default:
		return ""
	}
}

func buildCoverCandidates(rawURL string) []string {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return []string{rawURL}
	}

	var out []string
	seen := map[string]struct{}{}
	add := func(next *url.URL) {
		if next == nil {
			return
		}
		s := next.String()
		if s == "" {
			return
		}
		if _, ok := seen[s]; ok {
			return
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}

	add(u)

	// 一些 CDN 的 query 参数会失效，尝试去掉 query。
	if u.RawQuery != "" {
		noQuery := *u
		noQuery.RawQuery = ""
		add(&noQuery)
	}

	values := u.Query()
	origParam := strings.TrimSpace(values.Get("param"))
	sizeParams := []string{"1000y1000", "640y640", "500y500", "300y300"}

	if origParam != "" {
		// 先尝试原始 param，再尝试常见尺寸回退。
		if sizeParams[0] != origParam {
			sizeParams = append([]string{origParam}, sizeParams...)
		}
		for _, param := range sizeParams {
			withParam := *u
			q := withParam.Query()
			q.Set("param", param)
			withParam.RawQuery = q.Encode()
			add(&withParam)
		}
	} else {
		// 无尺寸参数时补一个常见尺寸，提高兼容性。
		withParam := *u
		q := withParam.Query()
		q.Set("param", sizeParams[0])
		withParam.RawQuery = q.Encode()
		add(&withParam)
	}

	return out
}

// extractExtension 提取文件扩展名
func extractExtension(urlStr string) string {
	u, err := url.Parse(urlStr)
	if err != nil {
		return "mp3"
	}

	path := u.Path
	if idx := strings.LastIndex(path, "."); idx > 0 && idx < len(path)-1 {
		ext := strings.ToLower(path[idx+1:])
		// 只返回常见音频格式
		if ext == "mp3" || ext == "flac" || ext == "m4a" || ext == "ogg" {
			return ext
		}
	}

	return "mp3"
}
