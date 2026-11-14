// Package media 管理中转富媒体: 上传 S3 + 记录 + 生成预签名链接
package media

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"path"
	"strings"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/shynome/go-onebots/db"
	"github.com/shynome/go-onebots/internal/storage"
)

// maxPresign 预签名有效期上限, 与 SigV4 限制一致
const maxPresign = 7 * 24 * time.Hour

type Store struct {
	app    core.App
	s3     *storage.S3
	expiry time.Duration
}

// New 基于 PocketBase 的 S3 设置构造
func New(app core.App) (*Store, error) {
	s3, err := storage.New(app)
	if err != nil {
		return nil, err
	}

	expiry := maxPresign
	if days := app.Settings().Logs.MaxDays; days > 0 {
		if d := time.Duration(days) * 24 * time.Hour; d < expiry {
			expiry = d
		}
	}
	if expiry < time.Hour {
		expiry = time.Hour
	}
	return &Store{app: app, s3: s3, expiry: expiry}, nil
}

// Save 上传文件并返回可直接给 QQ 使用的预签名 URL
func (s *Store) Save(ctx context.Context, botID, kind, filename, contentType string, r io.Reader, size int64) (string, error) {
	key := objectKey(botID, filename)

	if err := s.s3.Put(ctx, key, r, size, contentType); err != nil {
		return "", err
	}

	// 记录失败不影响发送, 只是清理时可能漏掉该对象
	if err := s.record(botID, key, kind, size, contentType); err != nil {
		_ = s.s3.Remove(ctx, []string{key})
		return "", err
	}

	return s.s3.PresignGet(ctx, key, s.expiry)
}

func (s *Store) record(botID, key, kind string, size int64, contentType string) error {
	col, err := s.app.FindCachedCollectionByNameOrId(db.TableMedia)
	if err != nil {
		return err
	}
	rec := core.NewRecord(col)
	rec.Set("bot", botID)
	rec.Set("key", key)
	rec.Set("kind", kind)
	rec.Set("size", size)
	rec.Set("content_type", contentType)
	return s.app.Save(rec)
}

func objectKey(botID, filename string) string {
	ext := strings.ToLower(path.Ext(filename))
	id := fmt.Sprintf("%d%06d", time.Now().UnixNano(), rand.IntN(1000000))
	return fmt.Sprintf("onebot-media/%s/%s/%s%s", botID, time.Now().Format("20060102"), id, ext)
}

// Cleanup 删除 cutoff 之前的中转文件, 返回删除的文件数与记录数
func (s *Store) Cleanup(ctx context.Context, cutoff time.Time, batch int) (files, records int, err error) {
	if batch <= 0 {
		batch = 200
	}
	for {
		recs, err := s.app.FindRecordsByFilter(db.TableMedia,
			"created < {:cutoff}", "created", batch, 0,
			dbx.Params{"cutoff": cutoff.UTC().Format("2006-01-02 15:04:05.000Z")},
		)
		if err != nil {
			return files, records, err
		}
		if len(recs) == 0 {
			return files, records, nil
		}

		keys := make([]string, 0, len(recs))
		for _, rec := range recs {
			if k := rec.GetString("key"); k != "" {
				keys = append(keys, k)
			}
		}
		if err := s.s3.Remove(ctx, keys); err != nil {
			// S3 删除失败时保留记录, 下次再试
			return files, records, err
		}
		files += len(keys)

		for _, rec := range recs {
			if err := s.app.Delete(rec); err != nil {
				return files, records, err
			}
			records++
		}
	}
}
