package auth

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestActorContext(t *testing.T) {
	_, ok := ActorFrom(context.Background())
	assert.False(t, ok)

	ctx := WithActor(context.Background(), Actor{Kind: KindAPIKey, ID: 7, Name: "ci"})
	a, ok := ActorFrom(ctx)
	assert.True(t, ok)
	assert.Equal(t, Actor{Kind: KindAPIKey, ID: 7, Name: "ci"}, a)
	assert.Equal(t, "api_key:ci", a.String())
	assert.Equal(t, "admin", Actor{Kind: KindAdmin, Name: "admin"}.String())
}
