// Package credential_svc 密钥库：共享的服务器登录凭据的增删改查。
// 密码、私钥与口令都用主密钥加密后保存，接口只返回「有没有内容」，永不返回密文或明文。
package credential_svc

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/cago-frame/cago/pkg/i18n"

	credentialapi "github.com/opskat/opsnap/internal/api/credential"
	"github.com/opskat/opsnap/internal/model/entity/credential_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/pkg/sshconn"
	"github.com/opskat/opsnap/internal/repository/credential_repo"
)

// serverNameSeparator 列出「被这些服务器使用」时的分隔符
const serverNameSeparator = "、"

// CredentialSvc 密钥服务，只操作共享密钥
type CredentialSvc interface {
	List(ctx context.Context) (*credentialapi.ListResponse, error)
	Get(ctx context.Context, id int64) (*credentialapi.GetResponse, error)
	// Create 添加一条共享密钥；密码或私钥必填
	Create(ctx context.Context, req *credentialapi.CreateRequest) (*credentialapi.CreateResponse, error)
	// Update 整体保存一条共享密钥；密码、私钥、口令留空或为占位时保留已保存的值
	Update(ctx context.Context, req *credentialapi.UpdateRequest) (*credentialapi.UpdateResponse, error)
	// Delete 删除一条共享密钥；被服务器使用时拒绝并带出使用者名称
	Delete(ctx context.Context, id int64) error
}

type credentialSvc struct {
	now func() time.Time
}

// NewCredential 创建密钥服务；now 注入时钟
func NewCredential(now func() time.Time) CredentialSvc { return &credentialSvc{now: now} }

var defaultCredential = NewCredential(time.Now)

// Credential 取得密钥服务
func Credential() CredentialSvc { return defaultCredential }

// RegisterCredential 替换服务实现
func RegisterCredential(i CredentialSvc) { defaultCredential = i }

// find 读取一条共享密钥；不存在和私有凭据都报「密钥不存在」，
// 私有凭据只能在它那台服务器的抽屉里看和改。
func (s *credentialSvc) find(ctx context.Context, id int64) (*credential_entity.Credential, error) {
	c, err := credential_repo.Credential().Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := c.Check(ctx); err != nil {
		return nil, err
	}
	if !c.IsShared() {
		return nil, i18n.NewError(ctx, code.CredentialNotFound)
	}
	return c, nil
}

func (s *credentialSvc) List(ctx context.Context) (*credentialapi.ListResponse, error) {
	list, err := credential_repo.Credential().ListShared(ctx)
	if err != nil {
		return nil, err
	}
	resp := &credentialapi.ListResponse{List: make([]*credentialapi.Item, 0, len(list))}
	for _, c := range list {
		refs, err := credential_repo.Credential().ServersByCredentialID(ctx, c.ID)
		if err != nil {
			return nil, err
		}
		resp.List = append(resp.List, toItem(c, refs))
	}
	return resp, nil
}

func (s *credentialSvc) Get(ctx context.Context, id int64) (*credentialapi.GetResponse, error) {
	item, err := s.view(ctx, id)
	if err != nil {
		return nil, err
	}
	return &credentialapi.GetResponse{Item: *item}, nil
}

func (s *credentialSvc) view(ctx context.Context, id int64) (*credentialapi.Item, error) {
	c, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	refs, err := credential_repo.Credential().ServersByCredentialID(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	return toItem(c, refs), nil
}

func (s *credentialSvc) Create(ctx context.Context, req *credentialapi.CreateRequest) (*credentialapi.CreateResponse, error) {
	// 与轮换主密钥互斥，见 secret.BeginWrite
	endWrite := secret.BeginWrite()
	defer endWrite()
	cred, err := s.build(ctx, nil, req.Input())
	if err != nil {
		return nil, err
	}
	if err := s.checkName(ctx, cred.Name, 0); err != nil {
		return nil, err
	}
	if err := s.validate(ctx, cred); err != nil {
		return nil, err
	}
	now := s.now().Unix()
	cred.Createtime, cred.Updatetime = now, now
	if err := credential_repo.Credential().Create(ctx, cred); err != nil {
		return nil, duplicateError(ctx, err, cred.Name)
	}
	item, err := s.view(ctx, cred.ID)
	if err != nil {
		return nil, err
	}
	return &credentialapi.CreateResponse{Item: *item}, nil
}

func (s *credentialSvc) Update(ctx context.Context, req *credentialapi.UpdateRequest) (*credentialapi.UpdateResponse, error) {
	// 与轮换主密钥互斥，见 secret.BeginWrite
	endWrite := secret.BeginWrite()
	defer endWrite()
	old, err := s.find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	cred, err := s.build(ctx, old, req.Input())
	if err != nil {
		return nil, err
	}
	if err := s.checkName(ctx, cred.Name, cred.ID); err != nil {
		return nil, err
	}
	if err := s.validate(ctx, cred); err != nil {
		return nil, err
	}
	cred.Updatetime = s.now().Unix()
	if err := credential_repo.Credential().Update(ctx, cred); err != nil {
		return nil, duplicateError(ctx, err, cred.Name)
	}
	item, err := s.view(ctx, cred.ID)
	if err != nil {
		return nil, err
	}
	return &credentialapi.UpdateResponse{Item: *item}, nil
}

func (s *credentialSvc) Delete(ctx context.Context, id int64) error {
	c, err := s.find(ctx, id)
	if err != nil {
		return err
	}
	n, err := credential_repo.Credential().CountServersByCredentialID(ctx, id)
	if err != nil {
		return err
	}
	if n > 0 {
		refs, err := credential_repo.Credential().ServersByCredentialID(ctx, id)
		if err != nil {
			return err
		}
		names := make([]string, 0, len(refs))
		for _, r := range refs {
			names = append(names, r.Name)
		}
		return i18n.NewError(ctx, code.CredentialInUse, n, strings.Join(names, serverNameSeparator))
	}
	return credential_repo.Credential().Delete(ctx, c.ID)
}

// build 把提交的内容合并成实体。old 为 nil 表示新建。内容此时已是密文。
//
// 密码、私钥、口令的空串与占位「已设置」都表示保留已保存的值（新建时没有已保存的值，等于没有内容）；
// 类型从一端切到另一端时，另一侧的内容一并清掉。
func (s *credentialSvc) build(ctx context.Context, old *credential_entity.Credential, in *credentialapi.Input) (*credential_entity.Credential, error) {
	cred := &credential_entity.Credential{Shared: true, Name: strings.TrimSpace(in.Name)}
	if old != nil {
		cred.ID = old.ID
		cred.Createtime = old.Createtime
	}
	kind := in.Kind
	if kind == "" && old != nil {
		kind = old.Kind
	}
	cred.Kind = kind
	cred.Username = strings.TrimSpace(in.Username)
	cred.Note = strings.TrimSpace(in.Note)

	// 类型没变才保留已保存的另一半；切换类型时从空白开始
	if old != nil && old.Kind == kind {
		cred.Password, cred.PrivateKey, cred.Passphrase = old.Password, old.PrivateKey, old.Passphrase
	}
	var err error
	switch kind {
	case credential_entity.KindPassword:
		cred.PrivateKey, cred.Passphrase = "", ""
		if cred.Password, err = keepOrEncrypt(in.Password, cred.Password); err != nil {
			return nil, err
		}
	case credential_entity.KindSSHKey:
		cred.Password = ""
		if cred.PrivateKey, err = keepOrEncrypt(in.PrivateKey, cred.PrivateKey); err != nil {
			return nil, err
		}
		if cred.Passphrase, err = keepOrEncrypt(in.Passphrase, cred.Passphrase); err != nil {
			return nil, err
		}
	}
	return cred, nil
}

// validate 校验名称、类型、内容是否齐全，并确认私钥能用口令解析
func (s *credentialSvc) validate(ctx context.Context, cred *credential_entity.Credential) error {
	if err := cred.Validate(ctx); err != nil {
		return err
	}
	return checkPrivateKey(ctx, cred)
}

// checkName 确认名称没有被别的共享密钥占用；selfID 是正在编辑的那条
func (s *credentialSvc) checkName(ctx context.Context, name string, selfID int64) error {
	list, err := credential_repo.Credential().ListShared(ctx)
	if err != nil {
		return err
	}
	for _, c := range list {
		if c.ID != selfID && c.Name == name {
			return i18n.NewError(ctx, code.CredentialNameDuplicate, name)
		}
	}
	return nil
}

// keepOrEncrypt 处理一项内容：空串与占位保留已保存的密文，其余加密。
// 返回的错误不带内容，避免把明文写进错误文案或日志。
func keepOrEncrypt(input, saved string) (string, error) {
	switch input {
	case "", secret.Masked:
		return saved, nil
	}
	sec := secret.Default()
	if sec == nil {
		return "", errors.New("主密钥未加载，无法加密密钥")
	}
	return sec.Encrypt(input)
}

// checkPrivateKey 私钥类型时确认私钥能用口令解析
func checkPrivateKey(ctx context.Context, cred *credential_entity.Credential) error {
	if cred.Kind != credential_entity.KindSSHKey || cred.PrivateKey == "" {
		return nil
	}
	sec := secret.Default()
	if sec == nil {
		return errors.New("主密钥未加载，无法解密密钥")
	}
	key, err := sec.Decrypt(cred.PrivateKey)
	if err != nil {
		return err
	}
	pass := ""
	if cred.Passphrase != "" {
		if pass, err = sec.Decrypt(cred.Passphrase); err != nil {
			return err
		}
	}
	if _, err := sshconn.ParsePrivateKey(key, pass); err != nil {
		return i18n.NewError(ctx, code.CredentialPrivateKeyInvalid)
	}
	return nil
}

// duplicateError 把共享名称的唯一索引错误翻译成 CredentialNameDuplicate，不把数据库错误暴露给调用方。
// 并发写入时 checkName 可能同时通过，这里兜住。
func duplicateError(ctx context.Context, err error, name string) error {
	if err == nil {
		return nil
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "unique constraint failed") || strings.Contains(msg, "unique constraint") {
		return i18n.NewError(ctx, code.CredentialNameDuplicate, name)
	}
	return err
}

// toItem 转换成接口返回的结构：只带回有没有内容，不带密文或明文
func toItem(c *credential_entity.Credential, refs []*credential_repo.ServerRef) *credentialapi.Item {
	item := &credentialapi.Item{
		ID:            c.ID,
		Name:          c.Name,
		Kind:          c.Kind,
		Username:      c.Username,
		Note:          c.Note,
		HasPassphrase: c.Passphrase != "",
		Servers:       make([]*credentialapi.ServerRef, 0, len(refs)),
		CreatedAt:     c.Createtime,
		UpdatedAt:     c.Updatetime,
	}
	switch c.Kind {
	case credential_entity.KindPassword:
		item.HasSecret = c.Password != ""
	default:
		item.HasSecret = c.PrivateKey != ""
	}
	for _, r := range refs {
		item.Servers = append(item.Servers, &credentialapi.ServerRef{
			ID: r.ID, Name: r.Name, Host: r.Host, Port: r.Port, Username: r.Username,
		})
	}
	return item
}
