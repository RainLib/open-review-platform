package agentadapter

import (
	"bytes"
	"encoding/json"
)

// A reservation is refunded only after a complete upstream message supplies
// valid cumulative usage. Missing, malformed or interrupted reports retain the
// ceiling. See https://platform.claude.com/docs/en/build-with-claude/streaming.
type anthropicUsage struct {
	streaming, invalid, started, stopped bool
	line, body                           []byte
	last                                 int64
	ceiling                              int64
}

func (usage *anthropicUsage) write(chunk []byte) {
	if usage.invalid {
		return
	}
	if !usage.streaming {
		usage.body = append(usage.body, chunk...)
		return
	}
	for _, b := range chunk {
		if b == '\n' {
			usage.readLine(bytes.TrimSpace(usage.line))
			usage.line = usage.line[:0]
		} else {
			usage.line = append(usage.line, b)
			if len(usage.line) > 64<<10 {
				usage.invalid = true
				usage.line = nil
				return
			}
		}
	}
}

func (usage *anthropicUsage) readLine(line []byte) {
	if !bytes.HasPrefix(line, []byte("data:")) {
		return
	}
	var event struct {
		Type  string `json:"type"`
		Usage struct {
			Output *int64 `json:"output_tokens"`
		} `json:"usage"`
		Message struct {
			Type string `json:"type"`
		} `json:"message"`
	}
	if json.Unmarshal(bytes.TrimSpace(line[5:]), &event) != nil {
		usage.invalid = true
		return
	}
	if usage.stopped {
		usage.invalid = true
		return
	}
	switch event.Type {
	case "message_start":
		if usage.started || event.Message.Type != "message" {
			usage.invalid = true
			return
		}
		usage.started = true
	case "message_delta":
		if !usage.started || event.Usage.Output == nil || *event.Usage.Output < usage.last || *event.Usage.Output > usage.ceiling {
			usage.invalid = true
			return
		}
		usage.last = *event.Usage.Output
	case "message_stop":
		usage.stopped = usage.started
	case "error":
		usage.invalid = true
	}
}

func (usage *anthropicUsage) refund() int64 {
	if usage.invalid {
		return 0
	}
	if usage.streaming {
		if len(usage.line) > 0 {
			usage.readLine(bytes.TrimSpace(usage.line))
		}
		if usage.invalid || !usage.stopped || usage.last <= 0 {
			return 0
		}
		return usage.ceiling - usage.last
	}
	var message struct {
		Type       string `json:"type"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			Output *int64 `json:"output_tokens"`
		} `json:"usage"`
	}
	if json.Unmarshal(usage.body, &message) != nil || message.Type != "message" || message.StopReason == "" || message.Usage.Output == nil || *message.Usage.Output <= 0 || *message.Usage.Output > usage.ceiling {
		return 0
	}
	return usage.ceiling - *message.Usage.Output
}
