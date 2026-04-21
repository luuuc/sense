package tui

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/luuuc/sense/internal/blast"
)

const (
	blastFrames   = 4
	blastDuration = 400 * time.Millisecond
	blastInterval = blastDuration / blastFrames
)

type blastTickMsg time.Time

func blastTick() tea.Cmd {
	return tea.Tick(blastInterval, func(t time.Time) tea.Msg {
		return blastTickMsg(t)
	})
}

type blastState struct {
	result  blast.Result
	hopMap  map[int64]int // symbol ID → hop distance (0 = subject)
	frame   int           // current animation frame (0..blastFrames)
	hubNode bool          // >50% of nodes affected
}

func computeBlast(db *sql.DB, symbolID int64) (*blastState, error) {
	if db == nil {
		return nil, fmt.Errorf("no database connection")
	}
	result, err := blast.Compute(context.Background(), db, symbolID, blast.Options{
		MaxHops:      3,
		IncludeTests: true,
	})
	if err != nil {
		return nil, err
	}

	hopMap := make(map[int64]int, 1+len(result.DirectCallers)+len(result.IndirectCallers))
	hopMap[result.Symbol.ID] = 0
	for _, c := range result.DirectCallers {
		hopMap[c.ID] = 1
	}
	for _, c := range result.IndirectCallers {
		hopMap[c.Symbol.ID] = c.Hops
	}

	return &blastState{
		result: result,
		hopMap: hopMap,
	}, nil
}

// visibleHops returns the max hop distance that should be illuminated
// at the current animation frame. Frame 0 = subject only, frame 1 = hop 1,
// frame 2 = hop 2, frame 3 = hop 3. Direct 1:1 mapping gives the per-hop
// ripple the pitch describes.
func (b *blastState) visibleHops() int {
	if b.frame >= blastFrames {
		return 3
	}
	return b.frame
}

// blastColorForHop maps hop distance to a color index in the blast palette.
// 0 = subject (bright), 1 = direct (warm), 2 = second-hop (amber), 3 = third-hop (dim).
func blastColorForHop(hop int) uint8 {
	switch hop {
	case 0:
		return colorBlastSubject
	case 1:
		return colorBlastHop1
	case 2:
		return colorBlastHop2
	default:
		return colorBlastHop3
	}
}

const (
	colorBlastSubject uint8 = 250
	colorBlastHop1    uint8 = 251
	colorBlastHop2    uint8 = 252
	colorBlastHop3    uint8 = 253
	colorFaded        uint8 = 249
)

func (b *blastState) statusText() string {
	r := b.result
	testCount := len(r.AffectedTests)
	return fmt.Sprintf("risk:%s  callers:%d  affected:%d  tests:%d",
		r.Risk, len(r.DirectCallers), r.TotalAffected, testCount)
}
