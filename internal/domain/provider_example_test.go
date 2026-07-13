package domain

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProviderExampleJSONHasNoCredentialFields(t *testing.T) {
	value := ProviderExample{
		ID: "example", Name: "Example", CLI: CLICodex,
		BaseURL: "https://example.test/v1", Model: "gpt-test", Provider: "custom",
	}
	b, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	encoded := strings.ToLower(string(b))
	for _, forbidden := range []string{"apikey", "api_key", "secret", "token", "auth", "webhook"} {
		if strings.Contains(encoded, forbidden) {
			t.Fatalf("provider example JSON contains forbidden field %q: %s", forbidden, encoded)
		}
	}
}
