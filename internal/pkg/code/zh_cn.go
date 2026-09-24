package code

var zhCN = map[int]string{ //nolint:gosec // 错误文案中的“密码”字样被误判为硬编码凭据
	ServerError:         "服务器内部错误",
	Unauthorized:        "未登录或登录已过期",
	CrossOriginRejected: "跨站请求被拒绝",

	AlreadyInitialized:    "已初始化：管理员账号已存在",
	SetupCodeInvalid:      "设置码不正确",
	UsernameInvalid:       "用户名须为 3–32 个字符，只能包含小写字母、数字以及 . _ -",
	PasswordTooShort:      "密码至少需要 12 个字符",
	LoginFailed:           "用户名或密码错误",
	TooManyAttempts:       "尝试次数过多，请在 %d 分钟后重试",
	CurrentPasswordWrong:  "当前密码不正确",
	NotInitialized:        "尚未创建管理员，请通过网页完成首次设置",
	SessionRequired:       "该操作只能在浏览器登录后进行，API 令牌无权调用",
	PasswordLoginDisabled: "密码登录已关闭",

	TokenInvalid:       "令牌无效",
	TokenRevoked:       "令牌已吊销",
	TokenExpired:       "令牌已过期",
	TokenNameInvalid:   "名称须为 1–64 个字符",
	TokenNameDuplicate: "已有同名的有效令牌",
	TokenExpiryInvalid: "有效期只能是 30 天、90 天、1 年或永不过期",
	TokenNotFound:      "令牌不存在",

	OIDCFieldRequired:              "显示名称、Issuer、Client ID 与 Client Secret 均为必填",
	OIDCRedirectInvalid:            "回调地址无效",
	OIDCIssuerUnreachable:          "无法连接 %s：%s",
	OIDCDiscoveryInvalid:           "OIDC discovery 文档无效或 Issuer 不一致：%s",
	OIDCResetConfirmRequired:       "修改 Issuer 或 Client ID 会清除已有绑定，请确认后再保存",
	OIDCNotConfigured:              "尚未配置 OIDC",
	OIDCAlreadyBound:               "已绑定 OIDC 身份，请先解除当前绑定",
	PasswordLoginDisableNotAllowed: "需要先绑定 OIDC 身份，并用它成功登录一次，才能关闭密码登录",
}
