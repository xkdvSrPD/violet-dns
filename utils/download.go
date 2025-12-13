package utils

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// DownloadFile 下载文件
func DownloadFile(url, destPath string) error {
	// 检查文件是否已存在且大小合理（大于 1KB）
	if info, err := os.Stat(destPath); err == nil {
		if info.Size() > 1024 {
			return nil // 文件已存在且大小合理，跳过下载
		}
		// 文件太小，可能是损坏的，删除重新下载
		os.Remove(destPath)
	}

	// 创建目录
	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	// 创建 HTTP 客户端，优先使用 IPv4
	client := &http.Client{
		Timeout: 5 * time.Minute,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          10,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}

	// 创建请求
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return fmt.Errorf("创建请求失败: %w", err)
	}

	// 设置 User-Agent
	req.Header.Set("User-Agent", "violet-dns/1.0")

	// 发起请求
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败: HTTP %d (URL: %s)", resp.StatusCode, url)
	}

	// 创建临时文件
	tmpFile := destPath + ".tmp"
	out, err := os.Create(tmpFile)
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}

	// 写入文件
	n, err := io.Copy(out, resp.Body)
	out.Close() // 立即关闭文件

	if err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("写入文件失败: %w", err)
	}

	// 验证文件大小
	if n < 1024 {
		os.Remove(tmpFile)
		return fmt.Errorf("下载的文件太小: %d 字节", n)
	}

	// 重命名临时文件
	if err := os.Rename(tmpFile, destPath); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("重命名文件失败: %w", err)
	}

	return nil
}

// FileExists 检查文件是否存在
func FileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
