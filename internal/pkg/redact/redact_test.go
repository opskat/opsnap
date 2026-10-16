package redact

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestRedactor(t *testing.T) {
	t.Run("遮盖每个已知凭据的所有出现", func(t *testing.T) {
		r := New("SECRET-DB-PW", "SECRET-REPO-KEY")
		got := r.String("mysqldump: auth SECRET-DB-PW failed; repo SECRET-REPO-KEY; again SECRET-DB-PW")
		assert.NotContains(t, got, "SECRET-DB-PW")
		assert.NotContains(t, got, "SECRET-REPO-KEY")
		assert.Equal(t, "mysqldump: auth "+Mask+" failed; repo "+Mask+"; again "+Mask, got)
	})

	t.Run("后加入的凭据同样生效，空值忽略", func(t *testing.T) {
		r := New()
		r.Add("", "tok-123")
		assert.Equal(t, "x "+Mask, r.String("x tok-123"))
		assert.Equal(t, "plain", r.String("plain"))
	})

	t.Run("较长的凭据先替换，包含关系不会漏出后缀", func(t *testing.T) {
		r := New("abc", "abcdef")
		assert.Equal(t, Mask, r.String("abcdef"))
	})

	t.Run("多行私钥逐行遮盖", func(t *testing.T) {
		key := "-----BEGIN OPENSSH PRIVATE KEY-----\nb3BlbnNzaC1rZXktdjEAAAAA\nQyNTUxOQAAACD\n-----END OPENSSH PRIVATE KEY-----\n"
		r := New(key)
		got := r.String("ssh: bad key line b3BlbnNzaC1rZXktdjEAAAAA here")
		assert.NotContains(t, got, "b3BlbnNzaC1rZXktdjEAAAAA")
	})

	t.Run("JSON 转义后的形式也遮盖", func(t *testing.T) {
		r := New(`pa"ss\word`)
		got := r.String(`{"message":"auth pa\"ss\\word"}`)
		assert.NotContains(t, got, `pa\"ss\\word`)
	})

	t.Run("并发加入与替换安全", func(t *testing.T) {
		r := New()
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
			wg.Add(2)
			go func() { defer wg.Done(); r.Add("SECRET-" + strings.Repeat("x", 5)) }()
			go func() { defer wg.Done(); _ = r.String("SECRET-xxxxx") }()
		}
		wg.Wait()
		assert.Equal(t, Mask, r.String("SECRET-xxxxx"))
	})
}
