package category

import (
	"context"
	"fmt"
	"log"

	"github.com/robfig/cron/v3"
)

// Updater 定时更新器
type Updater struct {
	loader      *Loader
	cron        *cron.Cron
	cronExpr    string
	filename    string
	groupConfig map[string][]string
}

// NewUpdater 创建新的更新器
func NewUpdater(loader *Loader, cronExpr, filename string, groupConfig map[string][]string) *Updater {
	return &Updater{
		loader:      loader,
		cron:        cron.New(cron.WithSeconds()), // 支持秒字段 (6 个字段格式)
		cronExpr:    cronExpr,
		filename:    filename,
		groupConfig: groupConfig,
	}
}

// Start 启动定时更新
func (u *Updater) Start(ctx context.Context) error {
	if u.cronExpr == "" {
		return nil // 未配置更新，跳过
	}

	// 添加定时任务
	_, err := u.cron.AddFunc(u.cronExpr, func() {
		if err := u.loader.Load(u.filename, u.groupConfig); err != nil {
			log.Printf("更新域名分类失败: %v\n", err)
		}
	})
	if err != nil {
		return fmt.Errorf("添加定时任务失败: %w", err)
	}

	// 启动 cron
	u.cron.Start()

	// 等待上下文取消
	go func() {
		<-ctx.Done()
		u.Stop()
	}()

	return nil
}

// Stop 停止定时更新
func (u *Updater) Stop() {
	if u.cron != nil {
		u.cron.Stop()
	}
}
