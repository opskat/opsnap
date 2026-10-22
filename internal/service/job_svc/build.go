package job_svc

import (
	"context"
	"errors"
	"strings"

	jobapi "github.com/opskat/opsnap/internal/api/job"
	"github.com/opskat/opsnap/internal/model/entity/job_entity"
	"github.com/opskat/opsnap/internal/pkg/secret"
	"github.com/opskat/opsnap/internal/repository/repo_repo"
	"github.com/opskat/opsnap/internal/repository/server_repo"
	"github.com/opskat/opsnap/internal/service/repo_svc"
)

// assemble 把提交的配置整理成实体：去掉空白项、按数据源清掉无关字段、补默认值，
// 加密新填的数据库密码（占位保留 old 已保存的密文）。old 为 nil 表示新建。
// 返回的 FieldErrors 只包含整理过程中发现的问题（密码）；其余校验由调用方继续做。
func (s *jobSvc) assemble(_ context.Context, old *job_entity.Job, in *jobapi.JobInput) (*job_entity.Job, job_entity.FieldErrors, error) {
	j := &job_entity.Job{
		Name:       strings.TrimSpace(in.Name),
		SourceType: strings.TrimSpace(in.SourceType),
		RepoID:     in.RepoID,

		RetentionPreset: in.Retention.Preset,
		RetentionDays:   in.Retention.Days,
		RetentionWeeks:  in.Retention.Weeks,
		RetentionMonths: in.Retention.Months,

		TimeoutSec:    in.Advanced.TimeoutSec,
		BandwidthKbps: in.Advanced.BandwidthKbps,
		Compression:   in.Advanced.Compression,
		ExtraArgs:     cleanList(in.Advanced.ExtraArgs),
		Databases:     []string{}, Paths: []string{}, Excludes: []string{},
	}
	applySchedule(j, in.Schedule)
	if j.TimeoutSec == 0 {
		j.TimeoutSec = job_entity.DefaultTimeoutSec
	}
	if j.Compression == "" {
		j.Compression = job_entity.CompressionAuto
	}
	if j.RetentionPreset != "custom" {
		j.RetentionDays, j.RetentionWeeks, j.RetentionMonths = 0, 0, 0
	}

	var errs job_entity.FieldErrors
	switch {
	case job_entity.IsDatabase(j.SourceType):
		j.ConnMode = in.ConnMode
		if j.ConnMode == job_entity.ConnSSH {
			j.ServerID = in.ServerID
		}
		j.DBHost = strings.TrimSpace(in.DBHost)
		j.DBPort = in.DBPort
		if j.DBPort == 0 {
			j.DBPort = job_entity.DefaultPort(j.SourceType)
		}
		j.DBUser = strings.TrimSpace(in.DBUser)
		if job_entity.HasDatabaseList(j.SourceType) {
			j.AllDatabases = in.AllDatabases
			if !j.AllDatabases {
				j.Databases = cleanList(in.Databases)
			}
		}
		if j.SourceType == job_entity.SourceMongoDB {
			j.AuthDB = strings.TrimSpace(in.AuthDB)
			if j.AuthDB == "" {
				j.AuthDB = job_entity.DefaultMongoAuthDB
			}
		}
		cipher, fe, err := dbPassword(old, j, in.DBPassword)
		if err != nil {
			return nil, nil, err
		}
		if fe != nil {
			errs = append(errs, *fe)
		}
		j.DBPassword = cipher
	case j.SourceType == job_entity.SourceFiles:
		j.ServerID = in.ServerID
		j.Paths = cleanList(in.Paths)
		j.Excludes = cleanList(in.Excludes)
	}
	return j, errs, nil
}

func applySchedule(j *job_entity.Job, in jobapi.ScheduleInput) {
	j.ScheduleKind = in.Kind
	j.ScheduleTime = strings.TrimSpace(in.Time)
	j.ScheduleWeekday = in.Weekday
	j.ScheduleCron = strings.TrimSpace(in.Cron)
}

// cleanList 去掉首尾空白和空项，保持顺序；结果不为 nil
func cleanList(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		if v = strings.TrimSpace(v); v != "" {
			out = append(out, v)
		}
	}
	return out
}

// dbPassword 处理数据库密码：空串表示没有，占位保留 old 的密文，其余加密。
// 占位只在连接目标（数据源、方式、服务器、地址、端口、用户）都没变时有效，
// 避免把已保存的密码带去一个新地址。
func dbPassword(old, j *job_entity.Job, input string) (string, *job_entity.FieldError, error) {
	switch input {
	case "":
		return "", nil, nil
	case secret.Masked:
		switch {
		case old == nil || old.DBPassword == "":
			return "", &job_entity.FieldError{Field: "db_password", Message: "没有已保存的密码，请填写"}, nil
		case !sameTarget(old, j):
			return "", &job_entity.FieldError{Field: "db_password", Message: "数据库地址或用户已改动，请重新填写密码"}, nil
		}
		return old.DBPassword, nil, nil
	}
	sec := secret.Default()
	if sec == nil {
		return "", nil, errNoSecret
	}
	cipher, err := sec.Encrypt(input)
	return cipher, nil, err
}

func sameTarget(a, b *job_entity.Job) bool {
	return a.SourceType == b.SourceType && a.ConnMode == b.ConnMode && a.ServerID == b.ServerID &&
		a.DBHost == b.DBHost && a.DBPort == b.DBPort && a.DBUser == b.DBUser
}

// plainPassword 解密已加密的数据库密码
func plainPassword(cipher string) (string, error) {
	if cipher == "" {
		return "", nil
	}
	sec := secret.Default()
	if sec == nil {
		return "", errNoSecret
	}
	return sec.Decrypt(cipher)
}

// checkResources 检查引用的服务器和存储位置：服务器存在，存储位置存在且已挂载。
// 前面已经报过同一字段的错误时不再重复。
// save 为 true 时连同文件任务的目录可读性一起校验（只在保存时）。
func (s *jobSvc) checkResources(ctx context.Context, j *job_entity.Job, prior job_entity.FieldErrors, save bool) (job_entity.FieldErrors, error) {
	var errs job_entity.FieldErrors
	if j.ServerID > 0 && !prior.Has("server_id") {
		srv, err := server_repo.Server().Find(ctx, j.ServerID)
		if err != nil {
			return nil, err
		}
		if srv == nil {
			errs = append(errs, job_entity.FieldError{Field: "server_id", Message: "服务器不存在"})
		}
	}
	// 文件任务：确认每条路径存在且是目录。服务器连不上时放行，不因此挡住保存。
	// 只在保存时做，不在 prepareConn（测试连接/读库列表/浏览）里做：
	// 目录可读是保存的不变量，测试连接另有独立的检查项，浏览本身还要靠这条路径进去。
	if save && job_entity.IsFiles(j.SourceType) && j.ServerID > 0 && !prior.Has("server_id") && !prior.Has("paths") {
		errs = append(errs, s.checkPaths(ctx, j.ServerID, j.Paths)...)
	}
	if j.RepoID > 0 && !prior.Has("repo_id") {
		msg, err := repoProblem(ctx, j.RepoID)
		if err != nil {
			return nil, err
		}
		if msg != "" {
			errs = append(errs, job_entity.FieldError{Field: "repo_id", Message: msg})
		}
	}
	return errs, nil
}

// repoProblem 存储位置不能用的原因；可用返回空串
func repoProblem(ctx context.Context, repoID int64) (string, error) {
	r, err := repo_repo.Repo().Find(ctx, repoID)
	if err != nil {
		return "", err
	}
	if r == nil {
		return "存储位置不存在", nil
	}
	if err := repo_svc.Preflight(ctx, repoID); err != nil {
		if errors.Is(err, repo_svc.ErrNotMounted) {
			return err.Error(), nil
		}
		return "", err
	}
	return "", nil
}
