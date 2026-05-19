package aitools

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestCurrentTimeDefaultsToUTCRFC3339(t *testing.T) {
	tool := &CurrentTimeTool{}
	got := tool.Call(context.Background(), "")

	parsed, err := time.Parse(time.RFC3339, got)
	if err != nil {
		t.Fatalf("expected RFC3339 output, got %q (err: %v)", got, err)
	}
	if parsed.Location().String() != "UTC" {
		t.Errorf("expected UTC location, got %s", parsed.Location())
	}
	if delta := time.Since(parsed); delta < 0 || delta > 5*time.Second {
		t.Errorf("returned time %v is not within 5s of now", parsed)
	}
}

func TestCurrentTimeEmptyJSONObjectAlsoDefaults(t *testing.T) {
	tool := &CurrentTimeTool{}
	got := tool.Call(context.Background(), "{}")
	if _, err := time.Parse(time.RFC3339, got); err != nil {
		t.Fatalf("expected RFC3339 output for empty params, got %q (err: %v)", got, err)
	}
}

func TestCurrentTimeHonorsTimezone(t *testing.T) {
	tool := &CurrentTimeTool{}
	got := tool.Call(context.Background(), `{"timezone":"America/Chicago"}`)

	parsed, err := time.Parse(time.RFC3339, got)
	if err != nil {
		t.Fatalf("expected RFC3339 output, got %q (err: %v)", got, err)
	}
	chicago, err := time.LoadLocation("America/Chicago")
	if err != nil {
		t.Skipf("America/Chicago not available on this system: %v", err)
	}
	_, wantOffset := time.Now().In(chicago).Zone()
	_, gotOffset := parsed.Zone()
	if gotOffset != wantOffset {
		t.Errorf("expected offset %d, got %d", wantOffset, gotOffset)
	}
}

func TestCurrentTimeHonorsFormat(t *testing.T) {
	tool := &CurrentTimeTool{}
	got := tool.Call(context.Background(), `{"format":"2006-01-02"}`)

	if _, err := time.Parse("2006-01-02", got); err != nil {
		t.Fatalf("expected date-only output, got %q (err: %v)", got, err)
	}
	if strings.Contains(got, "T") {
		t.Errorf("expected no time component, got %q", got)
	}
}

func TestCurrentTimeTimezoneAndFormat(t *testing.T) {
	if _, err := time.LoadLocation("Asia/Tokyo"); err != nil {
		t.Skipf("Asia/Tokyo not available on this system: %v", err)
	}
	tool := &CurrentTimeTool{}
	got := tool.Call(context.Background(), `{"timezone":"Asia/Tokyo","format":"2006-01-02 15:04 MST"}`)

	if !strings.HasSuffix(got, "JST") {
		t.Errorf("expected Tokyo zone abbreviation JST in output, got %q", got)
	}
}

func TestCurrentTimeUnknownTimezoneErrors(t *testing.T) {
	tool := &CurrentTimeTool{}
	got := tool.Call(context.Background(), `{"timezone":"Not/A_Real_Zone"}`)
	if !strings.HasPrefix(got, "Error:") {
		t.Errorf("expected error prefix, got %q", got)
	}
}

func TestCurrentTimeInvalidJSONErrors(t *testing.T) {
	tool := &CurrentTimeTool{}
	got := tool.Call(context.Background(), `{not json`)
	if !strings.HasPrefix(got, "Error:") {
		t.Errorf("expected error prefix, got %q", got)
	}
}

func TestCurrentTimeSchema(t *testing.T) {
	tool := &CurrentTimeTool{}
	schema := tool.ToolPayloadSchema()
	if schema.Type != TypeObject {
		t.Errorf("expected object schema, got %v", schema.Type)
	}
	if _, ok := schema.Properties["timezone"]; !ok {
		t.Error("expected timezone property")
	}
	if _, ok := schema.Properties["format"]; !ok {
		t.Error("expected format property")
	}
	if len(schema.Required) != 0 {
		t.Errorf("expected no required fields, got %v", schema.Required)
	}
}
