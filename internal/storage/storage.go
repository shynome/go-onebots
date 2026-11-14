// Package storage 封装 PocketBase 配置的 S3, 用于富媒体中转
package storage

import (
	"context"
	"fmt"
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

// List 列出前缀下的对象 key, 用于诊断
func (s *S3) List(ctx context.Context, prefix string) ([]string, error) {
	var keys []string
	for obj := range s.cli.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    prefix,
		Recursive: true,
	}) {
		if obj.Err != nil {
			return keys, obj.Err
		}
		keys = append(keys, obj.Key)
	}
	return keys, nil
}
