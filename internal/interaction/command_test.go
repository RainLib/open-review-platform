package interaction

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestParseRequiresExplicitMention(t *testing.T) {
	if _, mentioned, err := Parse("please @openreview review this"); err != nil || mentioned {
		t.Fatalf("non-leading mention must be ignored: mentioned=%v err=%v", mentioned, err)
	}
	if _, mentioned, err := Parse("@openreview review --mode=deep"); err != nil || !mentioned {
		t.Fatalf("expected command to be parsed: mentioned=%v err=%v", mentioned, err)
	}
}

func TestParseNormalizesAndRejectsAmbiguity(t *testing.T) {
	command, mentioned, err := Parse("  @OpenReview REVIEW --mode=SECURITY ")
	if err != nil || !mentioned || command.Kind != Review || command.Mode != "security" || command.Normalized != "@openreview review --mode=security" {
		t.Fatalf("unexpected command: %#v mentioned=%v err=%v", command, mentioned, err)
	}
	if _, mentioned, err := Parse("@openreview review --mode=deep --mode=security"); !mentioned || err == nil {
		t.Fatal("duplicate mode must be rejected")
	}
	if command, mentioned, err := Parse("@openreview"); err != nil || !mentioned || command.Kind != Help {
		t.Fatalf("bare mention should request help: %#v %v %v", command, mentioned, err)
	}
}

func TestParseReviewRuleSetSelectionIsUUIDAndOrderIndependent(t *testing.T) {
	ruleSetID := uuid.New()
	command, mentioned, err := Parse("@openreview review --rule=" + ruleSetID.String() + " --mode=security")
	if err != nil || !mentioned || command.Mode != "security" || command.RuleSetID != ruleSetID.String() || command.Normalized != "@openreview review --rule="+ruleSetID.String()+" --mode=security" {
		t.Fatalf("unexpected selected-rule command: %#v mentioned=%v err=%v", command, mentioned, err)
	}
	if _, mentioned, err := Parse("@openreview review --rule=not-a-uuid"); !mentioned || err == nil {
		t.Fatal("invalid rule set id must be rejected")
	}
	if _, mentioned, err := Parse("@openreview review --rule=" + ruleSetID.String() + " --rule=" + uuid.New().String()); !mentioned || err == nil {
		t.Fatal("duplicate rule selection must be rejected")
	}
}

func TestParseReviewForceRequestsAnExplicitRerun(t *testing.T) {
	command, mentioned, err := Parse("@openreview review --force --mode=security")
	if err != nil || !mentioned || command.Kind != Review || command.Mode != "security" || command.Normalized != "@openreview review --force --mode=security" {
		t.Fatalf("unexpected force command: %#v mentioned=%v err=%v", command, mentioned, err)
	}
	if _, mentioned, err := Parse("@openreview review --force --force"); !mentioned || err == nil {
		t.Fatal("duplicate force flag must be rejected")
	}
}

func TestParseAllowsOneExplicitRunTargetForTaskCommands(t *testing.T) {
	target := "109870c4-f3e3-4a38-9dcd-0a30c4e72cd9"
	for _, kind := range []Kind{Status, Cancel, Stop, Retry, Explain} {
		command, mentioned, err := Parse("@openreview " + string(kind) + " " + target)
		if err != nil || !mentioned || command.Kind != kind || command.Target != target || command.Normalized != "@openreview "+string(kind)+" "+target {
			t.Fatalf("unexpected %s command: %#v mentioned=%v err=%v", kind, command, mentioned, err)
		}
	}
	if _, mentioned, err := Parse("@openreview explain"); !mentioned || err == nil {
		t.Fatal("explain without a finding id must be rejected")
	}
	if _, mentioned, err := Parse("@openreview status one two"); !mentioned || err == nil {
		t.Fatal("multiple run ids must be rejected")
	}
}

func TestParseImplementIsExplicitAndHasNoShellArguments(t *testing.T) {
	command, mentioned, err := Parse("@openreview implement")
	if err != nil || !mentioned || command.Kind != Implement || command.Normalized != "@openreview implement" {
		t.Fatalf("unexpected implement command: %#v mentioned=%v err=%v", command, mentioned, err)
	}
	if _, mentioned, err := Parse("@openreview implement --dangerous"); !mentioned || err == nil {
		t.Fatal("implement must never accept arbitrary agent or shell arguments")
	}
}

func TestParseApproveRequiresOneFullPlanDigest(t *testing.T) {
	digest := strings.Repeat("A3", 32)
	command, mentioned, err := Parse("@OpenReview APPROVE " + digest)
	if err != nil || !mentioned || command.Kind != Approve || command.Target != strings.ToLower(digest) || command.Normalized != "@openreview approve "+strings.ToLower(digest) {
		t.Fatalf("unexpected approve command: %#v mentioned=%v err=%v", command, mentioned, err)
	}
	for _, body := range []string{
		"@openreview approve",
		"@openreview approve abc1234",
		"@openreview approve " + strings.Repeat("x", 64),
		"@openreview approve " + digest + " --force",
	} {
		if _, mentioned, err := Parse(body); !mentioned || err == nil {
			t.Fatalf("malformed approval must be rejected: %q mentioned=%v err=%v", body, mentioned, err)
		}
	}
}

func TestParseReviseKeepsFeedbackAsDataNotFlags(t *testing.T) {
	command, mentioned, err := Parse("@openreview revise Handle the missing nil response and add a regression test.")
	if err != nil || !mentioned || command.Kind != Revise || command.Instruction != "Handle the missing nil response and add a regression test." || command.Normalized != "@openreview revise <instruction>" {
		t.Fatalf("unexpected revise command: %#v mentioned=%v err=%v", command, mentioned, err)
	}
	if _, mentioned, err := Parse("@openreview revise short"); !mentioned || err == nil {
		t.Fatal("short feedback must not become a coding instruction")
	}
}
