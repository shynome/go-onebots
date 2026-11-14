// 临时诊断命令: 只读 PocketBase 设置, 列出 S3 前缀下的对象 key
package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	_ "modernc.org/sqlite"
)

type s3cfg struct {
	Enabled        bool   `json:"enabled"`
	Bucket         string `json:"bucket"`
	Region         string `json:"region"`
	Endpoint       string `json:"endpoint"`
	AccessKey      string `json:"accessKey"`
	Secret         string `json:"secret"`
	ForcePathStyle bool   `json:"forcePathStyle"`
}

func main() {
	prefix := flag.String("prefix", "", "对象 key 前缀")
	dbPath := flag.String("db", "pb_data/data.db", "PocketBase 数据库路径")
	flag.Parse()

	cfg, err := loadS3(*dbPath)
	if err != nil {
		fmt.Println("读取 S3 配置失败:", err)
		os.Exit(1)
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
		fmt.Println("初始化失败:", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	n := 0
	for obj := range cli.ListObjects(ctx, cfg.Bucket, minio.ListObjectsOptions{
		Prefix:    *prefix,
		Recursive: true,
	}) {
		if obj.Err != nil {
			fmt.Println("列出失败:", obj.Err)
			os.Exit(1)
		}
		fmt.Println(obj.Key, obj.Size)
		n++
	}
	fmt.Printf("共 %d 个对象\n", n)
}

func loadS3(dbPath string) (s3cfg, error) {
	db, err := sql.Open("sqlite", "file:"+dbPath+"?mode=ro")
	if err != nil {
		return s3cfg{}, err
	}
	defer db.Close()

	var raw string
	if err := db.QueryRow("select value from _params where id='settings'").Scan(&raw); err != nil {
		return s3cfg{}, err
	}
	var s struct {
		S3 s3cfg `json:"s3"`
	}
	if err := json.Unmarshal([]byte(raw), &s); err != nil {
		return s3cfg{}, err
	}
	return s.S3, nil
}
