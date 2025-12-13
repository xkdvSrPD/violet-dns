package utils

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

// DownloadFile 下载文件
func DownloadFile(url, destPath string) error {
	// 检查文件是否已存在
	if _, err := os.Stat(destPath); err == nil {
		return nil // 文件已存在，跳过下载
	}

	// 创建目录
	dir := filepath.Dir(destPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}

	// 创建 HTTP 客户端
	client := &http.Client{
		Timeout: 5 * time.Minute,
	}

	// 发起请求
	resp, err := client.Get(url)
	if err != nil {
		return fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("下载失败: HTTP %d", resp.StatusCode)
	}

	// 创建临时文件
	tmpFile := destPath + ".tmp"
	out, err := os.Create(tmpFile)
	if err != nil {
		return fmt.Errorf("创建临时文件失败: %w", err)
	}
	defer out.Close()

	// 写入文件
	if _, err := io.Copy(out, resp.Body); err != nil {
		os.Remove(tmpFile)
		return fmt.Errorf("写入文件失败: %w", err)
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
