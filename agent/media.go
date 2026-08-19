package agent

import (
	"squadron/aitools"
	"squadron/llm"
)

// mediaBlocksToContentBlocks translates provider-neutral tool media into llm
// content blocks. Images and SVG-rasterized PNGs become image blocks; PDFs
// become document blocks. Unknown kinds are dropped.
func mediaBlocksToContentBlocks(media []aitools.MediaBlock) []llm.ContentBlock {
	if len(media) == 0 {
		return nil
	}
	parts := make([]llm.ContentBlock, 0, len(media))
	for _, m := range media {
		switch m.Kind {
		case aitools.MediaKindImage:
			parts = append(parts, llm.ContentBlock{
				Type:      llm.ContentTypeImage,
				ImageData: &llm.ImageBlock{Data: m.Data, MediaType: m.MediaType},
			})
		case aitools.MediaKindDocument:
			parts = append(parts, llm.ContentBlock{
				Type:     llm.ContentTypeDocument,
				Document: &llm.DocumentBlock{Data: m.Data, MediaType: m.MediaType, Filename: m.Filename},
			})
		}
	}
	return parts
}
