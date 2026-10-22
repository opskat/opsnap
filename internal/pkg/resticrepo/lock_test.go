package resticrepo

import "testing"

func TestLocked(t *testing.T) {
	cases := []struct {
		name string
		code int
		out  string
		want bool
	}{
		{"退出码 11", 11, `{"message_type":"exit_error","code":11}`, true},
		{"输出说明已被锁", 1, `repository is already locked by PID 19886`, true},
		{"大小写不敏感", 1, `Repository Is ALREADY LOCKED`, true},
		{"其他失败", 1, `wrong password`, false},
		{"成功", 0, ``, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := Locked(c.code, c.out); got != c.want {
				t.Fatalf("Locked(%d, %q) = %v, want %v", c.code, c.out, got, c.want)
			}
		})
	}
}
