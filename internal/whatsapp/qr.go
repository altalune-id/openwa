package whatsapp

import (
	"fmt"

	"github.com/skip2/go-qrcode"
)

const qrSizePx = 256

func renderQR(code string) ([]byte, error) {
	png, err := qrcode.Encode(code, qrcode.Medium, qrSizePx)
	if err != nil {
		return nil, fmt.Errorf("whatsapp: render qr: %w", err)
	}
	return png, nil
}
