package interaction

import "testing"

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

func TestParseAllowsOneExplicitRunTargetForTaskCommands(t *testing.T) {
	target := "109870c4-f3e3-4a38-9dcd-0a30c4e72cd9"
	for _, kind := range []Kind{Status, Cancel, Retry} {
		command, mentioned, err := Parse("@openreview " + string(kind) + " " + target)
		if err != nil || !mentioned || command.Kind != kind || command.Target != target || command.Normalized != "@openreview "+string(kind)+" "+target {
			t.Fatalf("unexpected %s command: %#v mentioned=%v err=%v", kind, command, mentioned, err)
		}
	}
	if _, mentioned, err := Parse("@openreview status one two"); !mentioned || err == nil {
		t.Fatal("multiple run ids must be rejected")
	}
}
