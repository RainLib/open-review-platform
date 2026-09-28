package interaction

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

type Kind string

const (
	Review    Kind = "review"
	Status    Kind = "status"
	Cancel    Kind = "cancel"
	Stop      Kind = "stop"
	Retry     Kind = "retry"
	Explain   Kind = "explain"
	Implement Kind = "implement"
	Approve   Kind = "approve"
	Revise    Kind = "revise"
	Help      Kind = "help"
)

type Command struct {
	Kind      Kind
	Mode      string
	RuleSetID string
	// Target is a review-run/finding id for review commands, or the exact
	// 64-character plan digest for an Issue approval command.
	Target      string
	Instruction string
	Normalized  string
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
	case Review, Status, Cancel, Stop, Retry, Explain, Implement, Approve, Revise, Help:
	default:
		return Command{}, true, fmt.Errorf("unknown Open Review command %q", fields[1])
	}
	command := Command{Kind: kind, Normalized: "@openreview " + string(kind)}
	if kind == Review {
		forceRequested := false
		for _, argument := range fields[2:] {
			switch {
			case argument == "--force":
				// An explicit review command already creates a new run after a
				// terminal result. Keep the familiar Kody-style flag as an
				// intentional re-review request, without weakening admission,
				// authorization, repository scope, or merge-gate policy.
				if forceRequested {
					return Command{}, true, fmt.Errorf("force can only be provided once")
				}
				forceRequested = true
				command.Normalized += " --force"
			case strings.HasPrefix(argument, "--mode="):
				mode := strings.ToLower(strings.TrimPrefix(argument, "--mode="))
				if mode != "standard" && mode != "deep" && mode != "security" {
					return Command{}, true, fmt.Errorf("unsupported review mode %q", mode)
				}
				if command.Mode != "" {
					return Command{}, true, fmt.Errorf("review mode can only be provided once")
				}
				command.Mode = mode
				command.Normalized += " --mode=" + mode
			case strings.HasPrefix(argument, "--rule="):
				ruleSetID := strings.TrimSpace(strings.TrimPrefix(argument, "--rule="))
				parsed, err := uuid.Parse(ruleSetID)
				if err != nil || parsed == uuid.Nil {
					return Command{}, true, fmt.Errorf("rule set id must be a UUID")
				}
				if command.RuleSetID != "" {
					return Command{}, true, fmt.Errorf("rule set can only be provided once")
				}
				command.RuleSetID = parsed.String()
				command.Normalized += " --rule=" + command.RuleSetID
			default:
				return Command{}, true, fmt.Errorf("unknown review argument %q", argument)
			}
		}
	} else if kind == Implement {
		if len(fields) > 2 {
			return Command{}, true, fmt.Errorf("implement does not accept arguments")
		}
	} else if kind == Approve {
		if len(fields) != 3 || len(fields[2]) != 64 {
			return Command{}, true, fmt.Errorf("approve requires exactly one full 64-character plan SHA-256")
		}
		for _, character := range fields[2] {
			if (character < '0' || character > '9') && (character < 'a' || character > 'f') && (character < 'A' || character > 'F') {
				return Command{}, true, fmt.Errorf("approve requires a hexadecimal plan SHA-256")
			}
		}
		command.Target = strings.ToLower(fields[2])
		command.Normalized += " " + command.Target
	} else if kind == Revise {
		instruction := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(body), fields[0]+" "+fields[1]))
		if len(instruction) < 8 || len(instruction) > 4000 {
			return Command{}, true, fmt.Errorf("revise requires an instruction between 8 and 4000 characters")
		}
		command.Instruction = instruction
		command.Normalized += " <instruction>"
	} else if kind == Status || kind == Cancel || kind == Stop || kind == Retry || kind == Explain {
		if len(fields) > 3 {
			return Command{}, true, fmt.Errorf("%s accepts exactly one target id", kind)
		}
		if len(fields) != 3 && kind == Explain {
			return Command{}, true, fmt.Errorf("explain requires one finding id")
		}
		if len(fields) == 3 {
			command.Target = fields[2]
			command.Normalized += " " + command.Target
		}
	} else if len(fields) > 2 {
		return Command{}, true, fmt.Errorf("%s does not accept arguments", kind)
	}
	return command, true, nil
}
