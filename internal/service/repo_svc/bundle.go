package repo_svc

import (
	"fmt"
	"strings"
	"time"

	"github.com/opskat/opsnap/internal/model/entity/repo_entity"
)

// shQuote 单引号引用，供命令示例直接复制
func shQuote(v string) string {
	return "'" + strings.ReplaceAll(v, "'", `'\''`) + "'"
}

func bundleContent(r *repo_entity.Repo, now time.Time) string {
	var b strings.Builder
	w := func(format string, a ...any) { fmt.Fprintf(&b, format+"\n", a...) }
	p := r.Params

	w("opsnap 恢复包：%s", r.Name)
	w("生成时间：%s", now.Format(time.RFC3339))
	w("")
	w("本文件不包含任何密钥或密码。恢复时需要你自己保管的恢复密钥，以及访问 NAS 的账号。")
	if !r.HasRecoveryKey() {
		w("注意：这个存储位置目前没有恢复密钥。opsnap 所在机器损坏后将无法解密备份，请尽快在 opsnap 的「密钥」对话框里生成一把并妥善保管。")
	}
	w("")
	w("仓库类型：%s", typeLabel(r.Type))
	w("仓库地址：%s", r.Address())
	w("加密方式：AES-256（restic）")
	w("")
	w("一、准备")
	w("1. 在任意一台能访问 NAS 的机器上安装 restic 0.17 或更高版本：https://restic.net")
	w("2. 设置仓库地址与访问凭据（尖括号里的内容换成你自己的）：")
	w("")
	switch r.Type {
	case repo_entity.TypeLocal:
		w("   # 先把 NAS 上的共享目录挂载到本机，再把下面的路径换成实际的挂载位置")
		w("   export RESTIC_REPOSITORY=%s", shQuote(r.Address()))
	case repo_entity.TypeSFTP:
		w("   # restic 会调用本机的 ssh 连接 NAS，按提示输入 NAS 用户 %s 的密码或配置好私钥", p.Username)
		w("   export RESTIC_REPOSITORY=%s", shQuote(r.Address()))
	case repo_entity.TypeS3:
		w("   export RESTIC_REPOSITORY=%s", shQuote(r.Address()))
		w("   export AWS_ACCESS_KEY_ID='<Access Key ID>'")
		w("   export AWS_SECRET_ACCESS_KEY='<Secret Access Key>'")
		if p.Region != "" {
			w("   export AWS_DEFAULT_REGION=%s", shQuote(p.Region))
		}
	case repo_entity.TypeREST:
		w("   export RESTIC_REPOSITORY=%s", shQuote(r.Address()))
		if p.Username != "" {
			w("   export RESTIC_REST_USERNAME=%s", shQuote(p.Username))
			w("   export RESTIC_REST_PASSWORD='<REST 密码>'")
		}
	case repo_entity.TypeWebDAV:
		vendor := p.Vendor
		if vendor == "" {
			vendor = "other"
		}
		w("   # WebDAV 经 rclone 访问，先安装 rclone（https://rclone.org）并创建名为 opsnap 的远端：")
		w("   rclone config create opsnap webdav url=%s vendor=%s user=%s pass='<WebDAV 密码>' --obscure",
			shQuote(p.URL), shQuote(vendor), shQuote(p.Username))
		w("   export RESTIC_REPOSITORY=%s", shQuote(r.Address()))
	}
	w("")
	opt := ""
	if r.Type == repo_entity.TypeS3 && p.PathStyle {
		opt = " -o s3.bucket-lookup=path"
	}
	w("3. 不要把恢复密钥写进命令行或脚本。执行下面的命令时 restic 会提示输入密码，粘贴恢复密钥即可。")
	w("")
	w("二、查看快照")
	w("   restic snapshots%s", opt)
	w("")
	w("三、恢复")
	w("   # 恢复最新快照的全部内容")
	w("   restic restore latest --target ./restore%s", opt)
	w("   # 只恢复某个快照里的某个文件或目录")
	w("   restic restore <快照 ID> --target ./restore --include <快照内路径>%s", opt)
	w("   # 把数据库导出文件直接输出到本地文件")
	w("   restic dump <快照 ID> <快照内路径>%s > <本地文件>", opt)
	w("")
	w("四、检查仓库完整性（可选）")
	w("   restic check%s", opt)
	return b.String()
}

func typeLabel(t string) string {
	switch t {
	case repo_entity.TypeLocal:
		return "本地目录"
	case repo_entity.TypeSFTP:
		return "SFTP"
	case repo_entity.TypeS3:
		return "S3 兼容"
	case repo_entity.TypeREST:
		return "REST Server"
	case repo_entity.TypeWebDAV:
		return "WebDAV"
	}
	return t
}
