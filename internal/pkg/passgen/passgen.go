// Package passgen 用 crypto/rand 生成随机字符串，供密钥页的「随机生成」用。
// 字符集去掉容易看错的 0 O 1 l I；生成失败按普通错误返回，不 panic。
package passgen

import (
	"crypto/rand"
	"errors"
	"fmt"
)

// 字符集：小写去掉 l、大写去掉 I 与 O、数字去掉 0 与 1，另加常用符号
const (
	lowerChars   = "abcdefghijkmnopqrstuvwxyz"
	upperChars   = "ABCDEFGHJKLMNPQRSTUVWXYZ"
	digitChars   = "23456789"
	symbolChars  = "!@#$%^&*()-_=+[]{};:,.?/~"
	passwordSize = 32 // 密码长度
)

// charset 生成用的字符集；不含 0 O 1 l I
const charset = lowerChars + upperChars + digitChars + symbolChars

// Random 返回 n 位随机字符串。字节用拒绝采样映射到字符集，避免取模带来的偏差；
// 长度必须大于 0。
func Random(n int) (string, error) {
	if n <= 0 {
		return "", errors.New("随机字符串的长度必须大于 0")
	}
	// 只接受小于 limit 的字节，余下的拒绝掉，保证每个字符等概率
	limit := 256 - 256%len(charset)
	out := make([]byte, 0, n)
	buf := make([]byte, 64)
	for len(out) < n {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("生成随机字符串失败: %w", err)
		}
		for _, b := range buf {
			if int(b) >= limit {
				continue
			}
			out = append(out, charset[int(b)%len(charset)])
			if len(out) == n {
				break
			}
		}
	}
	return string(out), nil
}

// Password 生成 32 位密码
func Password() (string, error) { return Random(passwordSize) }
