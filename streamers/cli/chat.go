package cli

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/glamour"
)

// ChatHandler implements streamers.ChatHandler for terminal I/O
type ChatHandler struct {
	reader           *bufio.Reader
	spinner          *spinner
	reasoningStarted bool
	answerBuffer     strings.Builder
	renderer         *glamour.TermRenderer
}

// NewChatHandler creates a new CLI chat handler
func NewChatHandler() *ChatHandler {
	renderer, _ := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(100),
	)
	return &ChatHandler{
		reader:   bufio.NewReader(os.Stdin),
		spinner:  newSpinner(),
		renderer: renderer,
	}
}

func (s *ChatHandler) Welcome(agentName string, modelName string) {
	fmt.Printf("%s%sStarting chat with agent '%s'%s (model: %s)\n", ColorBold, ColorOrange, agentName, ColorReset, modelName)
	fmt.Printf("%sType 'exit' or 'quit' to end the conversation.%s\n", ColorGray, ColorReset)
	fmt.Println()
}

func (s *ChatHandler) AwaitClientAnswer() (string, error) {
	// Show input prompt
	fmt.Printf("%s>  %s", ColorGray, ColorReset)
	input, err := s.reader.ReadString('\n')
	if err != nil {
		return "", err
	}
	input = strings.TrimSpace(input)
	if input != "" {
		// Move cursor up, clear line, then print the user message in light brown with > prefix and indented
		fmt.Print("\033[1A\033[K")
		fmt.Printf("%s>  %s%s\n\n", ColorGray, ColorLightBrown, input+ColorReset)
	}
	return input, nil
}

func (s *ChatHandler) Goodbye() {
	fmt.Printf("%sGoodbye!%s\n", ColorGray, ColorReset)
}

func (s *ChatHandler) Error(err error) {
	fmt.Fprintf(os.Stderr, "Error: %v\n", err)
}

func (s *ChatHandler) Thinking() {
	s.spinner.Start("", "Thinking...")
}

func (s *ChatHandler) CallingTool(toolName string, payload string) {
	s.spinner.Stop()
	s.spinner.Start("", fmt.Sprintf("Calling %s%s%s...", ColorBold, toolName, ColorReset))
}

func (s *ChatHandler) ToolComplete(toolName string) {
	s.spinner.Stop()
	fmt.Printf("%s✓%s %s%s%s called\n\n", ColorGray, ColorReset, ColorBold, toolName, ColorReset)
}

func (s *ChatHandler) PublishReasoningChunk(chunk string) {
	// On first chunk, stop spinner and print title
	if !s.reasoningStarted {
		s.spinner.Stop()
		fmt.Printf("%s%sReasoning%s\n", ColorBold, ColorMagenta, ColorReset)
		s.reasoningStarted = true
	}
	// Stream directly in magenta italic
	fmt.Printf("%s%s%s", ColorItalic, ColorMagenta, chunk)
}

func (s *ChatHandler) FinishReasoning() {
	if s.reasoningStarted {
		// Reset color and add spacing
		fmt.Printf("%s\n\n", ColorReset)
		s.reasoningStarted = false
	}
	// Show waiting indicator while answer is being generated
	s.spinner.Start("", "Waiting for answer...")
}

func (s *ChatHandler) PublishAnswerChunk(chunk string) {
	// Buffer chunks - spinner keeps running
	s.answerBuffer.WriteString(chunk)
}

func (s *ChatHandler) FinishAnswer() {
	s.spinner.Stop()

	content := s.answerBuffer.String()
	if content == "" {
		return
	}

	// Render markdown
	rendered := content
	if s.renderer != nil {
		if out, err := s.renderer.Render(content); err == nil {
			rendered = out
		}
	}

	// Glamour adds leading/trailing newlines - trim them
	rendered = strings.TrimSpace(rendered)
	fmt.Printf("%s•%s%s\n\n", ColorGray, ColorReset, rendered)

	s.answerBuffer.Reset()
}

// spinner handles the loading animation
type spinner struct {
	frames  []string
	stop    chan struct{}
	stopped chan struct{}
	mu      sync.Mutex
	running bool
}

func newSpinner() *spinner {
	return &spinner{
		frames:  []string{"◐", "◓", "◑", "◒"},
		stop:    make(chan struct{}),
		stopped: make(chan struct{}),
	}
}

func (s *spinner) Start(prefix string, message string) {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return
	}
	s.running = true
	s.stop = make(chan struct{})
	s.stopped = make(chan struct{})
	s.mu.Unlock()

	go func() {
		defer close(s.stopped)
		i := 0
		for {
			select {
			case <-s.stop:
				fmt.Print("\r\033[K") // Clear line
				return
			default:
				if prefix != "" {
					fmt.Printf("\r%s %s%s%s %s", prefix, ColorOrange, s.frames[i%len(s.frames)], ColorReset, message)
				} else {
					fmt.Printf("\r%s%s%s %s", ColorGray, s.frames[i%len(s.frames)], ColorReset, message)
				}
				i++
				time.Sleep(80 * time.Millisecond)
			}
		}
	}()
}

func (s *spinner) Stop() {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return
	}
	s.running = false
	s.mu.Unlock()

	close(s.stop)
	<-s.stopped
}
