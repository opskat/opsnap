package kopiarepo

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kopia/kopia/repo/blob/sharded"
	"github.com/kopia/kopia/repo/format"
	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"golang.org/x/sys/unix"
)

// State 目标位置的情况
type State string

const (
	StateEmpty      State = "empty"      // 为空（本地目录不存在也算）
	StateRepository State = "repository" // 已是 kopia 仓库
	StateNotEmpty   State = "not_empty"  // 不为空，也不是 kopia 仓库
)

// ProbeResult 探测结果
type ProbeResult struct {
	State State
	// CreatedAt 仓库格式文件（kopia.repository）的写入时间，仅 StateRepository 时有值，取不到时为零值
	CreatedAt time.Time
}

// Reason 位置无法使用的原因
type Reason string

const (
	ReasonNotDirectory   Reason = "not_directory"    // 本地路径（或其上级）不是目录
	ReasonNotWritable    Reason = "not_writable"     // 没有写权限
	ReasonNoAccess       Reason = "no_access"        // 没有读权限
	ReasonBucketNotFound Reason = "bucket_not_found" // Bucket 不存在
	ReasonAccessDenied   Reason = "access_denied"    // S3 凭据错误或无权访问
	ReasonUnreachable    Reason = "unreachable"      // 其他连接错误，原因见 Err
)

// LocationError 无法连接或无法写入目标位置
type LocationError struct {
	Reason Reason
	Err    error
}

func (e *LocationError) Error() string {
	if e.Err == nil {
		return string(e.Reason)
	}
	return e.Err.Error()
}

func (e *LocationError) Unwrap() error { return e.Err }

func locErr(r Reason, err error) error { return &LocationError{Reason: r, Err: err} }

// writeTestPrefix 测试写入用的对象/文件名前缀，写入后立即删除
const writeTestPrefix = ".opsnap-write-test-"

// Probe 检查位置能否连接、能否写入（写入后删除一个测试对象），并判断属于哪种情况。
// 无法连接或不可写时返回 *LocationError。
func Probe(ctx context.Context, loc Location) (*ProbeResult, error) {
	switch loc.Kind {
	case KindLocal:
		return probeLocal(loc.Path)
	case KindS3:
		return probeS3(ctx, loc)
	default:
		return nil, ErrUnknownKind
	}
}

func probeLocal(path string) (*ProbeResult, error) {
	if !filepath.IsAbs(path) {
		return nil, ErrRelativePath
	}
	fi, err := os.Stat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		// 不存在的目录保存时自动创建：找到最近的已存在上级，要求它是可写目录
		return &ProbeResult{State: StateEmpty}, checkCreatable(path)
	case errors.Is(err, syscall.ENOTDIR):
		return nil, locErr(ReasonNotDirectory, err)
	case errors.Is(err, fs.ErrPermission):
		return nil, locErr(ReasonNoAccess, err)
	case err != nil:
		return nil, locErr(ReasonUnreachable, err)
	case !fi.IsDir():
		return nil, locErr(ReasonNotDirectory, fmt.Errorf("%s 不是目录", path))
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		if errors.Is(err, fs.ErrPermission) {
			return nil, locErr(ReasonNoAccess, err)
		}
		return nil, locErr(ReasonUnreachable, err)
	}
	f, err := os.CreateTemp(path, writeTestPrefix+"*")
	if err != nil {
		return nil, locErr(ReasonNotWritable, err)
	}
	_ = f.Close()
	if err := os.Remove(f.Name()); err != nil {
		return nil, locErr(ReasonNotWritable, err)
	}
	if len(entries) == 0 {
		return &ProbeResult{State: StateEmpty}, nil
	}
	if created, ok := localRepository(path); ok {
		return &ProbeResult{State: StateRepository, CreatedAt: created}, nil
	}
	return &ProbeResult{State: StateNotEmpty}, nil
}

// checkCreatable 要求 path 最近的已存在上级是当前进程可写的目录
func checkCreatable(path string) error {
	for dir := filepath.Dir(path); ; dir = filepath.Dir(dir) {
		fi, err := os.Stat(dir)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			if errors.Is(err, syscall.ENOTDIR) {
				return locErr(ReasonNotDirectory, err)
			}
			return locErr(ReasonNoAccess, err)
		}
		if !fi.IsDir() {
			return locErr(ReasonNotDirectory, fmt.Errorf("%s 不是目录", dir))
		}
		if err := unix.Access(dir, unix.W_OK); err != nil {
			return locErr(ReasonNotWritable, fmt.Errorf("%s: %w", dir, err))
		}
		return nil
	}
}

// localRepository 判断本地目录是否是 kopia 仓库，并返回格式文件的写入时间。
// 直接查看文件而不经 kopia 的 filesystem 后端：后者在 .shards 缺失时会写入它，而探测与浏览不能改动目录。
// kopia.repository 短于 kopia 的分片阈值（20 个字符），总是以 <目录>/kopia.repository.f 保存。
func localRepository(path string) (time.Time, bool) {
	fi, err := os.Stat(filepath.Join(path, format.KopiaRepositoryBlobID+sharded.CompleteBlobSuffix))
	if err != nil || !fi.Mode().IsRegular() {
		return time.Time{}, false
	}
	return fi.ModTime(), true
}

func newMinio(loc Location) (*minio.Client, error) {
	tr, err := minio.DefaultTransport(loc.UseTLS)
	if err != nil {
		return nil, locErr(ReasonUnreachable, err)
	}
	if loc.SkipVerify {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // 用户显式开启“跳过证书校验”
	}
	cli, err := minio.New(loc.Endpoint, &minio.Options{
		Creds:     credentials.NewStaticV4(loc.AccessKey, loc.SecretKey, ""),
		Secure:    loc.UseTLS,
		Region:    loc.Region,
		Transport: http.RoundTripper(tr),
		// 探测要尽快给出结果，不做多次重试（默认 10 次）
		MaxRetries: 1,
	})
	if err != nil {
		return nil, locErr(ReasonUnreachable, err)
	}
	return cli, nil
}

// s3Error 把 S3 错误归类为 LocationError
func s3Error(err error) error {
	switch minio.ToErrorResponse(err).Code {
	case minio.NoSuchBucket:
		return locErr(ReasonBucketNotFound, err)
	case minio.AccessDenied, "InvalidAccessKeyId", "SignatureDoesNotMatch":
		return locErr(ReasonAccessDenied, err)
	}
	return locErr(ReasonUnreachable, err)
}

func probeS3(ctx context.Context, loc Location) (*ProbeResult, error) {
	cli, err := newMinio(loc)
	if err != nil {
		return nil, err
	}
	exists, err := cli.BucketExists(ctx, loc.Bucket)
	if err != nil {
		return nil, s3Error(err)
	}
	if !exists {
		return nil, locErr(ReasonBucketNotFound, fmt.Errorf("bucket %q does not exist", loc.Bucket))
	}

	empty, err := s3Empty(ctx, cli, loc)
	if err != nil {
		return nil, err
	}

	testKey := loc.Prefix + writeTestPrefix + randomSuffix()
	if _, err := cli.PutObject(ctx, loc.Bucket, testKey, strings.NewReader("opsnap"), 6, minio.PutObjectOptions{}); err != nil {
		if minio.ToErrorResponse(err).Code == minio.AccessDenied {
			return nil, locErr(ReasonNotWritable, err)
		}
		return nil, s3Error(err)
	}
	if err := cli.RemoveObject(ctx, loc.Bucket, testKey, minio.RemoveObjectOptions{}); err != nil {
		return nil, locErr(ReasonNotWritable, err)
	}

	if empty {
		return &ProbeResult{State: StateEmpty}, nil
	}
	oi, err := cli.StatObject(ctx, loc.Bucket, loc.Prefix+format.KopiaRepositoryBlobID, minio.StatObjectOptions{})
	if err == nil {
		return &ProbeResult{State: StateRepository, CreatedAt: oi.LastModified}, nil
	}
	if minio.ToErrorResponse(err).Code != minio.NoSuchKey {
		return nil, s3Error(err)
	}
	return &ProbeResult{State: StateNotEmpty}, nil
}

// s3Empty 前缀下没有任何对象
func s3Empty(ctx context.Context, cli *minio.Client, loc Location) (bool, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	obj, ok := <-cli.ListObjects(ctx, loc.Bucket, minio.ListObjectsOptions{Prefix: loc.Prefix, Recursive: true, MaxKeys: 1})
	if !ok {
		return true, nil
	}
	if obj.Err != nil {
		return false, s3Error(obj.Err)
	}
	return false, nil
}

func randomSuffix() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}
