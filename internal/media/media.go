// Package media 管理中转富媒体: 通过 PocketBase 的 file 字段存储, 由 PB 负责 S3 生命周期
package media

import (
	"context"
	"fmt"
	"time"

	"github.com/pocketbase/dbx"
	"github.com/pocketbase/pocketbase/core"
	"github.com/pocketbase/pocketbase/tools/filesystem"
	"github.com/shynome/go-onebots/db"
	"github.com/shynome/go-onebots/internal/storage"
)

const (
	// dateLayout PocketBase 存库用的时间格式
	dateLayout = "2006-01-02 15:04:05.000Z"
	// touchInterval 命中缓存刷新 last_used 的最小间隔
	// 热点图片每条消息都会被引用, 一小时一次足够精确, 又不至于频繁写库
	touchInterval = time.Hour
)

type Store struct {
	app    core.App
	s3     *storage.S3
	expiry time.Duration
}

func New(app core.App) *Store {
	s3, err := storage.New(app)
	if err != nil {
		app.Logger().Warn("S3 不可用, 将不支持中转媒体(本地文件 / base64 / 内网 URL)", "reason", err)
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
	return s.save(botID, kind, f, nil)
}

// SaveBytes 保存内存数据, 返回可访问 URL
func (s *Store) SaveBytes(botID, kind, filename string, data []byte) (string, error) {
	f, err := filesystem.NewFileFromBytes(data, filename)
	if err != nil {
		return "", fmt.Errorf("构造文件失败: %w", err)
	}
	return s.save(botID, kind, f, nil)
}

// Image 已中转的图片: URL 与尺寸
type Image struct {
	URL    string
	Width  int
	Height int
}

// FindImage 按来源查已中转的图片, 用于跨消息去重
// 同一个图片链接只下载/转存一次
func (s *Store) FindImage(botID, source string) (Image, bool, error) {
	if source == "" {
		return Image{}, false, nil
	}
	recs, err := s.app.FindRecordsByFilter(db.TableMedia,
		"bot = {:bot} && source = {:source}", "-created", 1, 0,
		dbx.Params{"bot": botID, "source": source},
	)
	if err != nil {
		return Image{}, false, err
	}
	if len(recs) == 0 {
		return Image{}, false, nil
	}
	// 命中即视为一次使用, 刷新后清理不会把它当过期媒体删掉
	s.touch(recs[0])
	url, err := s.recordURL(recs[0])
	if err != nil {
		return Image{}, false, err
	}
	return Image{
		URL:    url,
		Width:  recs[0].GetInt("width"),
		Height: recs[0].GetInt("height"),
	}, true, nil
}

// SaveImage 保存图片并记录来源与尺寸, 便于后续按来源去重
func (s *Store) SaveImage(botID, source, filename string, data []byte, img Image) (string, error) {
	f, err := filesystem.NewFileFromBytes(data, filename)
	if err != nil {
		return "", fmt.Errorf("构造文件失败: %w", err)
	}
	return s.save(botID, "image", f, map[string]any{
		"source": source,
		"width":  img.Width,
		"height": img.Height,
	})
}

func (s *Store) save(botID, kind string, f *filesystem.File, extra map[string]any) (string, error) {
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
	rec.Set("last_used", time.Now())
	rec.Set("file", f)
	for k, v := range extra {
		rec.Set(k, v)
	}

	// PB 保存时会写入 S3, 删除记录时自动删除对象
	if err := s.app.Save(rec); err != nil {
		return "", fmt.Errorf("保存中转文件失败: %w", err)
	}
	return s.recordURL(rec)
}

// touch 刷新媒体最近使用时间
// 直接写 SQL, 不走 app.Save, 以免顺带把 updated(最后修改时间) 刷成使用时间
func (s *Store) touch(rec *core.Record) {
	if last := rec.GetDateTime("last_used").Time(); !last.IsZero() && time.Since(last) < touchInterval {
		return
	}
	_, err := s.app.DB().
		NewQuery("UPDATE " + db.TableMedia + " SET last_used = {:now} WHERE id = {:id}").
		Bind(dbx.Params{"now": time.Now().UTC().Format(dateLayout), "id": rec.Id}).
		Execute()
	if err != nil {
		s.app.Logger().Warn("刷新媒体使用时间失败", "error", err, "media", rec.Id)
	}
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

// Cleanup 删除 cutoff 前已不再使用的记录, 关联文件由 PocketBase 自动删除
// 按 last_used 判定: 老图只要还在被引用就会一直续期
func (s *Store) Cleanup(cutoff time.Time, batch int) (int, error) {
	if batch <= 0 {
		batch = 200
	}

	deleted := 0
	for {
		recs, err := s.app.FindRecordsByFilter(db.TableMedia,
			"(last_used < {:cutoff} || last_used = null)", "last_used", batch, 0,
			dbx.Params{"cutoff": cutoff.UTC().Format(dateLayout)},
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
