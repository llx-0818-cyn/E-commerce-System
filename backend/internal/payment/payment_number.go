package payment

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"time"
)

type PaymentNumberGenerator interface {
	Generate() (string, error)
}

type defaultPaymentNumberGenerator struct {
	now     func() time.Time
	entropy io.Reader
}

func NewPaymentNumberGenerator() PaymentNumberGenerator {
	return &defaultPaymentNumberGenerator{now: time.Now, entropy: rand.Reader}
}

func (g *defaultPaymentNumberGenerator) Generate() (string, error) {
	random := make([]byte, 8)
	if _, err := io.ReadFull(g.entropy, random); err != nil {
		return "", fmt.Errorf("generate payment number entropy: %w", err)
	}
	stamp := g.now().UTC().Format("20060102150405.000")
	return "PAY-" + stamp[:14] + stamp[15:] + "-" + hex.EncodeToString(random), nil
}
