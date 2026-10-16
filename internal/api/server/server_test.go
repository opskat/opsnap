package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestInput(t *testing.T) {
	hops := []HopInput{{Type: HopSOCKS5, Host: "proxy", Port: 1080, Username: "u", Password: "p"}}
	want := &ServerInput{
		Name: "web", Host: "10.0.0.1", Port: 22, Username: "root", AuthType: AuthPrivateKey,
		Password: "pw", PrivateKey: "key", Passphrase: "pass", Hops: hops, HostKeyFingerprint: "SHA256:x",
	}
	assert.Equal(t, want, NewCreateRequest(want).Input())
	assert.Equal(t, want, NewUpdateRequest(3, want).Input())
	assert.Equal(t, int64(3), NewUpdateRequest(3, want).ID)
	assert.Equal(t, want, NewTestRequest(4, want).Input())
	assert.Equal(t, int64(4), NewTestRequest(4, want).ID)
}
