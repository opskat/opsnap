// Package kopiarepo 把存储位置（本地目录或 S3 兼容存储）映射为 kopia 仓库：
// 探测位置状态、建库、用密钥连接并读取快照数、浏览本地目录，以及仓库密钥的生成与指纹。
// 这里不做持久化，也不认识存储记录；密钥与凭据由调用方解密后传入，本包不记录它们。
package kopiarepo

import (
	"errors"
	"path/filepath"
	"strings"
)

// Kind 存储类型
type Kind string

const (
	KindLocal Kind = "local"
	KindS3    Kind = "s3"
)

var (
	ErrRelativePath   = errors.New("本地目录必须是绝对路径")
	ErrEndpointScheme = errors.New("endpoint 不含协议（去掉 http:// 或 https://）")
	ErrUnknownKind    = errors.New("未知的存储类型")
)

// Location 一个存储位置及访问它所需的全部参数
type Location struct {
	Kind Kind

	// 本地目录
	Path string

	// S3 兼容存储
	Endpoint   string
	Region     string
	Bucket     string
	Prefix     string
	AccessKey  string
	SecretKey  string
	UseTLS     bool
	SkipVerify bool
}

// Normalize 校验并规范化位置：本地路径 Clean 为绝对路径；S3 Endpoint 转小写，前缀去掉首尾斜杠后以斜杠结尾。
func (l *Location) Normalize() error {
	switch l.Kind {
	case KindLocal:
		if !filepath.IsAbs(l.Path) {
			return ErrRelativePath
		}
		l.Path = filepath.Clean(l.Path)
	case KindS3:
		l.Endpoint = strings.ToLower(strings.TrimSpace(l.Endpoint))
		if strings.Contains(l.Endpoint, "://") {
			return ErrEndpointScheme
		}
		l.Bucket = strings.TrimSpace(l.Bucket)
		l.Prefix = strings.Trim(strings.TrimSpace(l.Prefix), "/")
		if l.Prefix != "" {
			l.Prefix += "/"
		}
	default:
		return ErrUnknownKind
	}
	return nil
}

// Key 位置的唯一标识：两个存储的 Key 相同即指向同一位置。需先 Normalize。
func (l Location) Key() string {
	if l.Kind == KindLocal {
		return "local:" + l.Path
	}
	return "s3:" + l.Endpoint + "/" + l.Bucket + "/" + l.Prefix
}

// String 用于展示的位置：本地为绝对路径，S3 为 s3://<bucket>/<前缀>
func (l Location) String() string {
	if l.Kind == KindLocal {
		return l.Path
	}
	return "s3://" + l.Bucket + "/" + l.Prefix
}
