package bond_test

import (
	"testing"

	"github.com/nisimpson/bond"
)

func TestTextPrompt(t *testing.T) {
	msgs := bond.TextPrompt("hello")
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != bond.RoleUser {
		t.Errorf("expected RoleUser, got %v", msgs[0].Role)
	}
	if len(msgs[0].Content) != 1 {
		t.Fatalf("expected 1 block, got %d", len(msgs[0].Content))
	}
	tb := msgs[0].Content[0].(*bond.TextBlock)
	if tb.Text != "hello" {
		t.Errorf("expected 'hello', got %q", tb.Text)
	}
}

func TestImagePrompt(t *testing.T) {
	msgs := bond.ImagePrompt("describe this", "s3://img.png", "image/png")
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if len(msgs[0].Content) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(msgs[0].Content))
	}
	tb := msgs[0].Content[0].(*bond.TextBlock)
	if tb.Text != "describe this" {
		t.Errorf("expected text block, got %q", tb.Text)
	}
	mb := msgs[0].Content[1].(*bond.MediaBlock)
	if mb.SourceURI != "s3://img.png" {
		t.Errorf("expected source URI, got %q", mb.SourceURI)
	}
	if mb.MIMEType != "image/png" {
		t.Errorf("expected mime type, got %q", mb.MIMEType)
	}
}

func TestConversation(t *testing.T) {
	msgs := bond.Conversation("user1", "assistant1", "user2")
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(msgs))
	}
	if msgs[0].Role != bond.RoleUser {
		t.Errorf("msg[0] expected user, got %v", msgs[0].Role)
	}
	if msgs[1].Role != bond.RoleAssistant {
		t.Errorf("msg[1] expected assistant, got %v", msgs[1].Role)
	}
	if msgs[2].Role != bond.RoleUser {
		t.Errorf("msg[2] expected user, got %v", msgs[2].Role)
	}
}

func TestMultiBlockPrompt(t *testing.T) {
	msgs := bond.MultiBlockPrompt(
		&bond.TextBlock{Text: "a"},
		&bond.TextBlock{Text: "b"},
	)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if len(msgs[0].Content) != 2 {
		t.Fatalf("expected 2 blocks, got %d", len(msgs[0].Content))
	}
}
