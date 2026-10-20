package migrations

import (
	"github.com/go-gormigrate/gormigrate/v2"
	"gorm.io/gorm"
)

// T202610180001 基线迁移：把 v0.1 的 202610150001~202610150010 与 202610160001
// 共 11 个迁移收敛成单一基线，一次性建出全部表结构。其中 202610150009 给 notify
// 补的 panel_url 列直接并入 notify 的建表语句。
// 合并依据：v0.1.0 发布后尚未有任何实例跑过旧迁移链（无部署、无存量库），
// 因此不存在需要沿用的历史迁移记录，新库直接以本基线为准。
// 注意：后续新增迁移时在 migrations.go 的列表末尾登记，不要拆散本基线。
func T202610180001() *gormigrate.Migration {
	return &gormigrate.Migration{
		ID: "202610180001",
		Migrate: func(tx *gorm.DB) error {
			stmts := []string{
				// ---- meta：实例级元信息（原 202610150001）----
				`CREATE TABLE IF NOT EXISTS meta (
					key        TEXT    NOT NULL PRIMARY KEY,
					value      TEXT    NOT NULL DEFAULT '',
					updatetime INTEGER NOT NULL DEFAULT 0
				)`,

				// ---- 认证与设置：单管理员、登录会话、API 密钥、全局设置（原 202610150002）----
				// admin 与 settings 都是单行表，用 CHECK (id = 1) 保证只有一行。
				// 会话令牌与 API 密钥只存哈希。
				`CREATE TABLE IF NOT EXISTS admin (
					id            INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
					username      TEXT    NOT NULL,
					password_hash TEXT    NOT NULL,
					failed_count  INTEGER NOT NULL DEFAULT 0,
					locked_until  INTEGER NOT NULL DEFAULT 0,
					createtime    INTEGER NOT NULL DEFAULT 0,
					updatetime    INTEGER NOT NULL DEFAULT 0
				)`,
				`CREATE TABLE IF NOT EXISTS sessions (
					id         INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
					token_hash TEXT    NOT NULL,
					expires_at INTEGER NOT NULL,
					createtime INTEGER NOT NULL DEFAULT 0
				)`,
				`CREATE UNIQUE INDEX IF NOT EXISTS uk_sessions_token_hash ON sessions (token_hash)`,
				`CREATE TABLE IF NOT EXISTS api_keys (
					id           INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
					name         TEXT    NOT NULL,
					prefix       TEXT    NOT NULL,
					key_hash     TEXT    NOT NULL,
					expires_at   INTEGER NOT NULL DEFAULT 0,
					last_used_at INTEGER NOT NULL DEFAULT 0,
					revoked_at   INTEGER NOT NULL DEFAULT 0,
					createtime   INTEGER NOT NULL DEFAULT 0
				)`,
				`CREATE UNIQUE INDEX IF NOT EXISTS uk_api_keys_key_hash ON api_keys (key_hash)`,
				`CREATE TABLE IF NOT EXISTS settings (
					id                        INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
					max_concurrent_runs       INTEGER NOT NULL,
					retention_days            INTEGER NOT NULL,
					timezone                  TEXT    NOT NULL,
					clear_mirror_after_backup INTEGER NOT NULL DEFAULT 0,
					updatetime                INTEGER NOT NULL DEFAULT 0
				)`,

				// ---- 通知设置：单行表（原 202610150003，并入 202610150009 的 panel_url）----
				// headers 是 JSON 对象，值为主密钥加密后的密文。
				`CREATE TABLE IF NOT EXISTS notify (
					id              INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
					url             TEXT    NOT NULL,
					headers         TEXT    NOT NULL,
					on_failed       INTEGER NOT NULL,
					on_recovered    INTEGER NOT NULL,
					on_stale        INTEGER NOT NULL,
					on_success      INTEGER NOT NULL,
					stale_hours     INTEGER NOT NULL,
					updatetime      INTEGER NOT NULL DEFAULT 0,
					panel_url       TEXT    NOT NULL DEFAULT ''
				)`,

				// ---- 密钥库：服务器登录用的密码、私钥（共享或服务器私有）----
				// password、private_key、passphrase 都是主密钥加密后的密文，没有就是空串。
				// shared = 1 能被多台服务器选用、出现在「密钥」页；shared = 0 只属于一台服务器，
				// 名称留空。共享密钥的名称唯一由部分索引保证。
				`CREATE TABLE IF NOT EXISTS credentials (
					id           INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
					name         TEXT    NOT NULL DEFAULT '',
					kind         TEXT    NOT NULL,
					password     TEXT    NOT NULL DEFAULT '',
					private_key  TEXT    NOT NULL DEFAULT '',
					passphrase   TEXT    NOT NULL DEFAULT '',
					username     TEXT    NOT NULL DEFAULT '',
					note         TEXT    NOT NULL DEFAULT '',
					shared       INTEGER NOT NULL DEFAULT 0,
					createtime   INTEGER NOT NULL DEFAULT 0,
					updatetime   INTEGER NOT NULL DEFAULT 0
				)`,
				`CREATE UNIQUE INDEX IF NOT EXISTS uk_credentials_shared_name ON credentials (name) WHERE shared = 1`,

				// ---- 服务器与代理链（原 202610150004）----
				// 登录凭据只在 credentials 里存一份，服务器引用它的 credential_id；
				// hops 里各代理的 password 仍是主密钥加密后的密文，按连接顺序排列成 JSON 数组。
				`CREATE TABLE IF NOT EXISTS servers (
					id                   INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
					name                 TEXT    NOT NULL,
					host                 TEXT    NOT NULL,
					port                 INTEGER NOT NULL,
					username             TEXT    NOT NULL,
					credential_id        INTEGER NOT NULL REFERENCES credentials (id),
					hops                 TEXT    NOT NULL DEFAULT '[]',
					host_key_fingerprint TEXT    NOT NULL,
					last_check_at        INTEGER NOT NULL DEFAULT 0,
					last_latency_ms      INTEGER NOT NULL DEFAULT 0,
					fail_since           INTEGER NOT NULL DEFAULT 0,
					last_error           TEXT    NOT NULL DEFAULT '',
					createtime           INTEGER NOT NULL DEFAULT 0,
					updatetime           INTEGER NOT NULL DEFAULT 0
				)`,

				// ---- 存储位置 restic 仓库（原 202610150005）----
				// credential、private_key、opsnap_key 都是主密钥加密后的密文；恢复密钥不保存，只记它的 restic 密钥 ID。
				`CREATE TABLE IF NOT EXISTS repos (
					id                      INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
					name                    TEXT    NOT NULL,
					type                    TEXT    NOT NULL,
					params                  TEXT    NOT NULL DEFAULT '{}',
					credential              TEXT    NOT NULL DEFAULT '',
					private_key             TEXT    NOT NULL DEFAULT '',
					opsnap_key              TEXT    NOT NULL,
					opsnap_key_id           TEXT    NOT NULL DEFAULT '',
					opsnap_key_created_at   INTEGER NOT NULL DEFAULT 0,
					recovery_key_id         TEXT    NOT NULL DEFAULT '',
					recovery_key_created_at INTEGER NOT NULL DEFAULT 0,
					status                  TEXT    NOT NULL DEFAULT 'unknown',
					status_message          TEXT    NOT NULL DEFAULT '',
					status_at               INTEGER NOT NULL DEFAULT 0,
					stored_size             INTEGER NOT NULL DEFAULT 0,
					raw_size                INTEGER NOT NULL DEFAULT 0,
					snapshot_count          INTEGER NOT NULL DEFAULT 0,
					free_space              INTEGER NOT NULL DEFAULT -1,
					key_count               INTEGER NOT NULL DEFAULT 0,
					stats_at                INTEGER NOT NULL DEFAULT 0,
					last_check_at           INTEGER NOT NULL DEFAULT 0,
					last_check_ok           INTEGER NOT NULL DEFAULT 0,
					last_check_message      TEXT    NOT NULL DEFAULT '',
					last_prune_at           INTEGER NOT NULL DEFAULT 0,
					last_prune_ok           INTEGER NOT NULL DEFAULT 0,
					last_prune_message      TEXT    NOT NULL DEFAULT '',
					createtime              INTEGER NOT NULL DEFAULT 0,
					updatetime              INTEGER NOT NULL DEFAULT 0
				)`,

				// ---- 备份任务（原 202610150006）----
				// db_password 是主密钥加密后的密文；databases、paths、excludes、extra_args 是 JSON 数组。
				// 删除任务只删这一行，不碰已有快照，所以 repo_id、server_id 不设外键。
				`CREATE TABLE IF NOT EXISTS jobs (
					id               INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
					name             TEXT    NOT NULL,
					source_type      TEXT    NOT NULL,
					server_id        INTEGER NOT NULL DEFAULT 0,
					conn_mode        TEXT    NOT NULL DEFAULT '',
					db_host          TEXT    NOT NULL DEFAULT '',
					db_port          INTEGER NOT NULL DEFAULT 0,
					db_user          TEXT    NOT NULL DEFAULT '',
					db_password      TEXT    NOT NULL DEFAULT '',
					databases        TEXT    NOT NULL DEFAULT '[]',
					all_databases    INTEGER NOT NULL DEFAULT 0,
					auth_db          TEXT    NOT NULL DEFAULT '',
					paths            TEXT    NOT NULL DEFAULT '[]',
					excludes         TEXT    NOT NULL DEFAULT '[]',
					repo_id          INTEGER NOT NULL,
					schedule_kind    TEXT    NOT NULL,
					schedule_time    TEXT    NOT NULL DEFAULT '',
					schedule_weekday INTEGER NOT NULL DEFAULT 0,
					schedule_cron    TEXT    NOT NULL DEFAULT '',
					retention_preset TEXT    NOT NULL,
					retention_days   INTEGER NOT NULL DEFAULT 0,
					retention_weeks  INTEGER NOT NULL DEFAULT 0,
					retention_months INTEGER NOT NULL DEFAULT 0,
					timeout_sec      INTEGER NOT NULL DEFAULT 7200,
					bandwidth_kbps   INTEGER NOT NULL DEFAULT 0,
					compression      TEXT    NOT NULL DEFAULT 'auto',
					extra_args       TEXT    NOT NULL DEFAULT '[]',
					paused           INTEGER NOT NULL DEFAULT 0,
					createtime       INTEGER NOT NULL DEFAULT 0,
					updatetime       INTEGER NOT NULL DEFAULT 0
				)`,
				`CREATE INDEX IF NOT EXISTS idx_jobs_server_id ON jobs (server_id)`,
				`CREATE INDEX IF NOT EXISTS idx_jobs_repo_id ON jobs (repo_id)`,

				// ---- 运行记录（原 202610150007）----
				// snapshot_ids、steps、log 是 JSON；日志写入前已遮盖凭据。job_id 不设外键。
				`CREATE TABLE IF NOT EXISTS runs (
					id              INTEGER NOT NULL PRIMARY KEY AUTOINCREMENT,
					job_id          INTEGER NOT NULL,
					job_name        TEXT    NOT NULL DEFAULT '',
					triggered_by    TEXT    NOT NULL,
					status          TEXT    NOT NULL,
					reason          TEXT    NOT NULL DEFAULT '',
					notify_failed   INTEGER NOT NULL DEFAULT 0,
					started_at      INTEGER NOT NULL,
					finished_at     INTEGER NOT NULL DEFAULT 0,
					duration_ms     INTEGER NOT NULL DEFAULT 0,
					bytes_processed INTEGER NOT NULL DEFAULT 0,
					bytes_added     INTEGER NOT NULL DEFAULT 0,
					snapshot_ids    TEXT    NOT NULL DEFAULT '[]',
					steps           TEXT    NOT NULL DEFAULT '[]',
					log             TEXT    NOT NULL DEFAULT '[]',
					createtime      INTEGER NOT NULL DEFAULT 0
				)`,
				`CREATE INDEX IF NOT EXISTS idx_runs_job_id ON runs (job_id, id)`,
				`CREATE INDEX IF NOT EXISTS idx_runs_status ON runs (status)`,

				// ---- 「太久没有成功」的告警状态（原 202610150008）----
				// 每个任务至多一行，重启后据此不重发告警；job_id 不设外键。
				`CREATE TABLE IF NOT EXISTS stale_alerts (
					job_id     INTEGER NOT NULL PRIMARY KEY,
					since      INTEGER NOT NULL,
					createtime INTEGER NOT NULL DEFAULT 0
				)`,

				// ---- 主密钥校验值（原 202610150010）----
				// check_value 是用主密钥加密的固定内容，启动时据此判断 OPSNAP_SECRET_KEY 是否匹配。
				`CREATE TABLE IF NOT EXISTS master_key (
					id          INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
					check_value TEXT    NOT NULL,
					rotated_at  INTEGER NOT NULL DEFAULT 0,
					createtime  INTEGER NOT NULL DEFAULT 0
				)`,

				// ---- 单点登录 OIDC（原 202610160001）----
				// 单行表，保存提供方配置、绑定的提供方账号和「允许密码登录」开关。
				// client_secret 用主密钥加密；没有这一行时等同未配置、允许密码登录。
				`CREATE TABLE IF NOT EXISTS sso (
					id             INTEGER NOT NULL PRIMARY KEY CHECK (id = 1),
					issuer         TEXT    NOT NULL DEFAULT '',
					client_id      TEXT    NOT NULL DEFAULT '',
					client_secret  TEXT    NOT NULL DEFAULT '',
					button_label   TEXT    NOT NULL DEFAULT '',
					panel_url      TEXT    NOT NULL DEFAULT '',
					bound_issuer   TEXT    NOT NULL DEFAULT '',
					bound_sub      TEXT    NOT NULL DEFAULT '',
					bound_name     TEXT    NOT NULL DEFAULT '',
					bound_at       INTEGER NOT NULL DEFAULT 0,
					password_login INTEGER NOT NULL DEFAULT 1,
					updatetime     INTEGER NOT NULL DEFAULT 0
				)`,
			}
			for _, s := range stmts {
				if err := tx.Exec(s).Error; err != nil {
					return err
				}
			}
			return nil
		},
		Rollback: func(tx *gorm.DB) error {
			// 先删引用 credentials 的 servers，再删 credentials
			for _, t := range []string{"sso", "master_key", "stale_alerts", "runs", "jobs", "repos", "servers", "credentials", "notify", "settings", "api_keys", "sessions", "admin", "meta"} {
				if err := tx.Exec(`DROP TABLE IF EXISTS ` + t).Error; err != nil {
					return err
				}
			}
			return nil
		},
	}
}
