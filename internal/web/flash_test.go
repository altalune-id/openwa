package web_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"altalune.id/openwa/internal/web"
)

func TestFlash_EncodeDecodeRoundTrip(t *testing.T) {
	t.Parallel()
	in := web.FlashPayload{Kind: web.FlashOK, Key: "flash.project_created", Args: []string{"Name", "Alpha"}}
	raw, err := web.EncodeFlash(in)
	require.NoError(t, err)
	assert.NotContains(t, raw, "|", "the payload must not collide with the SignCookie separator")
	out, err := web.DecodeFlash(raw)
	require.NoError(t, err)
	assert.Equal(t, in, out)
}

func TestSignedFlashCookie_CapsTheWholeCookie(t *testing.T) {
	t.Parallel()
	secret := []byte("0123456789abcdef0123456789abcdef")
	v, err := web.SignedFlashCookie(secret, web.FlashPayload{Kind: web.FlashOK, Key: "flash.project_created", Args: []string{"Name", "Alpha"}})
	require.NoError(t, err)
	raw, err := web.VerifyCookie(secret, v)
	require.NoError(t, err)
	p, err := web.DecodeFlash(raw)
	require.NoError(t, err)
	assert.Equal(t, "Alpha", p.Args[1])

	_, err = web.SignedFlashCookie(secret, web.FlashPayload{Kind: web.FlashOK, Key: "k", Args: []string{"Name", strings.Repeat("x", 5000)}})
	require.Error(t, err)
	assert.True(t, web.IsFlashTooLargeError(err))

	for n := 2900; n < 3100; n++ {
		v, err := web.SignedFlashCookie(secret, web.FlashPayload{Kind: web.FlashOK, Key: "k", Args: []string{"N", strings.Repeat("x", n)}})
		if err != nil {
			require.True(t, web.IsFlashTooLargeError(err))
			continue
		}
		require.LessOrEqual(t, len(web.FlashCookieName)+1+len(v), web.FlashMaxBytes, "n=%d", n)
	}
}

func TestFlash_GarbageIsRejected(t *testing.T) {
	t.Parallel()
	_, err := web.DecodeFlash("not base64 %%")
	require.Error(t, err)
}
