package tui

import "testing"

func rendererTestLayout() *Layout {
	nodes, edges := makeTestLayoutGraph()
	applyFruchtermanReingold(nodes, edges)
	return &Layout{
		GraphHash: "test",
		Nodes:     nodes,
		Edges:     edges,
	}
}

func TestGolden_RendererFit(t *testing.T) {
	r := &GraphRenderer{Layout: rendererTestLayout(), Mode: RenderBraille}
	got := r.Render(60, 20)
	assertGolden(t, "renderer_fit", got)
}

func TestGolden_RendererZoomMedium(t *testing.T) {
	r := &GraphRenderer{
		Layout:   rendererTestLayout(),
		Mode:     RenderBraille,
		Viewport: Viewport{Zoom: ZoomMedium},
	}
	got := r.Render(60, 20)
	assertGolden(t, "renderer_zoom_medium", got)
}

func TestGolden_RendererZoomClose(t *testing.T) {
	r := &GraphRenderer{
		Layout:   rendererTestLayout(),
		Mode:     RenderBraille,
		Viewport: Viewport{Zoom: ZoomClose},
	}
	got := r.Render(60, 20)
	assertGolden(t, "renderer_zoom_close", got)
}

func TestGolden_RendererPanned(t *testing.T) {
	r := &GraphRenderer{
		Layout:   rendererTestLayout(),
		Mode:     RenderBraille,
		Viewport: Viewport{Zoom: ZoomMedium, OffsetX: 0.1, OffsetY: 0.05},
	}
	got := r.Render(60, 20)
	assertGolden(t, "renderer_panned", got)
}

func TestNodeRadius(t *testing.T) {
	tests := []struct {
		centrality int
		wantMin    int
		wantMax    int
	}{
		{0, 1, 1},
		{1, 1, 2},
		{3, 2, 3},
		{10, 3, 4},
		{100, 5, 5},
	}
	for _, tc := range tests {
		got := nodeRadius(tc.centrality)
		if got < tc.wantMin || got > tc.wantMax {
			t.Errorf("nodeRadius(%d) = %d, want [%d,%d]", tc.centrality, got, tc.wantMin, tc.wantMax)
		}
	}
}

func TestViewport_ZoomBounds(t *testing.T) {
	v := Viewport{}
	v.ZoomOut()
	if v.Zoom != ZoomFit {
		t.Error("should not zoom below fit")
	}
	v.Zoom = ZoomDetail
	v.ZoomIn()
	if v.Zoom != ZoomDetail {
		t.Error("should not zoom above detail")
	}
}

func TestZoomLevel_String(t *testing.T) {
	tests := []struct {
		z    ZoomLevel
		want string
	}{
		{ZoomFit, "fit"},
		{ZoomMedium, "2×"},
		{ZoomClose, "4×"},
		{ZoomDetail, "8×"},
	}
	for _, tc := range tests {
		if got := tc.z.String(); got != tc.want {
			t.Errorf("ZoomLevel(%d).String() = %q, want %q", tc.z, got, tc.want)
		}
	}
}
