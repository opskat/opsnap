package l10n

import (
	"context"
	"errors"
	"fmt"
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

// 驱动等外层包装过的本包错误：外层原文不变，其中本包的文字按语言显示
func TestTextWrappedByOthers(t *testing.T) {
	en := i18n.WithLanguage(context.Background(), code.LangEn)
	inner := Errorf(code.NetDialFailed, "10.0.0.5:5432", errors.New("ssh: rejected: connect failed (Connection refused)"))
	err := fmt.Errorf("failed to connect to `user=backup database=app`: %w", inner)
	assert.Equal(t, "failed to connect to `user=backup database=app`: Failed to connect to 10.0.0.5:5432: "+
		"ssh: rejected: connect failed (Connection refused)", Text(en, err))
	assert.Equal(t, err.Error(), Text(context.Background(), err), "中文与原文一致")
}

// 业务层按错误码给出的接口错误：文案不带参数时按语言重新给出，带参数时原样显示
func TestTextHTTPError(t *testing.T) {
	en := i18n.WithLanguage(context.Background(), code.LangEn)
	zh := context.Background()
	assert.Equal(t, "Channel not found", Text(en, i18n.NewNotFoundError(zh, code.ChannelNotFound)))
	assert.Equal(t, "通道不存在", Text(zh, i18n.NewNotFoundError(en, code.ChannelNotFound)))
	withArgs := i18n.NewError(zh, code.ChannelChainTooLong, 7)
	assert.Equal(t, withArgs.Error(), Text(en, withArgs))
}
