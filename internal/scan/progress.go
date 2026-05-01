package scan

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mattn/go-isatty"
)

// progress owns the single-line \r-overwriting status display during
// a scan. One goroutine (the ticker in start) writes to the terminal;
// parse workers feed it via atomic counter bumps.
type progress struct {
	out     io.Writer
	enabled bool

	phase    atomic.Value // string
	current  atomic.Int64
	total    atomic.Int64
	warnings atomic.Int64

	once sync.Once
	done chan struct{}
}

func newProgress(out io.Writer, quiet bool) *progress {
	p := &progress{
		out:  out,
		done: make(chan struct{}),
	}
	p.phase.Store("")
	if !quiet {
		if f, ok := out.(*os.File); ok {
			p.enabled = isatty.IsTerminal(f.Fd()) || isatty.IsCygwinTerminal(f.Fd())
		}
	}
	return p
}

func (p *progress) start() {
	if !p.enabled {
		return
	}
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				p.render()
			case <-p.done:
				p.clear()
				return
			}
		}
	}()
}

func (p *progress) stop() {
	if !p.enabled {
		return
	}
	p.once.Do(func() { close(p.done) })
}

// setPhase updates the display phase. Pass total > 0 to show a counter.
func (p *progress) setPhase(name string, total int64) {
	p.phase.Store(name)
	p.current.Store(0)
	p.total.Store(total)
	if p.enabled {
		p.render()
	}
}

func (p *progress) inc() {
	p.current.Add(1)
}

func (p *progress) incWarnings() {
	p.warnings.Add(1)
}

func (p *progress) render() {
	phase := p.phase.Load().(string)
	if phase == "" {
		return
	}
	cur := p.current.Load()
	tot := p.total.Load()
	warns := p.warnings.Load()

	var b strings.Builder
	b.WriteString("\r")
	b.WriteString(phase)
	if tot > 0 {
		fmt.Fprintf(&b, " %d/%d", cur, tot)
	}
	if warns > 0 {
		fmt.Fprintf(&b, "  [%d warnings]", warns)
	}

	line := b.String()
	// Pad with spaces to overwrite any remnant from a longer previous line.
	const minWidth = 60
	if pad := minWidth - len(line) + 1; pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	_, _ = fmt.Fprint(p.out, line)
}

func (p *progress) clear() {
	_, _ = fmt.Fprintf(p.out, "\r%s\r", strings.Repeat(" ", 60))
}
