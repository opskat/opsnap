// Package credential_entity 密钥库：服务器登录用的密码与私钥。
// 一条凭据只存一种内容（密码，或私钥与可选口令），密文由主密钥加密后保存。
package credential_entity

import (
	"context"
	"strings"

	"github.com/cago-frame/cago/pkg/i18n"

	"github.com/opskat/opsnap/internal/pkg/code"
)

// 凭据类型
const (
	KindPassword = "password" // 密码
	KindSSHKey   = "ssh_key"  // 私钥（可带口令）
)

const (
	maxNameLen     = 64
	maxUsernameLen = 64
	maxNoteLen     = 200
)

// Credential 一条登录凭据。Password、PrivateKey、Passphrase 都是主密钥加密后的密文，没有就是空串。
// 共享凭据有名称、可以被多台服务器选用；私有凭据属于一台服务器，名称是空串。
type Credential struct {
	ID         int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Name       string `gorm:"column:name;not null"`
	Kind       string `gorm:"column:kind;not null"`
	Password   string `gorm:"column:password;not null"`
	PrivateKey string `gorm:"column:private_key;not null"`
	Passphrase string `gorm:"column:passphrase;not null"`
	Username   string `gorm:"column:username;not null"`
	Note       string `gorm:"column:note;not null"`
	Shared     bool   `gorm:"column:shared;not null"`
	Createtime int64  `gorm:"column:createtime"`
	Updatetime int64  `gorm:"column:updatetime"`
}

func (Credential) TableName() string { return "credentials" }

// Check 凭据是否存在
func (c *Credential) Check(ctx context.Context) error {
	if c == nil {
		return i18n.NewError(ctx, code.CredentialNotFound)
	}
	return nil
}

// Validate 保存前校验名称、类型、内容和长度。密码、私钥此时已是要落库的密文。
func (c *Credential) Validate(ctx context.Context) error {
	if c.Shared && (strings.TrimSpace(c.Name) == "" || len([]rune(c.Name)) > maxNameLen) {
		return i18n.NewError(ctx, code.CredentialFieldInvalid, "名称不能为空，且不超过 64 个字")
	}
	switch c.Kind {
	case KindPassword:
		if c.Password == "" {
			return i18n.NewError(ctx, code.CredentialEmpty, "请填写密码")
		}
	case KindSSHKey:
		if c.PrivateKey == "" {
			return i18n.NewError(ctx, code.CredentialEmpty, "请粘贴私钥")
		}
	default:
		return i18n.NewError(ctx, code.CredentialKindInvalid)
	}
	if len([]rune(c.Username)) > maxUsernameLen {
		return i18n.NewError(ctx, code.CredentialFieldInvalid, "默认用户不能超过 64 个字")
	}
	if len([]rune(c.Note)) > maxNoteLen {
		return i18n.NewError(ctx, code.CredentialFieldInvalid, "备注不能超过 200 个字")
	}
	return nil
}

// IsShared 是否是共享凭据（能被多台服务器选用、出现在密钥页）
func (c *Credential) IsShared() bool { return c.Shared }
