// Package storage 封装 PocketBase 配置的 S3, 用于富媒体中转
package storage

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/pocketbase/pocketbase/core"
)

// maxPresignExpiry S3 SigV4 预签名 URL 的最大有效期
const maxPresignExpiry = 7 * 24 * time.Hour

type S3 struct {
	cli    *minio.Client
	bucket string
}

// New 从 PocketBase 的 S3 设置构造客户端, 未启用时返回错误
func New(app core.App) (*S3, error) {
	cfg := app.Settings().S3
	if !cfg.Enabled {
		return nil, fmt.Errorf("PocketBase 未启用 S3 存储")
	}
	if cfg.Bucket == "" || cfg.AccessKey == "" || cfg.Secret == "" {
		return nil, fmt.Errorf("S3 配置不完整")
	}

	endpoint := cfg.Endpoint
	secure := true
	if strings.HasPrefix(endpoint, "http://") {
		endpoint = strings.TrimPrefix(endpoint, "http://")
		secure = false
	}
	endpoint = strings.TrimPrefix(endpoint, "https://")

	lookup := minio.BucketLookupAuto
	if cfg.ForcePathStyle {
		lookup = minio.BucketLookupPath
	}

	cli, err := minio.New(endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKey, cfg.Secret, ""),
		Secure:       secure,
		Region:       cfg.Region,
		BucketLookup: lookup,
	})
	if err != nil {
		return nil, fmt.Errorf("初始化 S3 客户端失败: %w", err)
	}
	return &S3{cli: cli, bucket: cfg.Bucket}, nil
}

func (s *S3) Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error {
	_, err := s.cli.PutObject(ctx, s.bucket, key, r, size, minio.PutObjectOptions{
		ContentType: contentType,
	})
	if err != nil {
		return fmt.Errorf("上传 S3 失败: %w", err)
	}
	return nil
}

// PresignGet 生成下载链接, 超过 7 天会被截断(SigV4 限制)
func (s *S3) PresignGet(ctx context.Context, key string, expiry time.Duration) (string, error) {
	if expiry <= 0 || expiry > maxPresignExpiry {
		expiry = maxPresignExpiry
	}
	u, err := s.cli.PresignedGetObject(ctx, s.bucket, key, expiry, nil)
	if err != nil {
		return "", fmt.Errorf("生成预签名链接失败: %w", err)
	}
	return u.String(), nil
}

func (s *S3) Remove(ctx context.Context, keys []string) error {
	if len(keys) == 0 {
		return nil
	}
	ch := make(chan minio.ObjectInfo, len(keys))
	for _, k := range keys {
		ch <- minio.ObjectInfo{Key: k}
	}
	close(ch)

	for obj := range s.cli.RemoveObjects(ctx, s.bucket, ch, minio.RemoveObjectsOptions{}) {
		if obj.Err != nil {
			return fmt.Errorf("删除 S3 对象 %s 失败: %w", obj.ObjectName, obj.Err)
		}
	}
	return nil
}
