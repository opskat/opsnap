package cmdbuild

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const (
	// DefaultHost 快照固定使用的主机名；容器重建后主机名会变，不固定会让 forget 分组错乱
	DefaultHost = "opsnap"
	// rcloneRemote 经 rclone 访问的后端在环境变量里使用的远端名
	rcloneRemote = "opsnap"
	// resticProgressFPS 输出 JSON 进度的频率（每 10 秒一次），避免日志被进度刷屏
	resticProgressFPS = "0.1"
)

// Compression restic 压缩方式
type Compression string

const (
	CompressionAuto Compression = "auto"
	CompressionMax  Compression = "max"
	CompressionOff  Compression = "off"
)

// Backend restic 仓库后端
type Backend interface {
	// location 返回仓库地址、需要的环境变量和 -o 扩展选项
	location() (repo string, env []string, opts []string, err error)
}

// LocalBackend 本机目录（通常是挂载进容器的 NAS 共享目录）
type LocalBackend struct {
	Path string
}

func (b LocalBackend) location() (string, []string, []string, error) {
	if err := checkAbs("本地仓库路径", b.Path); err != nil {
		return "", nil, nil, err
	}
	return b.Path, nil, nil, nil
}

// S3Backend S3 兼容存储（群晖/飞牛的 S3 服务、MinIO 等）
type S3Backend struct {
	// Endpoint 形如 https://nas:9000，允许 http（内容已由 restic 加密）
	Endpoint        string
	Bucket          string
	Prefix          string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
	// PathStyle 使用路径风格访问 bucket（MinIO 等自建服务通常需要）
	PathStyle bool
}

func (b S3Backend) location() (string, []string, []string, error) {
	if b.Endpoint == "" || b.Bucket == "" {
		return "", nil, nil, errors.New("S3 需要 endpoint 和 bucket")
	}
	repo := "s3:" + strings.TrimRight(b.Endpoint, "/") + "/" + b.Bucket
	if p := strings.Trim(b.Prefix, "/"); p != "" {
		repo += "/" + p
	}
	envs := []string{
		env("AWS_ACCESS_KEY_ID", b.AccessKeyID),
		env("AWS_SECRET_ACCESS_KEY", b.SecretAccessKey),
	}
	if b.Region != "" {
		envs = append(envs, env("AWS_DEFAULT_REGION", b.Region))
	}
	var opts []string
	if b.PathStyle {
		opts = append(opts, "s3.bucket-lookup=path")
	}
	return repo, envs, opts, nil
}

// RESTBackend restic REST server
type RESTBackend struct {
	// URL 形如 https://nas:8000/opsnap/，不能内嵌用户名密码
	URL      string
	User     string
	Password string
}

func (b RESTBackend) location() (string, []string, []string, error) {
	u, err := url.Parse(b.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", nil, nil, fmt.Errorf("REST 地址无效：%q", b.URL)
	}
	if u.User != nil {
		return "", nil, nil, errors.New("REST 地址里不能包含用户名或密码，请分别填写")
	}
	var envs []string
	if b.User != "" {
		envs = append(envs, env("RESTIC_REST_USERNAME", b.User))
	}
	if b.Password != "" {
		envs = append(envs, env("RESTIC_REST_PASSWORD", b.Password))
	}
	return "rest:" + b.URL, envs, nil, nil
}

// WebDAVBackend 经 rclone 访问的 WebDAV
type WebDAVBackend struct {
	URL  string
	Path string
	User string
	// ObscuredPassword 经 `rclone obscure` 处理后的密码（见 RcloneObscure）
	ObscuredPassword string
	// Vendor rclone 的 webdav vendor，为空时用 other
	Vendor string
}

func (b WebDAVBackend) location() (string, []string, []string, error) {
	u, err := url.Parse(b.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", nil, nil, fmt.Errorf("WebDAV 地址无效：%q", b.URL)
	}
	vendor := b.Vendor
	if vendor == "" {
		vendor = "other"
	}
	envs := []string{
		rcloneEnv("TYPE", "webdav"),
		rcloneEnv("URL", b.URL),
		rcloneEnv("VENDOR", vendor),
	}
	if b.User != "" {
		envs = append(envs, rcloneEnv("USER", b.User))
	}
	if b.ObscuredPassword != "" {
		envs = append(envs, rcloneEnv("PASS", b.ObscuredPassword))
	}
	return "rclone:" + rcloneRemote + ":" + b.Path, envs, nil, nil
}

// SFTPBackend 经 rclone 访问的 SFTP；同时支持私钥文件和密码，并强制校验主机指纹
type SFTPBackend struct {
	Host string
	// Port 为 0 时用 22
	Port int
	User string
	Path string
	// KeyFile 私钥文件路径（由调用方以 0600 权限写入临时文件）
	KeyFile string
	// ObscuredPassword 经 `rclone obscure` 处理后的密码
	ObscuredPassword string
	// KnownHostsFile 已知主机文件，用于校验 NAS 指纹
	KnownHostsFile string
}

func (b SFTPBackend) location() (string, []string, []string, error) {
	if b.Host == "" || b.User == "" {
		return "", nil, nil, errors.New("SFTP 需要地址和用户名")
	}
	if b.Path == "" {
		return "", nil, nil, errors.New("SFTP 需要仓库路径")
	}
	if b.KeyFile == "" && b.ObscuredPassword == "" {
		return "", nil, nil, errors.New("SFTP 需要私钥或密码")
	}
	if b.KnownHostsFile == "" {
		return "", nil, nil, errors.New("SFTP 需要已知主机文件以校验指纹")
	}
	port := b.Port
	if port == 0 {
		port = 22
	}
	if err := checkPort(port); err != nil {
		return "", nil, nil, err
	}
	envs := []string{
		rcloneEnv("TYPE", "sftp"),
		rcloneEnv("HOST", b.Host),
		rcloneEnv("PORT", strconv.Itoa(port)),
		rcloneEnv("USER", b.User),
		rcloneEnv("KNOWN_HOSTS_FILE", b.KnownHostsFile),
	}
	if b.KeyFile != "" {
		envs = append(envs, rcloneEnv("KEY_FILE", b.KeyFile))
	}
	if b.ObscuredPassword != "" {
		envs = append(envs, rcloneEnv("PASS", b.ObscuredPassword))
	}
	return "rclone:" + rcloneRemote + ":" + b.Path, envs, nil, nil
}

// rcloneEnv 生成 rclone 远端配置的环境变量
func rcloneEnv(key, value string) string {
	return env("RCLONE_CONFIG_"+strings.ToUpper(rcloneRemote)+"_"+key, value)
}

// RcloneObscure 生成 `rclone obscure -`，明文密码经标准输入传入，输出即混淆后的密码。
// rclone 的 WebDAV/SFTP 配置只接受混淆后的密码。
func RcloneObscure(password string) (Cmd, error) {
	if password == "" {
		return Cmd{}, errors.New("密码不能为空")
	}
	return Cmd{Name: "rclone", Args: []string{"obscure", "-"}, Stdin: []byte(password)}, nil
}

// RcloneAbout 生成 `rclone about <远端>:<路径> --json`，查询经 rclone 访问的后端（SFTP、WebDAV）的剩余空间。
// 远端配置与混淆后的密码只经环境变量传入；其他后端返回错误。
func RcloneAbout(b Backend) (Cmd, error) {
	if b == nil {
		return Cmd{}, errors.New("缺少仓库后端")
	}
	repo, envs, _, err := b.location()
	if err != nil {
		return Cmd{}, err
	}
	remote, ok := strings.CutPrefix(repo, "rclone:")
	if !ok {
		return Cmd{}, errors.New("只有经 rclone 访问的后端能查询剩余空间")
	}
	return Cmd{Name: "rclone", Args: []string{"about", remote, "--json"}, Env: envs}, nil
}

// Repo 一个 restic 仓库及其访问参数
type Repo struct {
	Backend Backend
	// Password 仓库密码（opsnap 密钥），经 RESTIC_PASSWORD 传入
	Password string
	// Compression 为空时用 restic 默认值
	Compression Compression
	// LimitUploadKiB 上传限速，0 表示不限
	LimitUploadKiB int
}

// restic 生成一条 restic 命令：sub 是子命令，extra 是子命令参数
func (r Repo) restic(sub []string, extra ...string) (Cmd, error) {
	if r.Backend == nil {
		return Cmd{}, errors.New("缺少仓库后端")
	}
	if r.Password == "" {
		return Cmd{}, errors.New("缺少仓库密码")
	}
	repo, envs, opts, err := r.Backend.location()
	if err != nil {
		return Cmd{}, err
	}
	args := append([]string{}, sub...)
	for _, o := range opts {
		args = append(args, "-o", o)
	}
	switch r.Compression {
	case "":
	case CompressionAuto, CompressionMax, CompressionOff:
		args = append(args, "--compression", string(r.Compression))
	default:
		return Cmd{}, fmt.Errorf("未知的压缩方式：%q", r.Compression)
	}
	if r.LimitUploadKiB > 0 {
		args = append(args, "--limit-upload", strconv.Itoa(r.LimitUploadKiB))
	}
	args = append(args, extra...)
	allEnv := append([]string{env("RESTIC_REPOSITORY", repo), env("RESTIC_PASSWORD", r.Password)}, envs...)
	return Cmd{Name: "restic", Args: args, Env: allEnv}, nil
}

// tagHostArgs 生成 --host 与 --tag 参数
func tagHostArgs(host string, tags []string) []string {
	if host == "" {
		host = DefaultHost
	}
	args := make([]string, 0, 2+2*len(tags))
	args = append(args, "--host", host)
	for _, t := range tags {
		args = append(args, "--tag", t)
	}
	return args
}

// ResticInit restic init
func ResticInit(r Repo) (Cmd, error) {
	return r.restic([]string{"init"})
}

// StdinBackup 由 restic 运行导出命令，并把它的标准输出存成快照里的一个文件
type StdinBackup struct {
	// Filename 快照内的文件名，例如 postgres/app.dump
	Filename string
	Tags     []string
	// Host 为空时用 DefaultHost
	Host string
	// Command 导出命令；它的 Env 会并入 restic 的环境变量并由 restic 传给它
	Command Cmd
}

// ResticBackupStdin restic backup --stdin-from-command：导出数据经管道直接进入 restic，本机不落盘；
// 导出命令以非零退出码结束时 restic 不生成快照
func ResticBackupStdin(r Repo, b StdinBackup) (Cmd, error) {
	if b.Filename == "" {
		return Cmd{}, errors.New("缺少快照内的文件名")
	}
	if b.Command.Name == "" {
		return Cmd{}, errors.New("缺少导出命令")
	}
	if b.Command.Stdin != nil {
		// restic 不会把自己的标准输入交给导出命令
		return Cmd{}, errors.New("导出命令不能依赖标准输入")
	}
	extra := make([]string, 0, 10+2*len(b.Tags)+len(b.Command.Args))
	extra = append(extra, "--json", "--stdin-from-command", "--stdin-filename", b.Filename)
	extra = append(extra, tagHostArgs(b.Host, b.Tags)...)
	extra = append(extra, "--", b.Command.Name)
	extra = append(extra, b.Command.Args...)
	c, err := r.restic([]string{"backup"}, extra...)
	if err != nil {
		return Cmd{}, err
	}
	c.Env = append(c.Env, env("RESTIC_PROGRESS_FPS", resticProgressFPS))
	c.Env = append(c.Env, b.Command.Env...)
	return c, nil
}

// DirBackup 对本机镜像目录生成快照
type DirBackup struct {
	Path string
	Tags []string
	// Host 为空时用 DefaultHost
	Host string
}

// ResticBackupDir restic backup <镜像目录>
func ResticBackupDir(r Repo, b DirBackup) (Cmd, error) {
	if err := checkAbs("镜像目录", b.Path); err != nil {
		return Cmd{}, err
	}
	extra := append([]string{"--json"}, tagHostArgs(b.Host, b.Tags)...)
	extra = append(extra, b.Path)
	c, err := r.restic([]string{"backup"}, extra...)
	if err != nil {
		return Cmd{}, err
	}
	c.Env = append(c.Env, env("RESTIC_PROGRESS_FPS", resticProgressFPS))
	return c, nil
}

// ForgetSpec 按保留规则删除某个任务的过期快照
type ForgetSpec struct {
	// Tags 快照必须同时带有这些标签（用于限定到单个任务）
	Tags []string
	// Policy 保留参数，来自 retention.Policy.ForgetArgs
	Policy []string
	// Prune 同时清理空间；日常运行不清理，交给每周维护
	Prune bool
	// DryRun 只演算不删除，输出里的 reasons 说明每个被保留的快照为什么保留
	DryRun bool
}

// ResticForget restic forget
func ResticForget(r Repo, f ForgetSpec) (Cmd, error) {
	if len(f.Tags) == 0 {
		return Cmd{}, errors.New("forget 必须限定标签，避免删除其他任务的快照")
	}
	if len(f.Policy) == 0 {
		return Cmd{}, errors.New("forget 缺少保留规则")
	}
	extra := []string{"--json", "--tag", strings.Join(f.Tags, ",")}
	extra = append(extra, f.Policy...)
	if f.DryRun {
		// 只演算时 restic 允许不加锁；否则 forget 总是取独占锁，会让同一仓库上正在开始的备份失败
		extra = append(extra, "--dry-run", "--no-lock")
	}
	if f.Prune {
		extra = append(extra, "--prune")
	}
	return r.restic([]string{"forget"}, extra...)
}

// ResticSnapshots restic snapshots --json，tags 非空时只列出同时带这些标签的快照
func ResticSnapshots(r Repo, tags []string) (Cmd, error) {
	extra := []string{"--json"}
	if len(tags) > 0 {
		extra = append(extra, "--tag", strings.Join(tags, ","))
	}
	return r.restic([]string{"snapshots"}, extra...)
}

// ResticLs restic ls --json <快照> [路径]
func ResticLs(r Repo, snapshotID, dir string) (Cmd, error) {
	if err := checkOperand("快照 ID", snapshotID); err != nil {
		return Cmd{}, err
	}
	extra := []string{"--json", snapshotID}
	if dir != "" {
		if err := checkAbs("快照内路径", dir); err != nil {
			return Cmd{}, err
		}
		extra = append(extra, dir)
	}
	return r.restic([]string{"ls"}, extra...)
}

// ResticDump restic dump：把快照内的文件输出到标准输出；archive 为 tar 或 zip 时打包目录
func ResticDump(r Repo, snapshotID, file, archive string) (Cmd, error) {
	if err := checkOperand("快照 ID", snapshotID); err != nil {
		return Cmd{}, err
	}
	if err := checkAbs("快照内路径", file); err != nil {
		return Cmd{}, err
	}
	var extra []string
	switch archive {
	case "":
	case "tar", "zip":
		extra = append(extra, "--archive", archive)
	default:
		return Cmd{}, fmt.Errorf("未知的打包格式：%q", archive)
	}
	extra = append(extra, snapshotID, file)
	return r.restic([]string{"dump"}, extra...)
}

// ResticStats restic stats --json --mode <mode>（raw-data 用于计算去重压缩比）
func ResticStats(r Repo, mode string) (Cmd, error) {
	switch mode {
	case "restore-size", "files-by-contents", "blobs-per-file", "raw-data":
	default:
		return Cmd{}, fmt.Errorf("未知的统计模式：%q", mode)
	}
	return r.restic([]string{"stats"}, "--json", "--mode", mode)
}

// ResticCheck restic check；readDataSubset 非空时抽样读取数据，例如 "5%"
func ResticCheck(r Repo, readDataSubset string) (Cmd, error) {
	if readDataSubset == "" {
		return r.restic([]string{"check"})
	}
	if err := checkOperand("抽样比例", readDataSubset); err != nil {
		return Cmd{}, err
	}
	return r.restic([]string{"check"}, "--read-data-subset", readDataSubset)
}

// ResticPrune restic prune
func ResticPrune(r Repo) (Cmd, error) {
	return r.restic([]string{"prune"})
}

// ResticUnlock restic unlock；removeAll 时连同未过期的锁一起删除（需要管理员二次确认）
func ResticUnlock(r Repo, removeAll bool) (Cmd, error) {
	if removeAll {
		return r.restic([]string{"unlock"}, "--remove-all")
	}
	return r.restic([]string{"unlock"})
}

// ResticKeyList restic key list --json
func ResticKeyList(r Repo) (Cmd, error) {
	return r.restic([]string{"key", "list"}, "--json")
}

// ResticKeyAdd restic key add：用当前密码打开仓库，新密钥经标准输入传入
func ResticKeyAdd(r Repo, newPassword string) (Cmd, error) {
	if newPassword == "" {
		return Cmd{}, errors.New("新密钥不能为空")
	}
	c, err := r.restic([]string{"key", "add"}, "--new-password-file", "/dev/stdin")
	if err != nil {
		return Cmd{}, err
	}
	c.Stdin = []byte(newPassword)
	return c, nil
}

// ResticKeyRemove restic key remove <密钥 ID>；当前密码不能是被删除的那把
func ResticKeyRemove(r Repo, keyID string) (Cmd, error) {
	if err := checkOperand("密钥 ID", keyID); err != nil {
		return Cmd{}, err
	}
	return r.restic([]string{"key", "remove"}, keyID)
}
