package examples

import (
	"encoding/json"
	"testing"
)

// Geometry contract for bundled examples (UX Phase 1.1): node cards are
// 240px wide and at least ~120px tall including ports and status chip, so
// any two nodes closer than these bounds on both axes render overlapping.
// The layout keeps ≥320px between columns and ≥170px between branch rows,
// plus extra clearance behind multi-output fan-outs.
const (
	nodeWidth  = 240
	nodeHeight = 120
	minGapX    = 320
	minGapY    = 170
)

type layoutWorkflow struct {
	Nodes []struct {
		ID       string `json:"id"`
		Position struct {
			X float64 `json:"x"`
			Y float64 `json:"y"`
		} `json:"position"`
	} `json:"nodes"`
}

func TestExampleNodeGeometry(t *testing.T) {
	list, err := All()
	if err != nil {
		t.Fatalf("list examples: %v", err)
	}
	if len(list) == 0 {
		t.Fatal("no embedded examples found")
	}
	for _, ex := range list {
		var wf layoutWorkflow
		if err := json.Unmarshal(ex.Workflow, &wf); err != nil {
			t.Fatalf("%s: decode: %v", ex.ID, err)
		}
		for i := range wf.Nodes {
			for j := i + 1; j < len(wf.Nodes); j++ {
				a, b := wf.Nodes[i], wf.Nodes[j]
				gapX := a.Position.X - b.Position.X
				if gapX < 0 {
					gapX = -gapX
				}
				gapY := a.Position.Y - b.Position.Y
				if gapY < 0 {
					gapY = -gapY
				}
				overlapX := nodeWidth - gapX
				overlapY := nodeHeight - gapY
				if overlapX > 0 && overlapY > 0 {
					t.Errorf("%s: nodes %s and %s overlap (dx=%.0f dy=%.0f; want ≥%dx%+d separation)",
						ex.ID, a.ID, b.ID, gapX, gapY, minGapX, minGapY-nodeHeight+120)
				}
				if gapY < nodeHeight && gapX < minGapX {
					t.Errorf("%s: nodes %s and %s in the same visual row are only %.0fpx apart horizontally (want ≥%d)",
						ex.ID, a.ID, b.ID, gapX, minGapX)
				}
				if gapX < nodeWidth && gapY < minGapY {
					t.Errorf("%s: nodes %s and %s in the same visual column are only %.0fpx apart vertically (want ≥%d)",
						ex.ID, a.ID, b.ID, gapY, minGapY)
				}
			}
		}
	}
}
