// Package code 对外返回的业务错误码
package code

// 通用错误码
const (
	RouteNotFound = iota + 10000 // 接口不存在
)

// 认证、API 密钥与设置相关错误码
const (
	Unauthorized                = iota + 11000 // 未登录或凭据无效
	AdminNotInitialized                        // 尚未创建管理员
	AdminAlreadyInitialized                    // 管理员已创建
	LoginFailed                                // 用户名或密码错误（带剩余次数）
	LoginLocked                                // 登录被锁定（带剩余分钟）
	PasswordTooShort                           // 密码太短
	PasswordTooLong                            // 密码太长
	OldPasswordWrong                           // 原密码错误
	APIKeyNotFound                             // API 密钥不存在
	SettingMaxConcurrentInvalid                // 同时运行任务数不合法
	SettingRetentionInvalid                    // 记录保留天数不合法
	SettingTimezoneInvalid                     // 时区不合法
	MasterKeyEmpty                             // 新主密钥为空
	MasterKeySame                              // 新主密钥与当前主密钥相同
	MasterKeyRotateFailed                      // 轮换主密钥失败（已保持原状）
)

// 通知相关错误码
const (
	NotifyURLInvalid         = iota + 11100 // Webhook 地址不是合法的 http(s) 地址
	NotifyURLEmpty                          // 尚未配置 Webhook 地址，无法发送测试消息
	NotifyStaleHoursInvalid                 // 太久没有成功的阈值不合法
	NotifyHeaderInvalid                     // 请求头名或值不合法
	NotifyHeaderValueMissing                // 请求头没有已保存的值，需要填写
	NotifyTestFailed                        // 测试消息没有发出（连接失败等）
	NotifyPanelURLInvalid                   // 面板地址不是合法的 http(s) 地址
)

// 服务器与代理链相关错误码
const (
	ServerNotFound               = iota + 11200 // 服务器不存在
	ServerFieldInvalid                          // 名称、地址、端口、用户不合法
	ServerAuthTypeInvalid                       // 认证方式不合法
	ServerCredentialRequired                    // 缺少密码或私钥
	ServerCredentialMissing                     // 回传了占位，但没有已保存的值
	ServerPrivateKeyInvalid                     // 私钥无法解析或口令错误
	ServerHopInvalid                            // 代理链某一跳不合法
	ServerFingerprintUnconfirmed                // 目标主机指纹尚未核对
	ServerFingerprintInvalid                    // 指纹格式不对
	ServerInUse                                 // 被任务使用，不能删除
	ServerUsedAsJump                            // 被其他服务器用作跳板，不能删除
	ServerCredentialRetarget                    // 地址或用户改了，已保存的密码不能沿用
	ServerUsedByRepo                            // 被 SFTP 存储位置用来连接，不能删除
)

// 存储位置相关错误码
const (
	RepoNotFound                 = iota + 11300 // 存储位置不存在
	RepoFieldInvalid                            // 名称、地址参数不合法
	RepoTypeInvalid                             // 类型不合法
	RepoCredentialRequired                      // 缺少密码、私钥或 Secret Access Key
	RepoCredentialMissing                       // 回传了占位，但没有已保存的值
	RepoHostKeyUnconfirmed                      // SFTP 主机公钥尚未核对
	RepoHostKeyInvalid                          // SFTP 主机公钥格式不对
	RepoHostKeyProbeFailed                      // 取不到 SFTP 主机公钥
	RepoRecoveryKeyNotSaved                     // 生成恢复密钥时没有确认已存进密码管理器
	RepoAccessFailed                            // 无法访问存储位置（带 restic 输出）
	RepoPathMissing                             // 本地目录不存在
	RepoNotEmpty                                // 本地目录不为空也不是 restic 仓库
	RepoExistingPasswordRequired                // 已是 restic 仓库，需要现有密码
	RepoExistingPasswordWrong                   // 现有密码错误
	RepoBusy                                    // 正在备份或维护
	RepoKeyOperationFailed                      // 密钥操作失败，已保持原状
	RepoRecoveryKeyExists                       // 已有恢复密钥
	RepoRecoveryKeyMissing                      // 没有恢复密钥
	RepoNotMounted                              // 本地目录缺少 .opsnap-repo
	RepoUnlockNotConfirmed                      // 解除仓库锁未二次确认
	RepoUnlockFailed                            // 解除仓库锁失败
	RepoInUse                                   // 被任务使用，不能删除
	RepoServerNotFound                          // SFTP 经由的服务器不存在
	RepoRecoveryKeyInvalid                      // 没有提交恢复密钥或格式不对
	RepoCredentialRetarget                      // 地址或用户改了，已保存的密码不能沿用
	RepoProbeTimeout                            // 探测仓库到上限仍没有结果（restic 一直在重试）
)

// 备份任务相关错误码
const (
	JobNotFound        = iota + 11400 // 任务不存在
	JobFieldInvalid                   // 字段校验失败，消息每行一个字段，写法 "[字段名] 原因"
	JobSourceNotListed                // 这种数据源没有库列表可读
)

// 运行记录相关错误码
const (
	RunNotFound         = iota + 11500 // 运行记录不存在
	RunAlreadyRunning                  // 同一任务正在运行，不能再次触发
	RunNotRunning                      // 运行已经结束，不能停止
	RunStopNotConfirmed                // 停止运行未二次确认
)

// 快照相关错误码
const (
	SnapshotNotFound     = iota + 11600 // 快照不存在，或不属于这个任务
	SnapshotPathNotFound                // 快照里没有这个路径，或没有选择路径
	SnapshotRepoLocked                  // 仓库被其他进程锁住
	SnapshotReadFailed                  // 读取仓库失败（带 restic 输出）
	SnapshotNotDatabase                 // 这个任务不是数据库任务，没有导出文件
)

// 概览相关错误码
const (
	OverviewWindowInvalid = iota + 11700 // 时间线窗口不是 7d 或 30d
)

// 单点登录（OIDC）相关错误码
const (
	SSOIssuerInvalid             = iota + 11800 // Issuer 不是合法的 http(s) 地址
	SSOClientIDRequired                         // 缺少 Client ID
	SSOClientSecretRequired                     // 第一次保存时缺少 Client Secret
	SSOButtonLabelInvalid                       // 按钮文字为空或过长
	SSOPanelURLInvalid                          // 面板对外地址不是合法的 http(s) 地址
	SSODiscoveryFailed                          // 读取发现文档失败（带地址和原因）
	SSOIssuerMismatch                           // 发现文档声明的 issuer 与填写的不一致（带地址、声明的 issuer）
	SSONotConfigured                            // 还没有保存提供方配置
	SSONotEnabled                               // 单点登录未启用（未配置或未绑定）
	SSOBindRequiresSession                      // 绑定只能由已登录面板的管理员发起
	SSOStateInvalid                             // 登录请求过期、已用过或不是这个浏览器发起的
	SSOProviderDenied                           // 提供方拒绝授权或返回错误（带按钮文字、错误码）
	SSOProviderUnreachable                      // 连不上提供方（带按钮文字、Issuer、原因）
	SSOTokenInvalid                             // 身份令牌校验失败（带按钮文字、哪一项）
	SSOAccountMismatch                          // 不是绑定的账号（带按钮文字、显示名）
	SSOAlreadyBound                             // 已绑定账号，换账号要先解除
	SSOPasswordLoginNeedsBinding                // 没有绑定账号时不能关闭密码登录
	SSOLockedWhilePasswordOff                   // 密码登录关闭期间不能改 Issuer/Client ID、解除绑定或停用
	PasswordLoginDisabled                       // 密码登录已关闭（带按钮文字）
	SSOConfigChanged                            // 授权期间提供方配置被修改或停用
)

// 密钥库（服务器登录用的密码与私钥）相关错误码
const (
	CredentialNotFound          = iota + 11900 // 密钥不存在
	CredentialFieldInvalid                     // 名称、类型、密码、私钥、口令、备注不合法
	CredentialNameDuplicate                    // 密钥名称已被其他共享密钥使用
	CredentialEmpty                            // 密钥没有内容（既没有密码也没有私钥）
	CredentialInUse                            // 被服务器使用，不能删除
	CredentialKindInvalid                      // 密钥类型不合法
	CredentialPrivateKeyInvalid                // 私钥无法解析或口令错误
)
