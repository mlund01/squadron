package agent

// EventLogger is the interface for logging structured events during execution
type EventLogger interface {
	LogEvent(eventType string, data map[string]any)
}

// contextEventLogger wraps an EventLogger and adds context fields to every event
type contextEventLogger struct {
	inner  EventLogger
	fields map[string]any
}

func newContextEventLogger(inner EventLogger, fields map[string]any) EventLogger {
	return &contextEventLogger{inner: inner, fields: fields}
}

func (l *contextEventLogger) LogEvent(eventType string, data map[string]any) {
	merged := make(map[string]any, len(l.fields)+len(data))
	for k, v := range l.fields {
		merged[k] = v
	}
	for k, v := range data {
		merged[k] = v
	}
	l.inner.LogEvent(eventType, merged)
}
