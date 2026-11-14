// Package media 管理中转富媒体: 通过 PocketBase 的 file 字段存储, 由 PB 负责 S3 生命周期
package media

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/shynome/go-onebots/db"
	"github.com/shynome/go-onebots/internal/storage"
)

type Store struct {
	app    core.App
	s3     *storage.S3
	expiry time.Duration
}

func New(app core.App) *Store {
	s3, err := storage.New(app)
	if err != nil {
		slog.Warn("S3 不可用, 将不支持中转媒体(本地文件 / base64 / 内网 URL)", "reason", err)
		s3 = nil
	}

	expiry := 7 * 24 * time.Hour
	if days := app.Settings().Logs.MaxDays; days > 0 {
		if d := time.Duration(days) * 24 * time.Hour; d < expiry {
			expiry = d
		}
	}
	if expiry < time.Hour {
		expiry = time.Hour
	}

	return &Store{app: app, s3: s3, expiry: expiry}
}

// SavePath 保存本地文件, 返回可访问 URL
func (s *Store) SavePath(botID, kind, localPath string) (string, error) {
	f, err := filesystem.NewFileFromPath(localPath)
	if err != nil {
		return "", fmt.Errorf("读取本地文件失败: %w", err)
	}
	return s.save(botID, kind, f)
}

// SaveBytes 保存内存数据, 返回可访问 URL
func (s *Store) SaveBytes(botID, kind, filename string, data []byte) (string, error) {
	f, err := filesystem.NewFileFromBytes(data, filename)
	if err != nil {
		return "", fmt.Errorf("构造文件失败: %w", err)
	}
	return s.save(botID, kind, f)
}

func (s *Store) save(botID, kind string, f *filesystem.File) (string, error) {
	if s.s3 == nil {
		return "", fmt.Errorf("未配置 S3, 不支持发送图片等中转媒体; 请改用公网可访问的 URL 或在 PocketBase 设置里配置 S3")
	}

	col, err := s.app.FindCachedCollectionByNameOrId(db.TableMedia)
	if err != nil {
		return "", err
	}

	rec := core.NewRecord(col)
	rec.Set("bot", botID)
	rec.Set("kind", kind)
	// key 是 system 字段且带唯一索引, 保留写入唯一值
	rec.Set("key", fmt.Sprintf("%s-%d", kind, time.Now().UnixNano()))
	rec.Set("file", f)

	// PB 保存时会写入 S3, 删除记录时自动删除对象
	if err := s.app.Save(rec); err != nil {
		return "", fmt.Errorf("保存中转文件失败: %w", err)
	}
	return s.recordURL(rec)
}

func (s *Store) recordURL(rec *core.Record) (string, error) {
	name := rec.GetString("file")
	if name == "" {
		return "", fmt.Errorf("中转文件未保存成功")
	}

	// PB 在 S3 中的对象 key 就是 {collectionId}/{recordId}/{filename}
	if s.s3 == nil {
		return "", fmt.Errorf("未配置 S3, 无法生成中转媒体链接")
	}
	key := fmt.Sprintf("%s/%s/%s", rec.Collection().Id, rec.Id, name)
	u, err := s.s3.PresignGet(context.Background(), key, s.expiry)
	if err != nil {
		return "", fmt.Errorf("生成 S3 预签名失败: %w", err)
	}
	return u, nil
}

// Cleanup 删除 cutoff 前的记录, 关联文件由 PocketBase 自动删除
func (s *Store) Cleanup(cutoff time.Time, batch int) (int, error) {
	if batch <= 0 {
		batch = 200
	}

	deleted := 0
	for {
		recs, err := s.app.FindRecordsByFilter(db.TableMedia,
			"created < {:cutoff}", "created", batch, 0,
			dbx.Params{"cutoff": cutoff.UTC().Format("2006-01-02 15:04:05.000Z")},
		)
		if err != nil {
			return deleted, err
		}
		if len(recs) == 0 {
			return deleted, nil
		}
		for _, rec := range recs {
			if err := s.app.Delete(rec); err != nil {
				return deleted, err
			}
			deleted++
		}
	}
}
