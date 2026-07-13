package security

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	secret := "sk-supersecret1234"
	got := Redact("Authorization: Bearer "+secret+" api_key="+secret, secret)
	if strings.Contains(got, secret) {
		t.Fatal("secret leaked")
	}
}
func TestMask(t *testing.T) {
	if got := Mask("123456789012"); got == "123456789012" || !strings.HasPrefix(got, "1234") {
		t.Fatalf("bad mask %q", got)
	}
}
