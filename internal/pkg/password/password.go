// Package password 使用 argon2id 哈希与校验管理员密码，结果以 PHC 字符串格式保存。
package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// 参数取 OWASP 推荐的 argon2id 最低配置（19 MiB、2 次迭代、1 线程）：
// 每次哈希的内存占用有限，并发登录请求不至于耗尽内存
const (
	memory  uint32 = 19 * 1024
	time    uint32 = 2
	threads uint8  = 1
	saltLen        = 16
	keyLen  uint32 = 32
)

var b64 = base64.RawStdEncoding

func Hash(pw string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("生成盐: %w", err)
	}
	key := argon2.IDKey([]byte(pw), salt, time, memory, threads, keyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, memory, time, threads, b64.EncodeToString(salt), b64.EncodeToString(key)), nil
}

// Verify 校验密码；哈希格式无法解析时视为不匹配
func Verify(pw, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil || p == 0 {
		return false
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil {
		return false
	}
	want, err := b64.DecodeString(parts[5])
	if err != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want))) //nolint:gosec // len(want) 来自自身生成的哈希，远小于 uint32 上限
	return subtle.ConstantTimeCompare(got, want) == 1
}
