package llm

import "testing"

func TestAddToolResultMedia_MergesIntoToolResultsTurn(t *testing.T) {
	s := NewSession(&mockProvider{}, "m")
	s.AddToolResults([]ToolResultBlock{{ToolUseID: "tu-1", Content: "summary"}})
	s.AddToolResultMedia([]ContentBlock{
		{Type: ContentTypeImage, ImageData: &ImageBlock{Data: "QUJD", MediaType: "image/png"}},
		{Type: ContentTypeDocument, Document: &DocumentBlock{Data: "REVG", MediaType: "application/pdf", Filename: "r.pdf"}},
	})

	msgs := s.GetHistory()
	if len(msgs) != 1 {
		t.Fatalf("expected media merged into a single user message, got %d messages", len(msgs))
	}
	parts := msgs[0].Parts
	if len(parts) != 3 {
		t.Fatalf("expected 3 parts (tool_result + image + document), got %d", len(parts))
	}
	if parts[0].Type != ContentTypeToolResult || parts[1].Type != ContentTypeImage || parts[2].Type != ContentTypeDocument {
		t.Fatalf("unexpected part ordering: %s, %s, %s", parts[0].Type, parts[1].Type, parts[2].Type)
	}
}

func TestAddToolResultMedia_NoToolResultsAppendsOwnTurn(t *testing.T) {
	s := NewSession(&mockProvider{}, "m")
	s.AddToolResultMedia([]ContentBlock{{Type: ContentTypeImage, ImageData: &ImageBlock{Data: "QUJD", MediaType: "image/png"}}})
	if got := len(s.GetHistory()); got != 1 {
		t.Fatalf("expected 1 message, got %d", got)
	}
}

func TestClone_CopiesDocumentBlock(t *testing.T) {
	s := NewSession(&mockProvider{}, "m")
	s.AddToolResultMedia([]ContentBlock{{Type: ContentTypeDocument, Document: &DocumentBlock{Data: "REVG", MediaType: "application/pdf", Filename: "r.pdf"}}})
	clone := s.Clone()

	cp := clone.GetHistory()[0].Parts[0]
	if cp.Document == nil || cp.Document.Data != "REVG" || cp.Document.MediaType != "application/pdf" {
		t.Fatalf("document block not deep-copied: %+v", cp.Document)
	}
	// Mutating the clone must not affect the original.
	cp.Document.Data = "changed"
	if s.GetHistory()[0].Parts[0].Document.Data != "REVG" {
		t.Fatalf("clone shares document pointer with original")
	}
}
