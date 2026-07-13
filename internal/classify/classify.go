package classify

import (
	"regexp"
	"strings"

	"ai-watch/internal/domain"
)

var patterns = map[domain.CLI]struct{ fatal, overload *regexp.Regexp }{
	domain.CLICodex: {
		fatal:    regexp.MustCompile(`(?i)not logged in|unauthorized|authentication|auth failed|api key.*missing|请.*登录|未登录|认证失败|unexpected argument|unknown option|invalid option|usage: codex exec|failed to initialize in-process app-server client`),
		overload: regexp.MustCompile(`(?i)high demand|temporary errors|server[_ -]?overloaded|overloaded|rate[_ -]?limit|too many requests|429|529|503|service unavailable|负载.*上限|请稍后重试|当前模型.*负载|请求过多|频率限制`),
	},
	domain.CLIClaude: {
		fatal:    regexp.MustCompile(`(?i)not logged in|login required|please log in|unauthorized|authentication|auth failed|invalid api key|api key.*missing|credit balance|billing|payment required|workspace.*trust|unexpected argument|unknown option|invalid option|usage: claude|请.*登录|未登录|认证失败|无效.*密钥|余额不足|欠费`),
		overload: regexp.MustCompile(`(?i)high demand|temporar(y|ily).*unavailable|temporary errors|overloaded|server[_ -]?overloaded|rate[_ -]?limit|too many requests|429|529|capacity|usage limit|limit reached|try again later|try again in|请稍后重试|当前.*负载|负载.*上限|请求过多|频率限制|额度.*限制`),
	},
}

func Result(cli domain.CLI, exitCode int, output, expected string, timedOut, stopped bool) domain.AttemptStatus {
	if stopped {
		return domain.AttemptStopped
	}
	if timedOut {
		return domain.AttemptTimeout
	}
	p := patterns[cli]
	if p.fatal != nil && p.fatal.MatchString(output) {
		return domain.AttemptFatal
	}
	if p.overload != nil && p.overload.MatchString(output) {
		return domain.AttemptOverloaded
	}
	if exitCode == 0 && strings.Contains(output, expected) {
		return domain.AttemptSuccess
	}
	return domain.AttemptUnmatched
}
