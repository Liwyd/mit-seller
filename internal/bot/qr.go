package bot

import (
	"errors"

	qrcode "github.com/skip2/go-qrcode"
)

// qrPNG draws a 400px QR code without a quiet zone, low error correction,
// like the endroid/qr-code settings the PHP bot used.
func qrPNG(content string) ([]byte, error) {
	if content == "" {
		return nil, errors.New("empty QR content")
	}
	q, err := qrcode.New(content, qrcode.Low)
	if err != nil {
		// very long multi-config payloads may not fit at Low; nothing else to try
		return nil, err
	}
	q.DisableBorder = true
	return q.PNG(400)
}
