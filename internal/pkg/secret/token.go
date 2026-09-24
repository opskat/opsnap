package secret

import (
	"crypto/sha256"
	"encoding/hex"
)

// HashToken 返回会话标识、API 令牌等随机令牌落库用的哈希（SHA-256 十六进制）。
// 这类令牌本身是高熵随机数，不需要密码那样的慢哈希；改动算法会让已落库的会话与令牌全部失效。
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
