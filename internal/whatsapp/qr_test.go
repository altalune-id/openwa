package whatsapp

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRenderQR_IsAPNG(t *testing.T) {
	t.Parallel()
	png, err := renderQR("2@abc,def,ghi")
	require.NoError(t, err)
	require.True(t, bytes.HasPrefix(png, []byte{0x89, 'P', 'N', 'G'}))
}
