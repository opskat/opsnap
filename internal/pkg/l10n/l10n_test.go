package l10n

import (
	"context"
	"errors"
	"testing"

	"github.com/cago-frame/cago/pkg/i18n"
	"github.com/stretchr/testify/assert"

	"github.com/opskat/opsnap/internal/pkg/code"
)

func TestErrorLocalizesAndWraps(t *testing.T) {
	en := i18n.WithLanguage(context.Background(), code.LangEn)
	raw := errors.New("ERROR:  permission denied for table secret")
	sentinel := Errorf(code.DumpErrPrivilege)
	err := Errorf(code.WrapFullColon, sentinel, raw)

	assert.Equal(t, "数据源账号缺少权限：ERROR:  permission denied for table secret", err.Error(), "Error() 为中文")
	assert.Equal(t, "The data source account lacks privileges: ERROR:  permission denied for table secret", Text(en, err),
		"参数中的文案随语言变化，原文不变")
	assert.ErrorIs(t, err, sentinel)
	assert.ErrorIs(t, err, raw)
	assert.Equal(t, raw.Error(), Text(en, raw), "不是本包的错误原样显示")
	assert.Equal(t, "a, b", Join([]string{"a", "b"}, code.ListSep).Localize(en))
	assert.Equal(t, "a、b", Join([]string{"a", "b"}, code.ListSep).Localize(context.Background()))
}
