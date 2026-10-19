package code

import (
	"bytes"
	"context"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/zarldev/zarlmono/zkit/ai/tools"
)

const (
	bashPreviewLineLimit     = 8 * 1024
	bashPreviewMaxUpdates    = 256
	bashPreviewInterval      = 100 * time.Millisecond
	bashPreviewNoticeReserve = 128
)

var (
	bashPreviewPEMBegin     = regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`)
	bashPreviewPEMEnd       = regexp.MustCompile(`-----END [A-Z ]*PRIVATE KEY-----`)
	bashPreviewCredential   = regexp.MustCompile(`(?i)(\bbearer\b|\b[a-z0-9_]*(?:api[_-]?key|password|passphrase|secret|token)\b["']?\s*[:=]|\b(?:proxy[_-]?)?authorization\b["']?\s*[:=])`)
	bashPreviewContinuation = regexp.MustCompile(`(?i)(\bbearer|\b[a-z0-9_]*(?:api[_-]?key|password|passphrase|secret|token)\b["']?\s*[:=]|\b(?:proxy[_-]?)?authorization\b["']?\s*[:=])\s*["']?\s*$`)
)

type bashPreviewStream struct {
	pending                []byte
	privateKey             bool
	credentialContinuation bool
}

// bashOutputPreview owns per-stream framing before any untrusted process bytes
// reach an observer. It retains only safe complete lines and bounded snapshots.
// cmd.Wait owns stdout/stderr copying; Execute starts and joins one bounded
// flush timer so a quiet process cannot strand a throttled complete line.
type bashOutputPreview struct {
	mu             sync.Mutex
	streams        [2]bashPreviewStream
	text           strings.Builder
	processed      int
	stopped        bool
	updates        int
	publishedBytes int
	lastPublish    time.Time
	publish        func(string)
}

func (p *bashOutputPreview) write(stderr bool, data []byte) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return
	}
	if p.updates >= bashPreviewMaxUpdates-1 {
		p.stop("[live preview stopped: update limit reached]\n")
		return
	}
	index := 0
	if stderr {
		index = 1
	}
	stream := &p.streams[index]
	for _, char := range data {
		if p.stopped {
			break
		}
		if p.processed >= tools.MaxToolOutputPreviewBytes {
			p.stop("[live preview stopped: byte limit reached]\n")
			break
		}
		p.processed++
		if char == '\n' {
			line := stream.pending
			if len(line) > 0 && line[len(line)-1] == '\r' {
				line = line[:len(line)-1]
			}
			p.completeLine(stream, stderr, line)
			stream.pending = stream.pending[:0]
			continue
		}
		if len(stream.pending) >= bashPreviewLineLimit {
			p.stop("[live preview stopped: oversized output line]\n")
			break
		}
		stream.pending = append(stream.pending, char)
	}
	p.emit(false)
}

func (p *bashOutputPreview) completeLine(stream *bashPreviewStream, stderr bool, line []byte) {
	if !utf8.Valid(line) {
		p.stop("[live preview stopped: invalid UTF-8 output]\n")
		return
	}
	for _, char := range string(line) {
		if unicode.Is(unicode.Bidi_Control, char) || unicode.IsControl(char) && char != '\t' {
			p.stop("[live preview stopped: terminal controls omitted]\n")
			return
		}
	}
	if stream.privateKey {
		if bashPreviewPEMEnd.Match(line) {
			stream.privateKey = false
		}
		return
	}
	if bashPreviewPEMBegin.Match(line) {
		stream.privateKey = !bashPreviewPEMEnd.Match(line)
		p.appendSafe("[private-key output redacted]\n", stderr)
		return
	}
	if stream.credentialContinuation {
		if len(bytes.TrimSpace(line)) != 0 {
			stream.credentialContinuation = false
		}
		return
	}
	if bashPreviewCredential.Match(line) {
		stream.credentialContinuation = bashPreviewContinuation.Match(line)
		p.appendSafe("[credential-bearing output redacted]\n", stderr)
		return
	}
	p.appendSafe(tools.RedactSecrets(string(line))+"\n", stderr)
}

func (p *bashOutputPreview) appendSafe(line string, stderr bool) {
	prefix := ""
	if stderr {
		prefix = "[stderr] "
	}
	if p.text.Len()+len(prefix)+len(line) > tools.MaxToolOutputPreviewBytes-bashPreviewNoticeReserve {
		p.stop("[live preview stopped: retained output limit reached]\n")
		return
	}
	p.text.WriteString(prefix)
	p.text.WriteString(line)
}

func (p *bashOutputPreview) stop(notice string) {
	if p.stopped {
		return
	}
	p.stopped = true
	for i := range p.streams {
		p.streams[i].pending = nil
	}
	p.text.WriteString(notice)
	p.emit(true)
}

func (p *bashOutputPreview) emit(force bool) {
	if p.updates >= bashPreviewMaxUpdates || p.text.Len() == p.publishedBytes {
		return
	}
	now := time.Now()
	if !force && !p.lastPublish.IsZero() && now.Sub(p.lastPublish) < bashPreviewInterval {
		return
	}
	p.lastPublish = now
	p.publishedBytes = p.text.Len()
	p.updates++
	p.publish(p.text.String())
}

func (p *bashOutputPreview) finish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.stopped {
		for _, stream := range p.streams {
			if len(stream.pending) != 0 || stream.privateKey || stream.credentialContinuation {
				p.stop("[live preview omitted unterminated or sensitive output]\n")
				break
			}
		}
	}
	p.emit(true)
}

// startFlush owns one coalescing timer for this invocation. The returned stop
// cancels and joins it before final output or a terminal event is published.
func (p *bashOutputPreview) startFlush(ctx context.Context) func() {
	flushContext, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(bashPreviewInterval)
		defer ticker.Stop()
		for {
			select {
			case <-flushContext.Done():
				return
			case <-ticker.C:
				p.mu.Lock()
				p.emit(false)
				p.mu.Unlock()
			}
		}
	}()
	return func() { cancel(); <-done }
}

// bashPreviewWriter feeds a globally bounded, per-stream final collector and
// optional per-stream preview parsing. Both paths sanitize before merging;
// within-stream byte order is preserved without a raw secret-fragment merge.
type bashPreviewWriter struct {
	collector *limitedBuffer
	preview   *bashOutputPreview
	stderr    bool
}

func (w bashPreviewWriter) Write(data []byte) (int, error) {
	n, err := w.collector.write(data, w.stderr)
	if w.preview != nil {
		w.preview.write(w.stderr, data)
	}
	return n, err
}
