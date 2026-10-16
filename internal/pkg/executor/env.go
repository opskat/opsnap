package executor

import "os"

// passEnv 允许传给子进程的本进程环境变量；其余（包括主密钥 OPSNAP_SECRET_KEY）一律不传
var passEnv = []string{"PATH", "HOME", "USER", "LANG", "LC_ALL", "TZ", "TMPDIR", "XDG_CACHE_HOME", "RESTIC_CACHE_DIR", "SSL_CERT_FILE", "SSL_CERT_DIR"}

// baseEnv 从本进程环境变量里挑出允许传递的部分
func baseEnv() []string {
	var env []string
	for _, k := range passEnv {
		if v, ok := os.LookupEnv(k); ok {
			env = append(env, k+"="+v)
		}
	}
	return env
}
