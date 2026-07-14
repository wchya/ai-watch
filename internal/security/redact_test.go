package security

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	secret := "sk-supersecret1234"
	got := Redact("Authorization: Bearer "+secret+" api_key="+secret+" access_token=oauth-value refresh_token=refresh-value AWS_SECRET_ACCESS_KEY=aws-value", secret)
	if strings.Contains(got, secret) {
		t.Fatal("secret leaked")
	}
	for _, value := range []string{"oauth-value", "refresh-value", "aws-value"} {
		if strings.Contains(got, value) {
			t.Fatalf("credential leaked: %s", got)
		}
	}
}
func TestMask(t *testing.T) {
	if got := Mask("123456789012"); got == "123456789012" || !strings.HasPrefix(got, "1234") {
		t.Fatalf("bad mask %q", got)
	}
}
