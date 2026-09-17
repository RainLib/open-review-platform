package interaction

import (
	"fmt"
	"strings"
)

type Kind string

const (
	Review Kind = "review"
	Status Kind = "status"
	Cancel Kind = "cancel"
	Retry  Kind = "retry"
	Help   Kind = "help"
)

type Command struct {
	Kind       Kind
	Mode       string
	Normalized string
}

// Parse accepts only an explicit leading bot mention. Natural-language
// comments and quoted examples must never accidentally start review work.
func Parse(body string) (Command, bool, error) {
	fields := strings.Fields(strings.TrimSpace(body))
	if len(fields) == 0 || !strings.EqualFold(fields[0], "@openreview") {
		return Command{}, false, nil
	}
	if len(fields) == 1 {
		return Command{Kind: Help, Normalized: "@openreview help"}, true, nil
	}
	kind := Kind(strings.ToLower(fields[1]))
	switch kind {
	case Review, Status, Cancel, Retry, Help:
	default:
		return Command{}, true, fmt.Errorf("unknown Open Review command %q", fields[1])
	}
	command := Command{Kind: kind, Normalized: "@openreview " + string(kind)}
	if kind != Review && len(fields) > 2 {
		return Command{}, true, fmt.Errorf("%s does not accept arguments", kind)
	}
	if kind == Review {
		for _, argument := range fields[2:] {
			if !strings.HasPrefix(argument, "--mode=") {
				return Command{}, true, fmt.Errorf("unknown review argument %q", argument)
			}
			mode := strings.ToLower(strings.TrimPrefix(argument, "--mode="))
			if mode != "standard" && mode != "deep" && mode != "security" {
				return Command{}, true, fmt.Errorf("unsupported review mode %q", mode)
			}
			if command.Mode != "" {
				return Command{}, true, fmt.Errorf("review mode can only be provided once")
			}
			command.Mode = mode
			command.Normalized += " --mode=" + mode
		}
	}
	return command, true, nil
}
