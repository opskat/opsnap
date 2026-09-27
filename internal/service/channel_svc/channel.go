// Package channel_svc 实现网络通道（SSH 跳板、SOCKS5 代理）的增删改、测试连接与主机密钥确认。
// 通道可以经由另一个通道，组成从 OpsNap 出发最多 5 跳的链路；只有测试成功才保存。
// 秘密用主密钥加密后保存，任何响应都不返回（docs/specs/2026-09-25-datasources.md「网络通道」）。
package channel_svc

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/cago-frame/cago/pkg/logger"
	"go.uber.org/zap"

	api "github.com/opskat/opsnap/internal/api/channel"
	"github.com/opskat/opsnap/internal/model/entity/channel_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/netchain"
	"github.com/opskat/opsnap/internal/repository/channel_repo"
	"github.com/opskat/opsnap/internal/service/secret_svc"
)

const (
	maxNameLength     = 64
	defaultSSHPort    = 22
	defaultSOCKS5Port = 1080
)

type ChannelSvc interface {
	List(ctx context.Context, req *api.ListRequest) (*api.ListResponse, error)
	Probe(ctx context.Context, req *api.ProbeRequest) (*api.ProbeResponse, error)
	Create(ctx context.Context, req *api.CreateRequest) (*api.CreateResponse, error)
	Update(ctx context.Context, req *api.UpdateRequest) (*api.UpdateResponse, error)
	Test(ctx context.Context, req *api.TestRequest) (*api.TestResponse, error)
	// ConfirmHostKey 信任 SSH 通道现在出示的主机密钥：出示的与 req.Fingerprint 一致时保存它并重新测试
	ConfirmHostKey(ctx context.Context, req *api.ConfirmHostKeyRequest) (*api.ConfirmHostKeyResponse, error)
	Delete(ctx context.Context, req *api.DeleteRequest) (*api.DeleteResponse, error)

	// Hops 把通道解析为从 OpsNap 出发、到该通道（含）为止的有序链路，用于 netchain.NewChain。
	// 凭据已解密，HostKey 为保存的指纹；结果含秘密，不能写入日志或响应
	Hops(ctx context.Context, id int64) ([]netchain.Hop, error)
	// References 直接经由该通道的数据源与通道
	References(ctx context.Context, id int64) (*api.UsedBy, error)
	// RecordHostKeyChanged err 为链路中某个已保存通道的主机密钥与保存的不一致时，
	// 把该通道的状态标为“主机密钥已变化”并记下出示的指纹，经过它的数据源同样标记（见 SetHostKeyChangedHook）；其他错误忽略
	RecordHostKeyChanged(ctx context.Context, err error) error
}

// Referrer 返回每个通道被哪些数据源直接经由（通道 ID → 数据源）
type Referrer func(ctx context.Context) (map[int64][]*api.Ref, error)

// HostKeyHook 通道主机密钥状态变化时通知数据源模块：
// 信任新密钥并保存后，重新测试经过该通道的所有数据源；发现密钥变化后，把经过该通道的数据源同样标为“主机密钥已变化”
type HostKeyHook func(ctx context.Context, channelID int64) error

type channelSvc struct {
	now func() time.Time

	mu        sync.RWMutex
	referrer  Referrer
	confirmed HostKeyHook
	changed   HostKeyHook
}

var defaultChannel = &channelSvc{now: time.Now}

func Channel() ChannelSvc {
	return defaultChannel
}

// SetDataSourceReferrer 由数据源模块注册，用于列表中的引用计数与删除保护；nil 表示没有数据源引用
func SetDataSourceReferrer(fn Referrer) {
	defaultChannel.mu.Lock()
	defer defaultChannel.mu.Unlock()
	defaultChannel.referrer = fn
}

// SetHostKeyConfirmedHook 由数据源模块注册：通道重新确认主机密钥后，重新测试经过它的数据源；nil 表示不需要
func SetHostKeyConfirmedHook(fn HostKeyHook) {
	defaultChannel.mu.Lock()
	defer defaultChannel.mu.Unlock()
	defaultChannel.confirmed = fn
}

// SetHostKeyChangedHook 由数据源模块注册：通道被标为“主机密钥已变化”后，经过它的数据源同样标记；nil 表示不需要
func SetHostKeyChangedHook(fn HostKeyHook) {
	defaultChannel.mu.Lock()
	defer defaultChannel.mu.Unlock()
	defaultChannel.changed = fn
}

// notify 调用数据源模块注册的钩子
func (s *channelSvc) notify(ctx context.Context, pick func(*channelSvc) HostKeyHook, channelID int64) error {
	s.mu.RLock()
	fn := pick(s)
	s.mu.RUnlock()
	if fn == nil {
		return nil
	}
	return fn(ctx, channelID)
}

func confirmedHook(s *channelSvc) HostKeyHook { return s.confirmed }

func changedHook(s *channelSvc) HostKeyHook { return s.changed }

// passwordOf 取保存的密码密文
func passwordOf(c *channel_entity.Channel) string { return c.Password }

// draft 校验后的通道设置；秘密为明文，只在本次请求中使用
type draft struct {
	ch         channel_entity.Channel
	password   string
	privateKey string
	passphrase string
}

func (d *draft) hop() netchain.Hop {
	return netchain.Hop{
		ID: d.ch.ID, Name: d.ch.Name, Kind: netchain.Kind(d.ch.Kind), Host: d.ch.Host, Port: d.ch.Port,
		User: d.ch.Username, Password: d.password, PrivateKey: []byte(d.privateKey), Passphrase: []byte(d.passphrase),
		HostKey: d.ch.HostKey,
	}
}

func (s *channelSvc) decrypt(ctx context.Context, ct string) (string, error) {
	if ct == "" {
		return "", nil
	}
	return secret_svc.Secret().Decrypt(ctx, ct)
}

func (s *channelSvc) encrypt(ctx context.Context, pt string) (string, error) {
	if pt == "" {
		return "", nil
	}
	return secret_svc.Secret().Encrypt(ctx, pt)
}

// keyError 把私钥解析错误转为对应字段的错误码
func keyError(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, netchain.ErrPassphraseMissing):
		return i18n.NewError(ctx, code.ChannelPassphraseMissing)
	case errors.Is(err, netchain.ErrPassphraseWrong):
		return i18n.NewError(ctx, code.ChannelPassphraseWrong)
	}
	return i18n.NewError(ctx, code.ChannelKeyInvalid)
}

// validate 校验字段与名称唯一性。existing 为正在编辑的通道（新建时为 nil）：
// 唯一性检查排除它自己；同一认证方式下秘密留空时沿用它保存的值；主机与端口未变时沿用它确认过的主机密钥
func (s *channelSvc) validate(ctx context.Context, f api.Form, existing *channel_entity.Channel) (*draft, error) {
	d := &draft{}
	c := &d.ch
	c.Name = strings.TrimSpace(f.Name)
	if c.Name == "" || utf8.RuneCountInString(c.Name) > maxNameLength {
		return nil, i18n.NewError(ctx, code.ChannelNameInvalid)
	}
	c.Kind = f.Kind
	if c.Host = strings.TrimSpace(f.Host); c.Host == "" {
		return nil, i18n.NewError(ctx, code.ChannelHostRequired)
	}
	c.Port = f.Port
	if c.Port == 0 {
		c.Port = defaultSOCKS5Port
		if c.Kind == channel_entity.KindSSH {
			c.Port = defaultSSHPort
		}
	}
	if c.Port < 1 || c.Port > 65535 {
		return nil, i18n.NewError(ctx, code.ChannelPortInvalid)
	}
	c.Username = strings.TrimSpace(f.Username)
	c.ViaID = f.ViaID

	// saved 返回 existing 在同类型、同认证方式下保存的秘密（解密后）
	saved := func(method string, field func(*channel_entity.Channel) string) (string, error) {
		if existing == nil || existing.Kind != c.Kind || existing.AuthMethod != method {
			return "", nil
		}
		return s.decrypt(ctx, field(existing))
	}
	var err error
	switch c.Kind {
	case channel_entity.KindSSH:
		if c.Username == "" {
			return nil, i18n.NewError(ctx, code.ChannelUserRequired)
		}
		c.AuthMethod = f.AuthMethod
		switch c.AuthMethod {
		case channel_entity.AuthPassword:
			if d.password = f.Password; d.password == "" {
				if d.password, err = saved(channel_entity.AuthPassword, passwordOf); err != nil {
					return nil, err
				}
			}
			if d.password == "" {
				return nil, i18n.NewError(ctx, code.ChannelPasswordRequired)
			}
		case channel_entity.AuthKey:
			if err := s.validateKey(ctx, d, f, saved); err != nil {
				return nil, err
			}
		default:
			return nil, i18n.NewError(ctx, code.ChannelAuthMethodInvalid)
		}
		c.HostKey = strings.TrimSpace(f.HostKey)
		if c.HostKey == "" && existing != nil && existing.Kind == channel_entity.KindSSH &&
			existing.Host == c.Host && existing.Port == c.Port {
			c.HostKey = existing.HostKey
		}
	case channel_entity.KindSOCKS5:
		c.AuthMethod = channel_entity.AuthNone
		d.password = f.Password
		if c.Username != "" && d.password == "" {
			if d.password, err = saved(channel_entity.AuthPassword, passwordOf); err != nil {
				return nil, err
			}
		}
		if (c.Username == "") != (d.password == "") {
			return nil, i18n.NewError(ctx, code.ChannelSOCKS5CredentialPair)
		}
		if c.Username != "" {
			c.AuthMethod = channel_entity.AuthPassword
		}
	}

	var selfID int64
	if existing != nil {
		selfID = existing.ID
	}
	c.ID = selfID
	same, err := channel_repo.Channel().FindByName(ctx, c.Name)
	if err != nil {
		return nil, err
	}
	if same != nil && same.ID != selfID {
		return nil, i18n.NewError(ctx, code.ChannelNameDuplicate)
	}
	return d, nil
}

// validateKey 取得私钥与口令（留空时沿用保存的）并在联网前解析；私钥未加密时不保留口令
func (s *channelSvc) validateKey(ctx context.Context, d *draft, f api.Form,
	saved func(method string, field func(*channel_entity.Channel) string) (string, error)) error {
	var err error
	if d.privateKey = f.PrivateKey; d.privateKey == "" {
		if d.privateKey, err = saved(channel_entity.AuthKey, func(c *channel_entity.Channel) string { return c.PrivateKey }); err != nil {
			return err
		}
	}
	if strings.TrimSpace(d.privateKey) == "" {
		return i18n.NewError(ctx, code.ChannelPrivateKeyRequired)
	}
	if d.passphrase = f.Passphrase; d.passphrase == "" {
		if d.passphrase, err = saved(channel_entity.AuthKey, func(c *channel_entity.Channel) string { return c.Passphrase }); err != nil {
			return err
		}
	}
	if _, err := netchain.ParsePrivateKey([]byte(d.privateKey), nil); err == nil {
		d.passphrase = ""
		return nil
	}
	if _, err := netchain.ParsePrivateKey([]byte(d.privateKey), []byte(d.passphrase)); err != nil {
		return keyError(ctx, err)
	}
	return nil
}

// all 读取全部通道，返回按创建顺序的列表与按 ID 的索引
func (s *channelSvc) all(ctx context.Context) ([]*channel_entity.Channel, map[int64]*channel_entity.Channel, error) {
	rows, err := channel_repo.Channel().List(ctx)
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[int64]*channel_entity.Channel, len(rows))
	for _, c := range rows {
		byID[c.ID] = c
	}
	return rows, byID, nil
}

// walk 解析经由 viaID 的链路（从 OpsNap 出发）；本通道（selfID，新建时为 0）出现在其中即为环路
func walk(ctx context.Context, byID map[int64]*channel_entity.Channel, viaID, selfID int64) ([]*channel_entity.Channel, error) {
	var via []*channel_entity.Channel
	for id := viaID; id != 0; {
		if id == selfID || len(via) > len(byID) {
			return nil, i18n.NewError(ctx, code.ChannelViaCycle)
		}
		c := byID[id]
		if c == nil {
			return nil, i18n.NewError(ctx, code.ChannelViaNotFound)
		}
		via = append([]*channel_entity.Channel{c}, via...)
		id = c.ViaID
	}
	return via, nil
}

// viaChain 解析经由 viaID 的链路并检查环路与长度：本通道与所有经过它的通道都不能超过 MaxHops 跳
func (s *channelSvc) viaChain(ctx context.Context, byID map[int64]*channel_entity.Channel, viaID, selfID int64) ([]*channel_entity.Channel, error) {
	via, err := walk(ctx, byID, viaID, selfID)
	if err != nil {
		return nil, err
	}
	depth := len(via) + 1
	longest := depth
	if selfID != 0 {
		for _, c := range byID {
			if n := distance(byID, c, selfID); n > 0 && depth+n > longest {
				longest = depth + n
			}
		}
	}
	if longest > netchain.MaxHops {
		return nil, i18n.NewError(ctx, code.ChannelChainTooLong, longest)
	}
	return via, nil
}

// distance c 经过 ancestorID 时返回 c 在它之后第几跳，否则返回 0
func distance(byID map[int64]*channel_entity.Channel, c *channel_entity.Channel, ancestorID int64) int {
	for n := 1; c != nil && c.ViaID != 0 && n <= len(byID); n++ {
		if c.ViaID == ancestorID {
			return n
		}
		c = byID[c.ViaID]
	}
	return 0
}

// chainOf 已保存通道 c 的完整链路（含 c）
func (s *channelSvc) chainOf(ctx context.Context, byID map[int64]*channel_entity.Channel, c *channel_entity.Channel) ([]*channel_entity.Channel, error) {
	via, err := walk(ctx, byID, c.ViaID, c.ID)
	if err != nil {
		return nil, err
	}
	return append(via, c), nil
}

// hopOf 用保存的设置（解密秘密）组成一跳
func (s *channelSvc) hopOf(ctx context.Context, c *channel_entity.Channel) (netchain.Hop, error) {
	h := netchain.Hop{
		ID: c.ID, Name: c.Name, Kind: netchain.Kind(c.Kind), Host: c.Host, Port: c.Port,
		User: c.Username, HostKey: c.HostKey,
	}
	password, err := s.decrypt(ctx, c.Password)
	if err != nil {
		return h, err
	}
	key, err := s.decrypt(ctx, c.PrivateKey)
	if err != nil {
		return h, err
	}
	passphrase, err := s.decrypt(ctx, c.Passphrase)
	if err != nil {
		return h, err
	}
	h.Password, h.PrivateKey, h.Passphrase = password, []byte(key), []byte(passphrase)
	return h, nil
}

func (s *channelSvc) hops(ctx context.Context, chain []*channel_entity.Channel) ([]netchain.Hop, error) {
	hops := make([]netchain.Hop, 0, len(chain)+1)
	for _, c := range chain {
		h, err := s.hopOf(ctx, c)
		if err != nil {
			return nil, err
		}
		hops = append(hops, h)
	}
	return hops, nil
}

// connect 沿链路依次建立每一跳后断开；整体超时为 netchain.DefaultTimeout
func connect(ctx context.Context, hops []netchain.Hop) error {
	chain, err := netchain.NewChain(hops)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, netchain.DefaultTimeout)
	defer cancel()
	tun, err := chain.Connect(ctx)
	if err != nil {
		return err
	}
	return tun.Close()
}

// HostKeyPrompt 把一跳（通道或服务器文件的目标主机）的主机密钥错误转为待用户确认的信息
func HostKeyPrompt(he *netchain.HopError, hk *netchain.HostKeyError) *api.HostKeyPrompt {
	return &api.HostKeyPrompt{
		Hop: he.Index, Name: he.Name, Address: he.Addr, KeyType: hk.KeyType,
		Fingerprint: hk.Fingerprint, Changed: hk.Changed, Saved: hk.Saved,
	}
}

// tryDraft 经由 via 测试尚未保存的设置。本通道的主机密钥未确认或与信任的不一致时返回待确认信息（不是错误）；
// 其他失败返回指出第几跳的错误
func (s *channelSvc) tryDraft(ctx context.Context, d *draft, via []*channel_entity.Channel) (*api.HostKeyPrompt, error) {
	hops, err := s.hops(ctx, via)
	if err != nil {
		return nil, err
	}
	hops = append(hops, d.hop())
	err = connect(ctx, hops)
	if err == nil {
		return nil, nil
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, ctx.Err()
	}
	var he *netchain.HopError
	if !errors.As(err, &he) {
		return nil, err
	}
	var hk *netchain.HostKeyError
	if errors.As(he, &hk) {
		if he.Index == len(hops) {
			return HostKeyPrompt(he, hk), nil
		}
		if err := s.RecordHostKeyChanged(ctx, he); err != nil {
			return nil, err
		}
		if hk.Changed {
			return nil, i18n.NewError(ctx, code.ChannelHostKeyChanged, he.Index, he.Name, kindLabel(string(he.Kind)))
		}
	}
	return nil, HopError(ctx, he)
}

// save 保存通道；它在测试期间已被删除时返回“不存在”
func (s *channelSvc) save(ctx context.Context, c *channel_entity.Channel) error {
	err := channel_repo.Channel().Save(ctx, c)
	if errors.Is(err, channel_repo.ErrNotFound) {
		return i18n.NewNotFoundError(ctx, code.ChannelNotFound)
	}
	return err
}

// saveStatus 只保存测试结果（及 extra 列），不覆盖测试期间被编辑的其它设置；它已被删除时返回“不存在”
func (s *channelSvc) saveStatus(ctx context.Context, c *channel_entity.Channel, extra ...string) error {
	cols := append(append([]string{}, channel_entity.StatusColumns...), extra...)
	err := channel_repo.Channel().SaveColumns(ctx, c, cols...)
	if errors.Is(err, channel_repo.ErrNotFound) {
		return i18n.NewNotFoundError(ctx, code.ChannelNotFound)
	}
	return err
}

// apply 写入测试通过的设置：秘密重新加密，当前认证方式用不到的旧凭据被删除
func (s *channelSvc) apply(ctx context.Context, c *channel_entity.Channel, d *draft) error {
	c.Name, c.Kind, c.Host, c.Port = d.ch.Name, d.ch.Kind, d.ch.Host, d.ch.Port
	c.Username, c.AuthMethod, c.ViaID, c.HostKey = d.ch.Username, d.ch.AuthMethod, d.ch.ViaID, d.ch.HostKey
	var err error
	if c.Password, err = s.encrypt(ctx, d.password); err != nil {
		return err
	}
	if c.PrivateKey, err = s.encrypt(ctx, d.privateKey); err != nil {
		return err
	}
	if c.Passphrase, err = s.encrypt(ctx, d.passphrase); err != nil {
		return err
	}
	now := s.now().Unix()
	c.SetOK()
	c.Checktime, c.Updatetime = now, now
	return nil
}

func (s *channelSvc) find(ctx context.Context, id int64) (*channel_entity.Channel, error) {
	c, err := channel_repo.Channel().Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return nil, i18n.NewNotFoundError(ctx, code.ChannelNotFound)
	}
	return c, nil
}

// prepare 校验表单并解析经由的链路
func (s *channelSvc) prepare(ctx context.Context, f api.Form, existing *channel_entity.Channel) (*draft, []*channel_entity.Channel, error) {
	d, err := s.validate(ctx, f, existing)
	if err != nil {
		return nil, nil, err
	}
	_, byID, err := s.all(ctx)
	if err != nil {
		return nil, nil, err
	}
	via, err := s.viaChain(ctx, byID, d.ch.ViaID, d.ch.ID)
	if err != nil {
		return nil, nil, err
	}
	return d, via, nil
}

func (s *channelSvc) Probe(ctx context.Context, req *api.ProbeRequest) (*api.ProbeResponse, error) {
	var existing *channel_entity.Channel
	if req.ID != 0 {
		var err error
		if existing, err = s.find(ctx, req.ID); err != nil {
			return nil, err
		}
	}
	d, via, err := s.prepare(ctx, req.Channel, existing)
	if err != nil {
		return nil, err
	}
	prompt, err := s.tryDraft(ctx, d, via)
	if err != nil {
		return nil, err
	}
	return &api.ProbeResponse{HostKey: prompt, Chain: chainHops(append(via, &d.ch))}, nil
}

func (s *channelSvc) Create(ctx context.Context, req *api.CreateRequest) (*api.CreateResponse, error) {
	d, via, err := s.prepare(ctx, req.Channel, nil)
	if err != nil {
		return nil, err
	}
	prompt, err := s.tryDraft(ctx, d, via)
	if err != nil {
		return nil, err
	}
	if prompt != nil {
		return &api.CreateResponse{HostKey: prompt}, nil
	}
	c := &channel_entity.Channel{Createtime: s.now().Unix()}
	if err := s.apply(ctx, c, d); err != nil {
		return nil, err
	}
	if err := channel_repo.Channel().Create(ctx, c); err != nil {
		return nil, err
	}
	item, err := s.item(ctx, c)
	if err != nil {
		return nil, err
	}
	return &api.CreateResponse{Item: item}, nil
}

func (s *channelSvc) Update(ctx context.Context, req *api.UpdateRequest) (*api.UpdateResponse, error) {
	c, err := s.find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	d, via, err := s.prepare(ctx, req.Channel, c)
	if err != nil {
		return nil, err
	}
	prompt, err := s.tryDraft(ctx, d, via)
	if err != nil {
		return nil, err
	}
	if prompt != nil {
		return &api.UpdateResponse{HostKey: prompt}, nil
	}
	oldHostKey := c.HostKey
	if err := s.apply(ctx, c, d); err != nil {
		return nil, err
	}
	if err := s.save(ctx, c); err != nil {
		return nil, err
	}
	if oldHostKey != "" && c.HostKey != oldHostKey {
		// 在编辑表单中信任了新的主机密钥：与列表中的“重新确认”一样，重新测试经过这台主机的数据源
		// （docs/specs/2026-09-25-datasources.md「主机密钥」）
		if err := s.notify(ctx, confirmedHook, c.ID); err != nil {
			return nil, err
		}
	}
	item, err := s.item(ctx, c)
	if err != nil {
		return nil, err
	}
	return &api.UpdateResponse{Item: item}, nil
}

// testSaved 沿保存的链路测试 c，把结果写入 c 的状态与测试时间（不落库）。hostKey 非空时代替 c 保存的主机密钥。
// 返回：c 自身的主机密钥不一致时的待确认信息；c 自身的主机密钥是否通过了校验
func (s *channelSvc) testSaved(ctx context.Context, c *channel_entity.Channel, hostKey string) (*api.HostKeyPrompt, bool, error) {
	_, byID, err := s.all(ctx)
	if err != nil {
		return nil, false, err
	}
	chain, err := s.chainOf(ctx, byID, c)
	if err != nil {
		return nil, false, err
	}
	hops, err := s.hops(ctx, chain)
	if err != nil {
		return nil, false, err
	}
	if hostKey != "" {
		hops[len(hops)-1].HostKey = hostKey
	}
	err = connect(ctx, hops)
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, false, ctx.Err()
	}
	c.Checktime = s.now().Unix()
	if err == nil {
		c.SetOK()
		return nil, true, nil
	}
	var he *netchain.HopError
	if !errors.As(err, &he) {
		return nil, false, err
	}
	status := channel_entity.StatusUnreachable
	if he.Reason == netchain.ReasonHostKeyChanged {
		status = channel_entity.StatusHostKeyChanged
	}
	reason, hs := hopStatus(he)
	c.SetFailure(status, reason, hs)
	logger.Ctx(ctx).Info("通道测试未通过", zap.Int64("channel_id", c.ID), zap.Int("hop", he.Index), zap.String("reason", string(he.Reason)))

	self := he.Index == len(hops)
	var prompt *api.HostKeyPrompt
	var hk *netchain.HostKeyError
	if errors.As(he, &hk) {
		if self {
			c.PresentedHostKey = hk.Fingerprint
			prompt = HostKeyPrompt(he, hk)
		} else if err := s.RecordHostKeyChanged(ctx, he); err != nil {
			return nil, false, err
		}
	}
	// 主机密钥在认证之前校验：本通道认证失败说明密钥已通过校验
	return prompt, self && he.Reason == netchain.ReasonAuthFailed, nil
}

func (s *channelSvc) Test(ctx context.Context, req *api.TestRequest) (*api.TestResponse, error) {
	c, err := s.find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	prompt, _, err := s.testSaved(ctx, c, "")
	if err != nil {
		return nil, err
	}
	if err := s.saveStatus(ctx, c); err != nil {
		return nil, err
	}
	if prompt != nil && prompt.Changed {
		if err := s.notify(ctx, changedHook, c.ID); err != nil {
			return nil, err
		}
	}
	item, err := s.item(ctx, c)
	if err != nil {
		return nil, err
	}
	return &api.TestResponse{Item: item, HostKey: prompt}, nil
}

func (s *channelSvc) ConfirmHostKey(ctx context.Context, req *api.ConfirmHostKeyRequest) (*api.ConfirmHostKeyResponse, error) {
	c, err := s.find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if c.Kind != channel_entity.KindSSH {
		return nil, i18n.NewError(ctx, code.ChannelNotSSH)
	}
	fp := strings.TrimSpace(req.Fingerprint)
	prompt, keyOK, err := s.testSaved(ctx, c, fp)
	if err != nil {
		return nil, err
	}
	if prompt != nil {
		// 出示的密钥与用户信任的不一致：什么也不保存，弹窗显示保存的与现在出示的
		prompt.Saved, prompt.Changed = c.HostKey, c.HostKey != ""
		return &api.ConfirmHostKeyResponse{HostKey: prompt}, nil
	}
	var extra []string
	if keyOK {
		c.HostKey = fp
		c.Updatetime = s.now().Unix()
		extra = []string{"host_key", "updatetime"}
	}
	if err := s.saveStatus(ctx, c, extra...); err != nil {
		return nil, err
	}
	if keyOK {
		// 信任新密钥后，经过这台主机的数据源也要重新测试（docs/specs/2026-09-25-datasources.md「主机密钥」）
		if err := s.notify(ctx, confirmedHook, c.ID); err != nil {
			return nil, err
		}
	}
	item, err := s.item(ctx, c)
	if err != nil {
		return nil, err
	}
	return &api.ConfirmHostKeyResponse{Item: item}, nil
}

func (s *channelSvc) RecordHostKeyChanged(ctx context.Context, err error) error {
	var he *netchain.HopError
	var hk *netchain.HostKeyError
	if !errors.As(err, &he) || !errors.As(err, &hk) || !hk.Changed || he.ID == 0 {
		return nil
	}
	c, err := channel_repo.Channel().Find(ctx, he.ID)
	if err != nil || c == nil || c.HostKey != hk.Saved {
		return err
	}
	reason, hs := hopStatus(he)
	c.SetFailure(channel_entity.StatusHostKeyChanged, reason, hs)
	c.PresentedHostKey = hk.Fingerprint
	c.Checktime = s.now().Unix()
	if err := channel_repo.Channel().SaveColumns(ctx, c, channel_entity.StatusColumns...); err != nil {
		if errors.Is(err, channel_repo.ErrNotFound) {
			return nil
		}
		return err
	}
	return s.notify(ctx, changedHook, c.ID)
}

func (s *channelSvc) Delete(ctx context.Context, req *api.DeleteRequest) (*api.DeleteResponse, error) {
	c, err := s.find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	used, err := s.References(ctx, c.ID)
	if err != nil {
		return nil, err
	}
	if refs := append(append([]*api.Ref{}, used.DataSources...), used.Channels...); len(refs) > 0 {
		names := make([]string, 0, len(refs))
		for _, r := range refs {
			names = append(names, r.Name)
		}
		return nil, i18n.NewError(ctx, code.ChannelInUse, strings.Join(names, ", "))
	}
	if err := channel_repo.Channel().Delete(ctx, c.ID); err != nil {
		return nil, err
	}
	return &api.DeleteResponse{}, nil
}

func (s *channelSvc) Hops(ctx context.Context, id int64) ([]netchain.Hop, error) {
	c, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	_, byID, err := s.all(ctx)
	if err != nil {
		return nil, err
	}
	chain, err := s.chainOf(ctx, byID, c)
	if err != nil {
		return nil, err
	}
	return s.hops(ctx, chain)
}

func (s *channelSvc) References(ctx context.Context, id int64) (*api.UsedBy, error) {
	c, err := s.find(ctx, id)
	if err != nil {
		return nil, err
	}
	rows, _, err := s.all(ctx)
	if err != nil {
		return nil, err
	}
	used, err := s.usedBy(ctx, rows)
	if err != nil {
		return nil, err
	}
	return used[c.ID], nil
}

// usedBy 每个通道被哪些数据源与通道直接经由
func (s *channelSvc) usedBy(ctx context.Context, rows []*channel_entity.Channel) (map[int64]*api.UsedBy, error) {
	used := make(map[int64]*api.UsedBy, len(rows))
	for _, c := range rows {
		used[c.ID] = &api.UsedBy{DataSources: []*api.Ref{}, Channels: []*api.Ref{}}
	}
	for _, c := range rows {
		if u := used[c.ViaID]; u != nil {
			u.Channels = append(u.Channels, &api.Ref{ID: c.ID, Name: c.Name})
		}
	}
	s.mu.RLock()
	fn := s.referrer
	s.mu.RUnlock()
	if fn == nil {
		return used, nil
	}
	refs, err := fn(ctx)
	if err != nil {
		return nil, err
	}
	for id, rs := range refs {
		if u := used[id]; u != nil {
			u.DataSources = append(u.DataSources, rs...)
		}
	}
	return used, nil
}

func (s *channelSvc) List(ctx context.Context, _ *api.ListRequest) (*api.ListResponse, error) {
	rows, byID, err := s.all(ctx)
	if err != nil {
		return nil, err
	}
	used, err := s.usedBy(ctx, rows)
	if err != nil {
		return nil, err
	}
	items := make([]*api.Item, 0, len(rows))
	for _, c := range rows {
		items = append(items, s.toItem(ctx, c, byID, used[c.ID]))
	}
	return &api.ListResponse{Items: items}, nil
}

// item 单个通道的列表项（含链路与引用）
func (s *channelSvc) item(ctx context.Context, c *channel_entity.Channel) (*api.Item, error) {
	rows, byID, err := s.all(ctx)
	if err != nil {
		return nil, err
	}
	byID[c.ID] = c
	used, err := s.usedBy(ctx, rows)
	if err != nil {
		return nil, err
	}
	u := used[c.ID]
	if u == nil {
		u = &api.UsedBy{DataSources: []*api.Ref{}, Channels: []*api.Ref{}}
	}
	return s.toItem(ctx, c, byID, u), nil
}

func (s *channelSvc) toItem(ctx context.Context, c *channel_entity.Channel, byID map[int64]*channel_entity.Channel, used *api.UsedBy) *api.Item {
	chain, err := s.chainOf(ctx, byID, c)
	if err != nil {
		chain = []*channel_entity.Channel{c}
	}
	item := &api.Item{
		ID:               c.ID,
		Name:             c.Name,
		Kind:             c.Kind,
		Host:             c.Host,
		Port:             c.Port,
		Username:         c.Username,
		AuthMethod:       c.AuthMethod,
		HasPassword:      c.Password != "",
		HasPrivateKey:    c.PrivateKey != "",
		HasPassphrase:    c.Passphrase != "",
		ViaID:            c.ViaID,
		Address:          c.Address(),
		Chain:            chainHops(chain),
		HostKey:          c.HostKey,
		PresentedHostKey: c.PresentedHostKey,
		UsedBy:           used,
		Status:           c.Status,
		CheckedAt:        c.Checktime,
		CreatedAt:        c.Createtime,
	}
	if c.StatusCode != 0 {
		item.StatusMessage = hopMessage(ctx, c.StatusCode, c.HopStatus())
	}
	return item
}

func chainHops(chain []*channel_entity.Channel) []*api.Hop {
	hops := make([]*api.Hop, 0, len(chain))
	for _, c := range chain {
		hops = append(hops, &api.Hop{ID: c.ID, Name: c.Name, Kind: c.Kind, Address: c.Address()})
	}
	return hops
}
