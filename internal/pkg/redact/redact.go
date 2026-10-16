// Package redact 在日志写入或推送前遮盖已知的凭据（密码、私钥、仓库密钥、令牌等）
package redact

import (
	"encoding/json"
	"sort"
	"strings"
	"sync"
)

// Mask 替换凭据的占位
const Mask = "******"

// Redactor 记录本次运行涉及的凭据，并在文本中遮盖它们。可并发使用。
type Redactor struct {
	mu      sync.RWMutex
	secrets []string // 按长度从长到短，先替换长的，避免短凭据是长凭据子串时漏出剩余部分
	seen    map[string]bool
}

// New 创建并加入初始凭据
func New(secrets ...string) *Redactor {
	r := &Redactor{seen: map[string]bool{}}
	r.Add(secrets...)
	return r
}

// Add 加入凭据；空值忽略。多行凭据（私钥）同时按行加入，JSON 转义后的形式也一并加入。
func (r *Redactor) Add(secrets ...string) {
	var forms []string
	for _, s := range secrets {
		if strings.TrimSpace(s) == "" {
			continue
		}
		forms = append(forms, s)
		if strings.ContainsAny(s, "\r\n") {
			for _, line := range strings.FieldsFunc(s, func(c rune) bool { return c == '\n' || c == '\r' }) {
				if line = strings.TrimSpace(line); line != "" {
					forms = append(forms, line)
				}
			}
		}
		if b, err := json.Marshal(s); err == nil {
			if esc := string(b[1 : len(b)-1]); esc != s {
				forms = append(forms, esc)
			}
		}
	}
	if len(forms) == 0 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, f := range forms {
		if !r.seen[f] {
			r.seen[f] = true
			r.secrets = append(r.secrets, f)
		}
	}
	sort.SliceStable(r.secrets, func(i, j int) bool { return len(r.secrets[i]) > len(r.secrets[j]) })
}

// String 返回遮盖凭据后的文本
func (r *Redactor) String(s string) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, sec := range r.secrets {
		if strings.Contains(s, sec) {
			s = strings.ReplaceAll(s, sec, Mask)
		}
	}
	return s
}
