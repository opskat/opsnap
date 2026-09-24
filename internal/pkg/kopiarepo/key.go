package kopiarepo

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
)

const keyAlphabet = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"

// GenerateKey 生成仓库密钥：24 个随机字母和数字，每 4 个一组用 - 连接（连字符也是密钥的一部分）。
func GenerateKey() (string, error) {
	var b strings.Builder
	limit := big.NewInt(int64(len(keyAlphabet)))
	for i := range 24 {
		if i > 0 && i%4 == 0 {
			b.WriteByte('-')
		}
		n, err := rand.Int(rand.Reader, limit)
		if err != nil {
			return "", fmt.Errorf("生成仓库密钥: %w", err)
		}
		b.WriteByte(keyAlphabet[n.Int64()])
	}
	return b.String(), nil
}

// Fingerprint 密钥指纹：SHA-256 摘要十六进制的前 4 位与后 4 位（大写），只用于核对是否同一把密钥。
func Fingerprint(key string) string {
	sum := sha256.Sum256([]byte(key))
	h := strings.ToUpper(hex.EncodeToString(sum[:]))
	return h[:4] + "···" + h[len(h)-4:]
}
