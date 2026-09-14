package order

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"time"
)

type OrderNumberGenerator interface {
	Generate() (string, error)
}

type defaultOrderNumberGenerator struct {
	now     func() time.Time
	entropy io.Reader
}

func NewOrderNumberGenerator() OrderNumberGenerator {
	return &defaultOrderNumberGenerator{now: time.Now, entropy: rand.Reader}
}

func (g *defaultOrderNumberGenerator) Generate() (string, error) {
	random := make([]byte, 8)
	if _, err := io.ReadFull(g.entropy, random); err != nil {
		return "", fmt.Errorf("generate order number entropy: %w", err)
	}
	stamp := g.now().UTC().Format("20060102150405.000")
	return "ORD-" + stamp[:14] + stamp[15:] + "-" + hex.EncodeToString(random), nil
}
