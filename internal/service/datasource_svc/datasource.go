// Package datasource_svc 实现数据源（MySQL、PostgreSQL、服务器文件）的增删改与测试连接。
// 数据源可以经由一个网络通道（及其上游组成的链路）连接；规则与网络通道相同：只有测试成功才保存，
// 服务器文件的目标主机密钥首次连接时需要确认。秘密用主密钥加密后保存，任何响应都不返回
// （docs/specs/2026-09-25-datasources.md「数据源」「主机密钥」）。
package datasource_svc

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

	channelapi "github.com/opskat/opsnap/internal/api/channel"
	api "github.com/opskat/opsnap/internal/api/datasource"
	"github.com/opskat/opsnap/internal/model/entity/channel_entity"
	"github.com/opskat/opsnap/internal/model/entity/datasource_entity"
	"github.com/opskat/opsnap/internal/pkg/code"
	"github.com/opskat/opsnap/internal/pkg/dsconn"
	"github.com/opskat/opsnap/internal/pkg/netchain"
	"github.com/opskat/opsnap/internal/repository/channel_repo"
	"github.com/opskat/opsnap/internal/repository/datasource_repo"
	"github.com/opskat/opsnap/internal/service/channel_svc"
	"github.com/opskat/opsnap/internal/service/secret_svc"
)

const (
	maxNameLength = 64
	// retestConcurrency 通道重新确认主机密钥后，同时重新测试的数据源个数
	retestConcurrency = 4
)

// defaultPorts 各类型的默认端口
var defaultPorts = map[string]int{
	datasource_entity.KindMySQL:      3306,
	datasource_entity.KindPostgreSQL: 5432,
	datasource_entity.KindServerFile: 22,
}

type DataSourceSvc interface {
	List(ctx context.Context, req *api.ListRequest) (*api.ListResponse, error)
	Get(ctx context.Context, req *api.GetRequest) (*api.GetResponse, error)
	Probe(ctx context.Context, req *api.ProbeRequest) (*api.ProbeResponse, error)
	Create(ctx context.Context, req *api.CreateRequest) (*api.CreateResponse, error)
	Update(ctx context.Context, req *api.UpdateRequest) (*api.UpdateResponse, error)
	Test(ctx context.Context, req *api.TestRequest) (*api.TestResponse, error)
	// ConfirmHostKey 信任服务器文件目标主机现在出示的密钥：出示的与 req.Fingerprint 一致时保存它并重新测试
	ConfirmHostKey(ctx context.Context, req *api.ConfirmHostKeyRequest) (*api.ConfirmHostKeyResponse, error)
	Delete(ctx context.Context, req *api.DeleteRequest) (*api.DeleteResponse, error)

	// References 每个通道被哪些数据源直接经由（通道 ID → 数据源），供通道的引用计数与删除保护
	References(ctx context.Context) (map[int64][]*channelapi.Ref, error)
	// RetestThroughChannel 重新测试链路经过该通道的所有数据源并保存结果
	RetestThroughChannel(ctx context.Context, channelID int64) error
	// MarkHostKeyChanged 通道的主机密钥已变化：把链路经过它的所有数据源标为“主机密钥已变化”并指明该通道（不联网）
	MarkHostKeyChanged(ctx context.Context, channelID int64) error
}

type dataSourceSvc struct {
	now func() time.Time

	mu        sync.RWMutex
	connector dsconn.Connector
}

var defaultDataSource = &dataSourceSvc{now: time.Now, connector: dsconn.Default}

func DataSource() DataSourceSvc {
	return defaultDataSource
}

// SetConnector 替换连接数据源的实现（测试用 mock）；nil 恢复为 dsconn.Default
func SetConnector(c dsconn.Connector) {
	if c == nil {
		c = dsconn.Default
	}
	defaultDataSource.mu.Lock()
	defer defaultDataSource.mu.Unlock()
	defaultDataSource.connector = c
}

// RegisterChannelHooks 向通道模块注册：引用计数与删除保护计入数据源；通道的主机密钥变化时经过它的数据源同样标记，
// 重新确认后重新测试经过它的数据源
func RegisterChannelHooks() {
	channel_svc.SetDataSourceReferrer(defaultDataSource.References)
	channel_svc.SetHostKeyChangedHook(defaultDataSource.MarkHostKeyChanged)
	channel_svc.SetHostKeyConfirmedHook(defaultDataSource.RetestThroughChannel)
}

func (s *dataSourceSvc) conn() dsconn.Connector {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.connector
}

// draft 校验后的数据源设置；秘密为明文，只在本次请求中使用
type draft struct {
	ds         datasource_entity.DataSource
	password   string
	privateKey string
	passphrase string
	clientKey  string
}

// config 连接参数；含秘密，不能写入日志或响应
func config(ds *datasource_entity.DataSource, password, privateKey, passphrase, clientKey string) dsconn.Config {
	cfg := dsconn.Config{
		ID: ds.ID, Name: ds.Name, Type: dsconn.Type(ds.Kind), Host: ds.Host, Port: ds.Port,
		User: ds.Username, Password: password, Database: ds.Database,
	}
	if ds.Kind == datasource_entity.KindServerFile {
		cfg.PrivateKey, cfg.Passphrase, cfg.HostKey = []byte(privateKey), []byte(passphrase), ds.HostKey
		return cfg
	}
	cfg.TLS = dsconn.TLSConfig{Mode: dsconn.TLSMode(ds.TLSMode)}
	if ds.TLSCA != "" {
		cfg.TLS.CA = []byte(ds.TLSCA)
	}
	if ds.TLSClientCert != "" || clientKey != "" {
		cfg.TLS.ClientCert, cfg.TLS.ClientKey = []byte(ds.TLSClientCert), []byte(clientKey)
	}
	return cfg
}

func (d *draft) config() dsconn.Config {
	return config(&d.ds, d.password, d.privateKey, d.passphrase, d.clientKey)
}

func (d *draft) secrets() []string {
	return []string{d.password, d.passphrase, d.clientKey}
}

func (s *dataSourceSvc) decrypt(ctx context.Context, ct string) (string, error) {
	if ct == "" {
		return "", nil
	}
	return secret_svc.Secret().Decrypt(ctx, ct)
}

func (s *dataSourceSvc) encrypt(ctx context.Context, pt string) (string, error) {
	if pt == "" {
		return "", nil
	}
	return secret_svc.Secret().Encrypt(ctx, pt)
}

// savedConfig 用保存的设置（解密秘密）组成连接参数，并返回其中的秘密供去除
func (s *dataSourceSvc) savedConfig(ctx context.Context, ds *datasource_entity.DataSource) (dsconn.Config, []string, error) {
	var plain [4]string
	for i, ct := range []string{ds.Password, ds.PrivateKey, ds.Passphrase, ds.TLSClientKey} {
		pt, err := s.decrypt(ctx, ct)
		if err != nil {
			return dsconn.Config{}, nil, err
		}
		plain[i] = pt
	}
	return config(ds, plain[0], plain[1], plain[2], plain[3]), []string{plain[0], plain[2], plain[3]}, nil
}

// secretField 保存的某个秘密字段的密文
type secretField func(*datasource_entity.DataSource) string

// validate 校验字段、名称唯一性与经由的通道。existing 为正在编辑的数据源（新建时为 nil）：
// 唯一性检查排除它自己；同类型、同认证方式下秘密留空时沿用它保存的值；主机与端口未变时沿用它确认过的主机密钥
func (s *dataSourceSvc) validate(ctx context.Context, f api.Form, existing *datasource_entity.DataSource,
	channels map[int64]*channel_entity.Channel) (*draft, error) {
	d := &draft{}
	ds := &d.ds
	if existing != nil {
		ds.ID = existing.ID
	}
	ds.Name = strings.TrimSpace(f.Name)
	if ds.Name == "" || utf8.RuneCountInString(ds.Name) > maxNameLength {
		return nil, i18n.NewError(ctx, code.DataSourceNameInvalid)
	}
	ds.Kind = f.Kind
	if ds.Host = strings.TrimSpace(f.Host); ds.Host == "" {
		return nil, i18n.NewError(ctx, code.DataSourceHostRequired)
	}
	if ds.Port = f.Port; ds.Port == 0 {
		ds.Port = defaultPorts[ds.Kind]
	}
	if ds.Port < 1 || ds.Port > 65535 {
		return nil, i18n.NewError(ctx, code.DataSourcePortInvalid)
	}
	if ds.Username = strings.TrimSpace(f.Username); ds.Username == "" {
		return nil, i18n.NewError(ctx, code.DataSourceUserRequired)
	}

	// saved 返回 existing 在同类型、同认证方式下保存的秘密（解密后）
	saved := func(method string, field secretField) (string, error) {
		if existing == nil || existing.Kind != ds.Kind || existing.AuthMethod != method {
			return "", nil
		}
		return s.decrypt(ctx, field(existing))
	}
	var err error
	if ds.Kind == datasource_entity.KindServerFile {
		err = s.validateServerFile(ctx, d, f, existing, saved)
	} else {
		err = s.validateDatabase(ctx, d, f, saved)
	}
	if err != nil {
		return nil, err
	}

	if ds.ChannelID = f.ChannelID; ds.ChannelID != 0 && channels[ds.ChannelID] == nil {
		return nil, i18n.NewError(ctx, code.DataSourceChannelNotFound)
	}
	same, err := datasource_repo.DataSource().FindByName(ctx, ds.Name)
	if err != nil {
		return nil, err
	}
	if same != nil && same.ID != ds.ID {
		return nil, i18n.NewError(ctx, code.DataSourceNameDuplicate)
	}
	if err := d.config().Validate(); err != nil {
		return nil, configError(ctx, err)
	}
	return d, nil
}

// configError 把 dsconn 的配置错误转为接口错误
func configError(ctx context.Context, err error) error {
	var fe *dsconn.FieldError
	if errors.As(err, &fe) {
		return fieldError(ctx, fe)
	}
	return i18n.NewError(ctx, code.DataSourceConfigInvalid)
}

// validateDatabase MySQL / PostgreSQL：密码必填；TLS 模式默认优先加密；客户端证书与私钥必须同时提供
func (s *dataSourceSvc) validateDatabase(ctx context.Context, d *draft, f api.Form, saved func(string, secretField) (string, error)) error {
	ds := &d.ds
	ds.AuthMethod = datasource_entity.AuthPassword
	var err error
	if d.password = f.Password; d.password == "" {
		if d.password, err = saved(datasource_entity.AuthPassword, func(e *datasource_entity.DataSource) string { return e.Password }); err != nil {
			return err
		}
	}
	if d.password == "" {
		return i18n.NewError(ctx, code.DataSourcePasswordRequired)
	}
	if ds.Kind == datasource_entity.KindPostgreSQL {
		if ds.Database = strings.TrimSpace(f.Database); ds.Database == "" {
			ds.Database = dsconn.DefaultPGDatabase
		}
	}
	if ds.TLSMode = f.TLSMode; ds.TLSMode == "" {
		ds.TLSMode = string(dsconn.TLSPrefer)
	}
	ds.TLSCA, ds.TLSClientCert, d.clientKey = blankToEmpty(f.TLSCA), blankToEmpty(f.TLSClientCert), blankToEmpty(f.TLSClientKey)
	// 客户端私钥留空且仍填写了客户端证书：沿用已保存的私钥；去掉证书时私钥一并删除
	if d.clientKey == "" && ds.TLSClientCert != "" {
		if d.clientKey, err = saved(datasource_entity.AuthPassword, func(e *datasource_entity.DataSource) string { return e.TLSClientKey }); err != nil {
			return err
		}
	}
	if (ds.TLSClientCert == "") != (d.clientKey == "") {
		return i18n.NewError(ctx, code.DataSourceTLSPairRequired)
	}
	return nil
}

// blankToEmpty 只有空白的 PEM 字段视为未填写；有内容时原样保存
func blankToEmpty(s string) string {
	if strings.TrimSpace(s) == "" {
		return ""
	}
	return s
}

// validateServerFile 服务器文件：认证方式为密码或私钥；私钥在联网前解析，未加密时不保留口令
func (s *dataSourceSvc) validateServerFile(ctx context.Context, d *draft, f api.Form, existing *datasource_entity.DataSource,
	saved func(string, secretField) (string, error)) error {
	ds := &d.ds
	ds.AuthMethod = f.AuthMethod
	var err error
	switch ds.AuthMethod {
	case datasource_entity.AuthPassword:
		if d.password = f.Password; d.password == "" {
			if d.password, err = saved(datasource_entity.AuthPassword, func(e *datasource_entity.DataSource) string { return e.Password }); err != nil {
				return err
			}
		}
		if d.password == "" {
			return i18n.NewError(ctx, code.DataSourcePasswordRequired)
		}
	case datasource_entity.AuthKey:
		if d.privateKey = f.PrivateKey; d.privateKey == "" {
			if d.privateKey, err = saved(datasource_entity.AuthKey, func(e *datasource_entity.DataSource) string { return e.PrivateKey }); err != nil {
				return err
			}
		}
		if strings.TrimSpace(d.privateKey) == "" {
			return i18n.NewError(ctx, code.DataSourcePrivateKeyRequired)
		}
		if d.passphrase = f.Passphrase; d.passphrase == "" {
			if d.passphrase, err = saved(datasource_entity.AuthKey, func(e *datasource_entity.DataSource) string { return e.Passphrase }); err != nil {
				return err
			}
		}
		if _, err := netchain.ParsePrivateKey([]byte(d.privateKey), nil); err == nil {
			d.passphrase = ""
		} else if _, err := netchain.ParsePrivateKey([]byte(d.privateKey), []byte(d.passphrase)); err != nil {
			return keyError(ctx, err)
		}
	default:
		return i18n.NewError(ctx, code.DataSourceAuthMethodInvalid)
	}
	ds.HostKey = strings.TrimSpace(f.HostKey)
	if ds.HostKey == "" && existing != nil && existing.Kind == datasource_entity.KindServerFile &&
		existing.Host == ds.Host && existing.Port == ds.Port {
		ds.HostKey = existing.HostKey
	}
	return nil
}

// channels 全部通道（按 ID 索引），用于解析展示用的链路
func (s *dataSourceSvc) channels(ctx context.Context) (map[int64]*channel_entity.Channel, error) {
	rows, err := channel_repo.Channel().List(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[int64]*channel_entity.Channel, len(rows))
	for _, c := range rows {
		byID[c.ID] = c
	}
	return byID, nil
}

// via 从 OpsNap 出发经由到 channelID（含）的通道；通道缺失或成环时截断
func via(byID map[int64]*channel_entity.Channel, channelID int64) []*channel_entity.Channel {
	var chain []*channel_entity.Channel
	for id := channelID; id != 0 && len(chain) <= len(byID); {
		c := byID[id]
		if c == nil {
			break
		}
		chain = append([]*channel_entity.Channel{c}, chain...)
		id = c.ViaID
	}
	return chain
}

// chainHops 展示用的完整链路，最后一跳是数据源本身
func chainHops(byID map[int64]*channel_entity.Channel, ds *datasource_entity.DataSource) []*channelapi.Hop {
	chain := via(byID, ds.ChannelID)
	hops := make([]*channelapi.Hop, 0, len(chain)+1)
	for _, c := range chain {
		hops = append(hops, &channelapi.Hop{ID: c.ID, Name: c.Name, Kind: c.Kind, Address: c.Address()})
	}
	return append(hops, &channelapi.Hop{ID: ds.ID, Name: ds.Name, Kind: ds.Kind, Address: ds.Address()})
}

// hops 经由通道的链路（凭据已解密），直连时为空
func (s *dataSourceSvc) hops(ctx context.Context, channelID int64) ([]netchain.Hop, error) {
	if channelID == 0 {
		return nil, nil
	}
	return channel_svc.Channel().Hops(ctx, channelID)
}

// connect 沿链路连接数据源、认证并读取基本信息后断开；整体超时为 netchain.DefaultTimeout
func (s *dataSourceSvc) connect(ctx context.Context, hops []netchain.Hop, cfg dsconn.Config) (dsconn.Info, error) {
	ctx, cancel := context.WithTimeout(ctx, netchain.DefaultTimeout)
	defer cancel()
	chain, err := netchain.NewChain(hops)
	if err != nil {
		return dsconn.Info{}, err
	}
	tun, err := chain.Connect(ctx)
	if err != nil {
		return dsconn.Info{}, err
	}
	defer func() { _ = tun.Close() }()
	return s.conn().Test(ctx, tun, cfg)
}

func hostKeyPrompt(he *netchain.HopError, hk *netchain.HostKeyError) *channelapi.HostKeyPrompt {
	return &channelapi.HostKeyPrompt{
		Hop: he.Index, Name: he.Name, Address: he.Addr, KeyType: hk.KeyType,
		Fingerprint: hk.Fingerprint, Changed: hk.Changed, Saved: hk.Saved,
	}
}

func serverInfo(info dsconn.Info) *api.ServerInfo {
	si := &api.ServerInfo{Version: info.Version, System: info.System}
	if info.TLS != nil {
		si.TLS = &api.TLS{Version: info.TLS.Version, Verified: info.TLS.Verified}
	}
	return si
}

// tryDraft 经由链路测试尚未保存的设置。服务器文件的目标主机密钥未确认或与信任的不一致时返回待确认信息（不是错误）；
// 其他失败返回指出第几跳的错误
func (s *dataSourceSvc) tryDraft(ctx context.Context, d *draft) (*channelapi.HostKeyPrompt, *dsconn.Info, error) {
	hops, err := s.hops(ctx, d.ds.ChannelID)
	if err != nil {
		return nil, nil, err
	}
	info, err := s.connect(ctx, hops, d.config())
	if err == nil {
		return nil, &info, nil
	}
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, nil, ctx.Err()
	}
	var (
		he *netchain.HopError
		hk *netchain.HostKeyError
		de *dsconn.Error
	)
	switch {
	case errors.As(err, &he) && he.Index > len(hops):
		// 服务器文件的目标主机
		if errors.As(he, &hk) {
			return hostKeyPrompt(he, hk), nil, nil
		}
		return nil, nil, channel_svc.HopError(ctx, he)
	case he != nil:
		if errors.As(he, &hk) {
			if err := channel_svc.Channel().RecordHostKeyChanged(ctx, he); err != nil {
				return nil, nil, err
			}
			if hk.Changed {
				return nil, nil, i18n.NewError(ctx, code.ChannelHostKeyChanged, he.Index, he.Name, kindLabel(string(he.Kind)))
			}
		}
		return nil, nil, channel_svc.HopError(ctx, he)
	case errors.As(err, &de):
		reason, hs := targetStatus(de, len(hops)+1, &d.ds, d.secrets()...)
		return nil, nil, i18n.NewError(ctx, code.DataSourceTestFailed, targetArgs(ctx, reason, hs)...)
	case errors.Is(err, dsconn.ErrInvalidConfig):
		return nil, nil, configError(ctx, err)
	}
	var fe *dsconn.FieldError
	if errors.As(err, &fe) {
		return nil, nil, fieldError(ctx, fe)
	}
	return nil, nil, err
}

// setInfo 记录测试成功时读取的服务端信息
func setInfo(ds *datasource_entity.DataSource, info dsconn.Info) {
	ds.Version, ds.System, ds.TLSVersion, ds.TLSVerified = info.Version, info.System, "", false
	if info.TLS != nil {
		ds.TLSVersion, ds.TLSVerified = info.TLS.Version, info.TLS.Verified
	}
}

// testSaved 沿保存的链路测试 ds，把结果写入 ds 的状态、服务端信息与测试时间（不落库）。hostKey 非空时代替保存的目标主机密钥。
// 返回：目标主机的密钥不一致时的待确认信息；目标主机的密钥是否通过了校验（服务器文件）
func (s *dataSourceSvc) testSaved(ctx context.Context, ds *datasource_entity.DataSource, hostKey string) (*channelapi.HostKeyPrompt, bool, error) {
	cfg, secrets, err := s.savedConfig(ctx, ds)
	if err != nil {
		return nil, false, err
	}
	if hostKey != "" {
		cfg.HostKey = hostKey
	}
	hops, err := s.hops(ctx, ds.ChannelID)
	if err != nil {
		return nil, false, err
	}
	info, err := s.connect(ctx, hops, cfg)
	if errors.Is(ctx.Err(), context.Canceled) {
		return nil, false, ctx.Err()
	}
	ds.Checktime = s.now().Unix()
	if err == nil {
		ds.SetOK()
		setInfo(ds, info)
		return nil, true, nil
	}
	ds.PresentedHostKey = ""
	target := len(hops) + 1
	var (
		he     *netchain.HopError
		hk     *netchain.HostKeyError
		de     *dsconn.Error
		prompt *channelapi.HostKeyPrompt
		keyOK  bool
	)
	switch {
	case errors.As(err, &he):
		status := datasource_entity.StatusUnreachable
		if he.Reason == netchain.ReasonHostKeyChanged {
			status = datasource_entity.StatusHostKeyChanged
		}
		ds.SetFailure(status, code.ChannelHopFailed, hopStatus(he, len(hops)))
		if errors.As(he, &hk) {
			if he.Index == target {
				ds.PresentedHostKey = hk.Fingerprint
				prompt = hostKeyPrompt(he, hk)
			} else if err := channel_svc.Channel().RecordHostKeyChanged(ctx, he); err != nil {
				return nil, false, err
			}
		}
		// 主机密钥在认证之前校验：目标主机认证失败说明密钥已通过校验
		keyOK = he.Index == target && he.Reason == netchain.ReasonAuthFailed
	case errors.As(err, &de):
		reason, hs := targetStatus(de, target, ds, secrets...)
		ds.SetFailure(datasource_entity.StatusUnreachable, reason, hs)
		// 服务器文件登录之后（执行 uname -sm）才会出现数据源本身的错误
		keyOK = ds.Kind == datasource_entity.KindServerFile
	default:
		ds.SetFailure(datasource_entity.StatusUnreachable, code.DataSourceReasonFailed, datasource_entity.HopStatus{
			Hop: target, Name: ds.Name, Kind: ds.Kind, Reason: string(dsconn.ReasonFailed), Detail: scrub(err.Error(), secrets...),
		})
	}
	hs := ds.HopStatus()
	logger.Ctx(ctx).Info("数据源测试未通过", zap.Int64("datasource_id", ds.ID), zap.Int("hop", hs.Hop), zap.String("reason", hs.Reason))
	return prompt, keyOK, nil
}

// save 保存数据源；它在测试期间已被删除时返回“不存在”
func (s *dataSourceSvc) save(ctx context.Context, ds *datasource_entity.DataSource) error {
	err := datasource_repo.DataSource().Save(ctx, ds)
	if errors.Is(err, datasource_repo.ErrNotFound) {
		return i18n.NewNotFoundError(ctx, code.DataSourceNotFound)
	}
	return err
}

// apply 写入测试通过的设置与读取的服务端信息：秘密重新加密，当前类型与认证方式用不到的旧凭据被删除
func (s *dataSourceSvc) apply(ctx context.Context, ds *datasource_entity.DataSource, d *draft, info dsconn.Info) error {
	n := d.ds
	ds.Name, ds.Kind, ds.Host, ds.Port, ds.Username, ds.AuthMethod = n.Name, n.Kind, n.Host, n.Port, n.Username, n.AuthMethod
	ds.Database, ds.TLSMode, ds.TLSCA, ds.TLSClientCert = n.Database, n.TLSMode, n.TLSCA, n.TLSClientCert
	ds.HostKey, ds.ChannelID = n.HostKey, n.ChannelID
	for _, f := range []struct {
		dst *string
		pt  string
	}{{&ds.Password, d.password}, {&ds.PrivateKey, d.privateKey}, {&ds.Passphrase, d.passphrase}, {&ds.TLSClientKey, d.clientKey}} {
		ct, err := s.encrypt(ctx, f.pt)
		if err != nil {
			return err
		}
		*f.dst = ct
	}
	now := s.now().Unix()
	ds.SetOK()
	setInfo(ds, info)
	ds.Checktime, ds.Updatetime = now, now
	return nil
}

func (s *dataSourceSvc) find(ctx context.Context, id int64) (*datasource_entity.DataSource, error) {
	ds, err := datasource_repo.DataSource().Find(ctx, id)
	if err != nil {
		return nil, err
	}
	if ds == nil {
		return nil, i18n.NewNotFoundError(ctx, code.DataSourceNotFound)
	}
	return ds, nil
}

// prepare 校验表单
func (s *dataSourceSvc) prepare(ctx context.Context, f api.Form, existing *datasource_entity.DataSource) (*draft, map[int64]*channel_entity.Channel, error) {
	byID, err := s.channels(ctx)
	if err != nil {
		return nil, nil, err
	}
	d, err := s.validate(ctx, f, existing, byID)
	if err != nil {
		return nil, nil, err
	}
	return d, byID, nil
}

func (s *dataSourceSvc) Probe(ctx context.Context, req *api.ProbeRequest) (*api.ProbeResponse, error) {
	var existing *datasource_entity.DataSource
	if req.ID != 0 {
		var err error
		if existing, err = s.find(ctx, req.ID); err != nil {
			return nil, err
		}
	}
	d, byID, err := s.prepare(ctx, req.DataSource, existing)
	if err != nil {
		return nil, err
	}
	prompt, info, err := s.tryDraft(ctx, d)
	if err != nil {
		return nil, err
	}
	resp := &api.ProbeResponse{HostKey: prompt, Chain: chainHops(byID, &d.ds)}
	if info != nil {
		resp.Server = serverInfo(*info)
	}
	return resp, nil
}

func (s *dataSourceSvc) Create(ctx context.Context, req *api.CreateRequest) (*api.CreateResponse, error) {
	d, _, err := s.prepare(ctx, req.DataSource, nil)
	if err != nil {
		return nil, err
	}
	prompt, info, err := s.tryDraft(ctx, d)
	if err != nil {
		return nil, err
	}
	if prompt != nil {
		return &api.CreateResponse{HostKey: prompt}, nil
	}
	ds := &datasource_entity.DataSource{Createtime: s.now().Unix()}
	if err := s.apply(ctx, ds, d, *info); err != nil {
		return nil, err
	}
	if err := datasource_repo.DataSource().Create(ctx, ds); err != nil {
		return nil, err
	}
	item, err := s.item(ctx, ds)
	if err != nil {
		return nil, err
	}
	return &api.CreateResponse{Item: item}, nil
}

func (s *dataSourceSvc) Update(ctx context.Context, req *api.UpdateRequest) (*api.UpdateResponse, error) {
	ds, err := s.find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	d, _, err := s.prepare(ctx, req.DataSource, ds)
	if err != nil {
		return nil, err
	}
	prompt, info, err := s.tryDraft(ctx, d)
	if err != nil {
		return nil, err
	}
	if prompt != nil {
		return &api.UpdateResponse{HostKey: prompt}, nil
	}
	if err := s.apply(ctx, ds, d, *info); err != nil {
		return nil, err
	}
	if err := s.save(ctx, ds); err != nil {
		return nil, err
	}
	item, err := s.item(ctx, ds)
	if err != nil {
		return nil, err
	}
	return &api.UpdateResponse{Item: item}, nil
}

func (s *dataSourceSvc) Test(ctx context.Context, req *api.TestRequest) (*api.TestResponse, error) {
	ds, err := s.find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	prompt, _, err := s.testSaved(ctx, ds, "")
	if err != nil {
		return nil, err
	}
	if err := s.save(ctx, ds); err != nil {
		return nil, err
	}
	item, err := s.item(ctx, ds)
	if err != nil {
		return nil, err
	}
	return &api.TestResponse{Item: item, HostKey: prompt}, nil
}

func (s *dataSourceSvc) ConfirmHostKey(ctx context.Context, req *api.ConfirmHostKeyRequest) (*api.ConfirmHostKeyResponse, error) {
	ds, err := s.find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if ds.Kind != datasource_entity.KindServerFile {
		return nil, i18n.NewError(ctx, code.DataSourceNotServerFile)
	}
	fp := strings.TrimSpace(req.Fingerprint)
	prompt, keyOK, err := s.testSaved(ctx, ds, fp)
	if err != nil {
		return nil, err
	}
	if prompt != nil {
		// 出示的密钥与用户信任的不一致：什么也不保存，弹窗显示保存的与现在出示的
		prompt.Saved, prompt.Changed = ds.HostKey, ds.HostKey != ""
		return &api.ConfirmHostKeyResponse{HostKey: prompt}, nil
	}
	if keyOK {
		ds.HostKey = fp
		ds.Updatetime = s.now().Unix()
	}
	if err := s.save(ctx, ds); err != nil {
		return nil, err
	}
	item, err := s.item(ctx, ds)
	if err != nil {
		return nil, err
	}
	return &api.ConfirmHostKeyResponse{Item: item}, nil
}

// routedThrough 链路经过 channelID 的数据源，以及该通道在各自链路中是第几跳
func (s *dataSourceSvc) routedThrough(ctx context.Context, channelID int64) (map[*datasource_entity.DataSource]int, *channel_entity.Channel, error) {
	rows, err := datasource_repo.DataSource().List(ctx)
	if err != nil {
		return nil, nil, err
	}
	byID, err := s.channels(ctx)
	if err != nil {
		return nil, nil, err
	}
	targets := map[*datasource_entity.DataSource]int{}
	for _, ds := range rows {
		for i, c := range via(byID, ds.ChannelID) {
			if c.ID == channelID {
				targets[ds] = i + 1
				break
			}
		}
	}
	return targets, byID[channelID], nil
}

func (s *dataSourceSvc) MarkHostKeyChanged(ctx context.Context, channelID int64) error {
	targets, c, err := s.routedThrough(ctx, channelID)
	if err != nil || c == nil {
		return err
	}
	for ds, hop := range targets {
		ds.SetFailure(datasource_entity.StatusHostKeyChanged, code.ChannelHopFailed, datasource_entity.HopStatus{
			Hop: hop, ChannelID: c.ID, Name: c.Name, Kind: c.Kind, Reason: string(netchain.ReasonHostKeyChanged),
		})
		ds.PresentedHostKey = ""
		if err := datasource_repo.DataSource().Save(ctx, ds); err != nil && !errors.Is(err, datasource_repo.ErrNotFound) {
			return err
		}
	}
	return nil
}

func (s *dataSourceSvc) RetestThroughChannel(ctx context.Context, channelID int64) error {
	targets, _, err := s.routedThrough(ctx, channelID)
	if err != nil {
		return err
	}
	// 各数据源最长 30 秒，并发测试；单个数据源的失败只记录日志，不影响其他数据源
	var wg sync.WaitGroup
	sem := make(chan struct{}, retestConcurrency)
	for ds := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(ds *datasource_entity.DataSource) {
			defer func() { <-sem; wg.Done() }()
			if _, _, err := s.testSaved(ctx, ds, ""); err != nil {
				logger.Ctx(ctx).Warn("重新测试数据源失败", zap.Int64("datasource_id", ds.ID), zap.Error(err))
				return
			}
			if err := s.save(ctx, ds); err != nil {
				logger.Ctx(ctx).Warn("保存数据源测试结果失败", zap.Int64("datasource_id", ds.ID), zap.Error(err))
			}
		}(ds)
	}
	wg.Wait()
	return nil
}

func (s *dataSourceSvc) Delete(ctx context.Context, req *api.DeleteRequest) (*api.DeleteResponse, error) {
	ds, err := s.find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	if err := datasource_repo.DataSource().Delete(ctx, ds.ID); err != nil {
		return nil, err
	}
	return &api.DeleteResponse{}, nil
}

func (s *dataSourceSvc) References(ctx context.Context) (map[int64][]*channelapi.Ref, error) {
	rows, err := datasource_repo.DataSource().List(ctx)
	if err != nil {
		return nil, err
	}
	refs := map[int64][]*channelapi.Ref{}
	for _, ds := range rows {
		if ds.ChannelID != 0 {
			refs[ds.ChannelID] = append(refs[ds.ChannelID], &channelapi.Ref{ID: ds.ID, Name: ds.Name})
		}
	}
	return refs, nil
}

func (s *dataSourceSvc) List(ctx context.Context, _ *api.ListRequest) (*api.ListResponse, error) {
	rows, err := datasource_repo.DataSource().List(ctx)
	if err != nil {
		return nil, err
	}
	byID, err := s.channels(ctx)
	if err != nil {
		return nil, err
	}
	items := make([]*api.Item, 0, len(rows))
	for _, ds := range rows {
		items = append(items, toItem(ctx, ds, byID))
	}
	return &api.ListResponse{Items: items}, nil
}

func (s *dataSourceSvc) Get(ctx context.Context, req *api.GetRequest) (*api.GetResponse, error) {
	ds, err := s.find(ctx, req.ID)
	if err != nil {
		return nil, err
	}
	item, err := s.item(ctx, ds)
	if err != nil {
		return nil, err
	}
	return &api.GetResponse{Item: item}, nil
}

// item 单个数据源的响应（含链路）
func (s *dataSourceSvc) item(ctx context.Context, ds *datasource_entity.DataSource) (*api.Item, error) {
	byID, err := s.channels(ctx)
	if err != nil {
		return nil, err
	}
	return toItem(ctx, ds, byID), nil
}

func toItem(ctx context.Context, ds *datasource_entity.DataSource, byID map[int64]*channel_entity.Channel) *api.Item {
	item := &api.Item{
		ID:               ds.ID,
		Name:             ds.Name,
		Kind:             ds.Kind,
		Host:             ds.Host,
		Port:             ds.Port,
		Username:         ds.Username,
		AuthMethod:       ds.AuthMethod,
		HasPassword:      ds.Password != "",
		HasPrivateKey:    ds.PrivateKey != "",
		HasPassphrase:    ds.Passphrase != "",
		Database:         ds.Database,
		TLSMode:          ds.TLSMode,
		TLSCA:            ds.TLSCA,
		TLSClientCert:    ds.TLSClientCert,
		HasTLSClientKey:  ds.TLSClientKey != "",
		ChannelID:        ds.ChannelID,
		Address:          ds.Address(),
		Chain:            chainHops(byID, ds),
		HostKey:          ds.HostKey,
		PresentedHostKey: ds.PresentedHostKey,
		Status:           ds.Status,
		FailedHop:        failedHop(ds),
		CheckedAt:        ds.Checktime,
		CreatedAt:        ds.Createtime,
	}
	if ds.Version != "" || ds.System != "" {
		item.Server = &api.ServerInfo{Version: ds.Version, System: ds.System}
		if ds.TLSVersion != "" {
			item.Server.TLS = &api.TLS{Version: ds.TLSVersion, Verified: ds.TLSVerified}
		}
	}
	if ds.StatusCode != 0 {
		item.StatusMessage = statusMessage(ctx, ds)
	}
	return item
}
