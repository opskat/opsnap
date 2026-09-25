// Package code 定义接口错误码及其中英文文案。
// 新增错误码时必须同时在 zhCN 与 en 中补充文案，code_test 会检查两者一致。
package code

import "github.com/cago-frame/cago/pkg/i18n"

// 语言标识，与 cago i18n 的 DefaultLang 保持一致
const (
	LangZhCN = "zh-cn"
	LangEn   = "en"
)

// 通用
const (
	ServerError = iota + 10000
	Unauthorized
	CrossOriginRejected
)

// 首次设置与账号
const (
	AlreadyInitialized = iota + 10100
	SetupCodeInvalid
	UsernameInvalid
	PasswordTooShort
	LoginFailed
	TooManyAttempts
	CurrentPasswordWrong
	NotInitialized
	SessionRequired
	PasswordLoginDisabled
	ReauthPasswordWrong
)

// API 令牌
const (
	TokenInvalid = iota + 10200
	TokenRevoked
	TokenExpired
	TokenNameInvalid
	TokenNameDuplicate
	TokenExpiryInvalid
	TokenNotFound
)

// OIDC
const (
	OIDCFieldRequired = iota + 10300
	OIDCRedirectInvalid
	OIDCIssuerUnreachable
	OIDCDiscoveryInvalid
	OIDCResetConfirmRequired
	OIDCNotConfigured
	OIDCAlreadyBound
	PasswordLoginDisableNotAllowed
)

// 存储
const (
	StorageNotFound = iota + 10400
	StorageNameInvalid
	StorageNameDuplicate
	StoragePathRelative
	StorageS3FieldRequired
	StorageEndpointScheme
	StorageLocationInUse
	StorageLocationNotEmpty
	StorageUnreachable
	StorageNotWritable
	StorageNotDirectory
	StorageNoAccess
	StorageBucketNotFound
	StorageAccessDenied
	StorageKeyRequired
	StorageKeyTooShort
	StorageKeyNotConfirmed
	StorageKeyInvalid
	StorageManagedKeyInvalid
	StorageNotRepository
	StorageLocationChangeConfirm
	StorageAlreadyRepository
	StorageReauthRequired
	StorageDirNameInvalid
	StorageDirExists
	StorageDirNoPermission
	StorageDirNoAccess
	StorageDirCreateFailed
)

// 网络通道
const (
	ChannelNotFound = iota + 10500
	ChannelNameInvalid
	ChannelNameDuplicate
	ChannelHostRequired
	ChannelPortInvalid
	ChannelUserRequired
	ChannelAuthMethodInvalid
	ChannelPasswordRequired
	ChannelPrivateKeyRequired
	ChannelSOCKS5CredentialPair
	ChannelPassphraseMissing
	ChannelPassphraseWrong
	ChannelKeyInvalid
	ChannelViaNotFound
	ChannelViaCycle
	ChannelChainTooLong
	ChannelHopFailed
	ChannelHostKeyChanged
	ChannelInUse
	ChannelNotSSH
	// 以下为一跳失败的原因，只用于拼接 ChannelHopFailed 的文案
	ChannelReasonUnreachable
	ChannelReasonTimeout
	ChannelReasonCanceled
	ChannelReasonProtocol
	ChannelReasonNegotiation
	ChannelReasonAuthFailed
	ChannelReasonHostKeyUnknown
	ChannelReasonHostKeyChanged
)

func init() {
	i18n.DefaultLang = LangZhCN
	i18n.Register(LangZhCN, zhCN)
	i18n.Register(LangEn, en)
}
