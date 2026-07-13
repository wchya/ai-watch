package security

import (
	"regexp"
	"strings"
)

var credentialPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(api[_ -]?key|auth[_ -]?token|authorization)(\s*[=:]\s*|\s+bearer\s+)[^\s"']+`),
	regexp.MustCompile(`(?i)bearer\s+[a-z0-9._~+/=-]{8,}`),
	regexp.MustCompile(`\b(sk-[A-Za-z0-9_-]{8,})\b`),
}

func Mask(secret string) string {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return ""
	}
	if len(secret) <= 8 {
		return strings.Repeat("*", len(secret))
	}
	return secret[:4] + strings.Repeat("*", min(12, len(secret)-8)) + secret[len(secret)-4:]
}

func Redact(text string, secrets ...string) string {
	for _, secret := range secrets {
		if secret != "" {
			text = strings.ReplaceAll(text, secret, "[REDACTED]")
		}
	}
	for _, p := range credentialPatterns {
		text = p.ReplaceAllStringFunc(text, func(s string) string {
			if i := strings.IndexAny(s, "=:"); i >= 0 {
				return s[:i+1] + "[REDACTED]"
			}
			parts := strings.Fields(s)
			if len(parts) > 0 {
				return parts[0] + " [REDACTED]"
			}
			return "[REDACTED]"
		})
	}
	return text
}
