package executor

import (
	"bytes"
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opsnap/internal/pkg/cmdbuild"
)

func TestExecRunSplit(t *testing.T) {
	var out, errOut bytes.Buffer
	code, err := New().RunSplit(context.Background(),
		cmdbuild.Cmd{Name: "sh", Args: []string{"-c", "printf data; printf oops >&2; exit 3"}}, &out, &errOut)
	require.NoError(t, err)
	assert.Equal(t, 3, code)
	assert.Equal(t, "data", out.String(), "标准输出不能混入标准错误")
	assert.Equal(t, "oops", errOut.String())
}

func TestFakeRunSplit(t *testing.T) {
	f := NewFake().On(Match("restic", "dump"), Step{Output: "body", Stderr: "Fatal: x", ExitCode: 1})
	var out, errOut bytes.Buffer
	code, err := f.RunSplit(context.Background(), cmdbuild.Cmd{Name: "restic", Args: []string{"dump"}}, &out, &errOut)
	require.NoError(t, err)
	assert.Equal(t, 1, code)
	assert.Equal(t, "body", out.String())
	assert.Equal(t, "Fatal: x", errOut.String())
	assert.Len(t, f.Calls(), 1)

	var merged bytes.Buffer
	_, _ = f.Run(context.Background(), cmdbuild.Cmd{Name: "restic", Args: []string{"dump"}}, &merged)
	assert.Equal(t, "bodyFatal: x", merged.String(), "合并输出时依次写入")
}
