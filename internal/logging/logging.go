package logging

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"sync"
)

type handler struct {
	prefix string
	out    io.Writer
	mu     *sync.Mutex
	attrs  []slog.Attr
	group  string
}

func New(prefix string) *slog.Logger {
	return NewTo(prefix, os.Stderr)
}

func NewTo(prefix string, out io.Writer) *slog.Logger {
	return slog.New(&handler{prefix: prefix, out: out, mu: &sync.Mutex{}})
}

func (h *handler) Enabled(context.Context, slog.Level) bool {
	return true
}

func (h *handler) Handle(_ context.Context, record slog.Record) error {
	var line bytes.Buffer
	line.WriteString(h.prefix)
	line.WriteByte(' ')
	line.WriteString(record.Message)
	for _, attr := range h.attrs {
		h.write(&line, attr)
	}
	record.Attrs(func(attr slog.Attr) bool {
		h.write(&line, attr)
		return true
	})
	line.WriteByte('\n')
	h.mu.Lock()
	defer h.mu.Unlock()
	_, err := h.out.Write(line.Bytes())
	return err
}

func (h *handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	next := *h
	next.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &next
}

func (h *handler) WithGroup(name string) slog.Handler {
	next := *h
	if next.group == "" {
		next.group = name
	} else {
		next.group = next.group + "." + name
	}
	return &next
}

func (h *handler) write(line *bytes.Buffer, attr slog.Attr) {
	if attr.Equal(slog.Attr{}) {
		return
	}
	key := attr.Key
	if h.group != "" {
		key = h.group + "." + key
	}
	fmt.Fprintf(line, " %s=%v", key, attr.Value.Resolve().Any())
}
