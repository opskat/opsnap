// Package code 定义接口错误码及其中英文文案。
// 新增错误码时必须同时在 zhCN 与 en 中补充文案，code_test 会检查两者一致。
package code

import (
	"context"

	"github.com/cago-frame/cago/pkg/i18n"
)

// 语言标识，与 cago i18n 的 DefaultLang 保持一致
const (
	LangZhCN = "zh-cn"
	LangEn   = "en"
)

// Lang ctx 的界面语言：LangZhCN 或 LangEn（未设置时为默认语言 LangZhCN）
func Lang(ctx context.Context) string { return i18n.T(ctx, Language) }

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
	// StorageInUse 被任务引用时不能删除，参数为引用它的任务名（docs/specs/2026-09-27-backup-jobs.md「对已有页面的影响」）
	StorageInUse
	// StorageLocationLocked 被任务引用时不能更改位置，参数同 StorageInUse
	StorageLocationLocked
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

// 数据源
const (
	DataSourceNotFound = iota + 10600
	DataSourceNameInvalid
	DataSourceNameDuplicate
	DataSourceHostRequired
	DataSourcePortInvalid
	DataSourceUserRequired
	DataSourceAuthMethodInvalid
	DataSourcePasswordRequired
	DataSourcePrivateKeyRequired
	DataSourcePassphraseMissing
	DataSourcePassphraseWrong
	DataSourceKeyInvalid
	DataSourceTLSPairRequired
	DataSourceCAInvalid
	DataSourceClientCertInvalid
	DataSourceClientKeyInvalid
	DataSourceChannelNotFound
	DataSourceConfigInvalid
	DataSourceTestFailed
	DataSourceNotServerFile
	// 以下为数据源本身连接失败的原因，只用于拼接 DataSourceTestFailed 的文案
	DataSourceReasonUnreachable
	DataSourceReasonTimeout
	DataSourceReasonCanceled
	DataSourceReasonAuthFailed
	DataSourceReasonTLS
	DataSourceReasonCertificate
	DataSourceReasonFailed
	// DataSourceDatabasesFailed 实时读取数据库列表失败，参数为原因（原文，已去掉秘密）
	DataSourceDatabasesFailed
	// DataSourceInUse 被任务引用时不能删除，参数为引用它的任务名（docs/specs/2026-09-27-backup-jobs.md「对已有页面的影响」）
	DataSourceInUse
)

// 备份任务
const (
	JobNotFound = iota + 10700
	JobNameInvalid
	JobNameDuplicate
	JobTypeUnsupported
	JobDataSourceNotFound
	JobDataSourceUnsupported
	JobDataSourceNotReady
	JobStorageNotFound
	JobStorageNotReady
	JobScopeInvalid
	JobDatabasesRequired
	JobMethodUnsupported
	JobExcludeInvalid
	JobPrefixInvalid
	JobPrefixConflict
	JobCompressionInvalid
	JobScheduleInvalid
	JobTimezoneInvalid
	JobRetentionDaysInvalid
	JobRetentionWeeksInvalid
	JobRetentionMonthsInvalid
	JobRetriesInvalid
	JobRetryIntervalInvalid
	JobTimeoutInvalid
	JobImmutableField
	JobRunActive
	// 以下只用于删除任务响应中的快照提示
	JobSnapshotsNotDeleted
	JobSnapshotsUnreachable
	// JobRunAlreadyActive 立即执行时任务已在运行或排队
	JobRunAlreadyActive
	JobRunNotFound
	// JobRunFinished 取消已结束的运行
	JobRunFinished
	// JobStatsStorageUnreadable 只用于统计响应中的提示：无法读取存储中的快照
	JobStatsStorageUnreadable
	// JobReason* 运行记录的固定原因（job_entity.Run.ReasonCode），只用于运行记录的显示
	JobReasonStillRunning
	JobReasonRetryVoided
	JobReasonInterrupted
	JobReasonRestart
	JobReasonTimeout
	// JobDuration* 时长的描述，用于 JobReasonTimeout
	JobDurationMinutes
	JobDurationHours
	JobDurationHoursMinutes
	// JobSnapshotsUnreachableCount 只用于删除任务响应中的快照提示：无法打开存储，份数取任务上记录的快照数
	JobSnapshotsUnreachableCount
)

// 运行记录（失败原因、执行日志）中由 OpsNap 生成的文字，只用于显示：由 internal/pkg/l10n 在显示时按查看者的
// 界面语言拼出，导出工具与数据库的原文作为参数原样代入
const (
	// Language 当前界面语言的标识（LangZhCN、LangEn），见 Lang
	Language = iota + 10800
	// ListSep 列表分隔符
	ListSep
	// WrapColon “原因: 详情”（半角冒号）
	WrapColon
	// WrapFullColon “原因：详情”（中文为全角冒号）
	WrapFullColon
	// RunLog* 运行日志与失败原因中 OpsNap 自己的文字（job_svc）
	RunLogPrepare
	RunStorageMissing
	RunStorageNotOK
	RunDataSourceMissing
	RunHostKeyChanged
	RunLogToolPath
	RunOpenStorageFailed
	RunLogStorageOpened
	RunLogConnecting
	RunConnectFailed
	RunLogConnected
	RunListDatabasesFailed
	RunNoDatabases
	RunMissingDatabases
	RunLogDatabases
	RunStartDumpFailed
	RunLogForwarding
	RunLogExporting
	RunLogExportChecked
	RunLogFileSize
	RunLogSnapshotVerified
	RunLogListSnapshotsFailed
	RunLogRetention
	RunLogDeleteFailed
	RunLogDeleted
	RunLogMaintainFailed
	RunLogMaintained
	RunLogCanceled
	RunLogAborted
	// RunReasonEllipsis 过长的失败原因中省略部分的标记
	RunReasonEllipsis
	// Dump* 导出包（internal/pkg/dump）的错误与日志
	DumpErrInvalidOptions
	DumpErrToolNotFound
	DumpErrToolVersion
	DumpErrUnsupportedTLS
	DumpErrPrivilege
	DumpErrIncomplete
	DumpErrClosed
	DumpToolFailed
	DumpStderrOmitted
	DumpViaChannelFailed
	DumpUnsupportedType
	DumpNoDialer
	DumpNoDatabases
	DumpEmptyDatabase
	DumpGlobalsPGOnly
	DumpMySQLOnlyOptions
	DumpExcludeFormat
	DumpExcludeFormatMySQL
	DumpExcludeFormatPG
	DumpToolMissing
	DumpPkgMySQL
	DumpPkgPostgreSQL
	DumpToolVersionUnknown
	DumpConnect
	DumpNoSystemCA
	DumpStartTool
	DumpReadOutput
	DumpGlobalsSuperuser
	DumpIncompleteMySQL
	DumpIncompletePGArchive
	DumpIncompletePGGlobals
	DumpMariaDBTLS
	DumpMySQLDumpOlder
	DumpVerifyCAOnly
	DumpListTables
	DumpNonInnoDB
	DumpExcludeUnmatched
	DumpAccounts
	DumpAccountsPrivilege
	DumpPGToolTooOld
	DumpPGPasswordNewline
	DumpCheckExclude
	// Kopia* 仓库包（internal/pkg/kopiarepo）写入快照、保留与维护的错误
	KopiaErrInvalidSnapshot
	KopiaErrVerify
	KopiaErrNotJobSnapshot
	KopiaUnsupportedCompression
	KopiaNoPrefix
	KopiaNoFiles
	KopiaBadFileName
	KopiaDupFileName
	KopiaNoReader
	KopiaWriteSnapshot
	KopiaWriteIncomplete
	KopiaWriteFatal
	KopiaSaveManifest
	KopiaVerifyDeleteFailed
	KopiaRootNotDir
	KopiaReadDir
	KopiaFileCount
	KopiaFileMissing
	KopiaFileSize
	KopiaFileNoObject
	KopiaFileIncomplete
	KopiaListSnapshots
	KopiaLoadManifests
	KopiaDeleteSnapshots
	KopiaMaintainMode
	KopiaMaintainUnsupported
	KopiaMaintain
	// Net* 网络链路包（internal/pkg/netchain）的错误
	NetErrTooManyHops
	NetErrCycle
	NetErrInvalidHop
	NetErrAuthFailed
	NetErrNegotiation
	NetErrProtocol
	NetErrHostKeyUnknown
	NetErrHostKeyChanged
	NetErrPassphraseMissing
	NetErrPassphraseWrong
	NetErrKeyInvalid
	NetHopFailed
	NetHostKeyChangedDetail
	NetHostKeyUnknownDetail
	NetTimeout
	NetCanceled
	NetSSHHandshake
	NetSocksNetwork
	NetSocksClosed
	NetSocksNotSocks
	NetSocksAuthRequired
	NetSocksNoUserPass
	NetSocksBadMethod
	NetSocksCredTooLong
	NetSocksReply1
	NetSocksReply2
	NetSocksReply3
	NetSocksReply4
	NetSocksReply5
	NetSocksReply6
	NetSocksReply7
	NetSocksReply8
	NetSocksReplyCode
	NetSocksBadPort
	NetSocksHostTooLong
	NetSocksBadReply
	NetSocksConnectFailed
	NetSocksBadAddrType
	NetUnknownKind
	NetNoHost
	NetBadPort
	NetNoSSHUser
	NetSSHAuthRequired
	NetHopCount
	NetChannelTwice
	NetHopInvalid
	NetDialFailed
	NetNotSSHTarget
	// Conn* 数据源连接包（internal/pkg/dsconn）的错误
	ConnErrInvalidConfig
	ConnNoUser
	ConnUnknownTLSMode
	ConnCANotPEM
	ConnCertNotPEM
	ConnKeyInvalid
	ConnKeyMismatch
	ConnCertMissing
	ConnKeyMissing
	ConnNoPeerCert
	// WrapParen “原因（详情）”
	WrapParen
	// Kopia* 仓库包的其余错误（打开、建库、位置）
	KopiaErrInvalidPassword
	KopiaErrNotEmpty
	KopiaErrAlreadyRepository
	KopiaErrNotRepository
	KopiaCreate
	KopiaConnect
	KopiaNotDirectory
	KopiaErrRelativePath
	KopiaErrEndpointScheme
	KopiaErrUnknownKind
	KopiaGenerateKey
	KopiaErrInvalidDirName
	KopiaReadSnapshot
	KopiaUsage
	// StorageErrNotReady 存储服务打开写入会话时状态不是正常
	StorageErrNotReady
	// Secret* 主密钥与加解密（internal/pkg/secret、secret_svc）的错误
	SecretErrInvalidKey
	SecretErrDecrypt
	SecretGenerateKey
	SecretInitCipher
	SecretRandom
	SecretErrKeyMissing
	SecretErrKeyMismatch
	SecretErrNotInitialized
	// ProbeVersionUnrecognized 导出工具 --version 的输出中找不到版本号
	ProbeVersionUnrecognized
	// DataSourceListUnsupported 读取数据库列表时数据源类型不支持
	DataSourceListUnsupported
)

func init() {
	i18n.DefaultLang = LangZhCN
	i18n.Register(LangZhCN, zhCN)
	i18n.Register(LangEn, en)
}
