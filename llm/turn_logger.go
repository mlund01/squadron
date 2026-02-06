package llm

import (
	"encoding/json"
	"os"
	"time"
)

const contentPreviewMaxLen = 200

// TurnLogger writes a full session snapshot after every LLM turn to a JSONL file.
type TurnLogger struct {
	file      *os.File
	turnCount int
}

// NewTurnLogger creates a turn logger that writes to the given file path.
func NewTurnLogger(filename string) (*TurnLogger, error) {
	f, err := os.Create(filename)
	if err != nil {
		return nil, err
	}
	return &TurnLogger{file: f}, nil
}

// Close closes the underlying file.
func (tl *TurnLogger) Close() {
	if tl.file != nil {
		tl.file.Close()
	}
}

// turnSnapshot is the top-level envelope written per turn.
type turnSnapshot struct {
	Turn         int               `json:"turn"`
	Timestamp    string            `json:"timestamp"`
	Action       string            `json:"action,omitempty"`
	MessageCount int               `json:"message_count"`
	Messages     []messageSnapshot `json:"messages"`
}

// messageSnapshot captures one message's state without the full payload.
type messageSnapshot struct {
	Index          int              `json:"index"`
	Role           string           `json:"role"`
	ContentPreview string           `json:"content_preview,omitempty"`
	ContentLength  int              `json:"content_length"`
	HasImage       bool             `json:"has_image"`
	ImageMediaType string           `json:"image_media_type,omitempty"`
	ImageBytes     int              `json:"image_bytes,omitempty"`
	Pruned         bool             `json:"pruned"`
	Metadata       *metadataSnapshot `json:"metadata"`
}

// metadataSnapshot is the serializable form of MessageMetadata.
type metadataSnapshot struct {
	ToolName     string `json:"tool_name"`
	MessageID    string `json:"message_id"`
	MessageIndex int    `json:"message_index"`
	IsPrunable   bool   `json:"is_prunable"`
}

// LogTurn snapshots the full message list and writes one JSONL line.
func (tl *TurnLogger) LogTurn(action string, messages []Message) {
	tl.turnCount++

	snap := turnSnapshot{
		Turn:         tl.turnCount,
		Timestamp:    time.Now().Format(time.RFC3339Nano),
		Action:       action,
		MessageCount: len(messages),
		Messages:     make([]messageSnapshot, len(messages)),
	}

	for i, msg := range messages {
		ms := messageSnapshot{
			Index:  i,
			Role:   string(msg.Role),
			Pruned: msg.Content == "[RESULT PRUNED]",
		}

		// Content info
		text := msg.GetTextContent()
		ms.ContentLength = len(text)
		if len(text) > contentPreviewMaxLen {
			ms.ContentPreview = text[:contentPreviewMaxLen] + "..."
		} else if text != "" {
			ms.ContentPreview = text
		}

		// Image info — scan Parts for images
		if msg.HasParts() {
			for _, part := range msg.Parts {
				if part.Type == ContentTypeImage && part.ImageData != nil {
					ms.HasImage = true
					ms.ImageMediaType = part.ImageData.MediaType
					ms.ImageBytes = len(part.ImageData.Data)
					break // report first image only
				}
			}
		}

		// Metadata
		if msg.Metadata != nil {
			ms.Metadata = &metadataSnapshot{
				ToolName:     msg.Metadata.ToolName,
				MessageID:    msg.Metadata.MessageID,
				MessageIndex: msg.Metadata.MessageIndex,
				IsPrunable:   msg.Metadata.IsPrunable,
			}
		}

		snap.Messages[i] = ms
	}

	data, err := json.Marshal(snap)
	if err != nil {
		return
	}
	tl.file.WriteString(string(data) + "\n")
}
