package layout

import (
	"cmp"
	"slices"
	"strings"
)

// box is a Cell with the size it occupies.
type box struct {
	Cell
	w, h int
}

func refs(cells []box) []*box {
	out := make([]*box, len(cells))
	for i := range cells {
		out[i] = &cells[i]
	}
	return out
}

func connectors(cells []box) string {
	names := make([]string, 0, len(cells))
	for _, b := range cells {
		names = append(names, b.Connector)
	}
	return strings.Join(names, ", ")
}

// axis selects the coordinate and extent a sweep works along.
type axis struct {
	pos func(*box) *int
	ext func(*box) int
}

var (
	xAxis = axis{pos: func(b *box) *int { return &b.X }, ext: func(b *box) int { return b.w }}
	yAxis = axis{pos: func(b *box) *int { return &b.Y }, ext: func(b *box) int { return b.h }}
)

// normalize shifts every cell so the bounding box starts at the origin.
func normalize(cells []box) {
	minX, minY := cells[0].X, cells[0].Y
	for _, b := range cells[1:] {
		minX = min(minX, b.X)
		minY = min(minY, b.Y)
	}
	for i := range cells {
		cells[i].X -= minX
		cells[i].Y -= minY
	}
}

// closeGaps shifts cells toward the origin along a until no band of that
// axis is empty: a column or row that lost every monitor closes, and the
// cells beyond it move over by its width. Cells that share a band stay
// where they are relative to each other.
func closeGaps(cells []*box, a axis) {
	order := slices.Clone(cells)
	slices.SortFunc(order, func(p, q *box) int { return cmp.Compare(*a.pos(p), *a.pos(q)) })
	covered, shift := 0, 0
	for _, b := range order {
		start := *a.pos(b)
		if start > covered {
			shift += start - covered
			covered = start
		}
		covered = max(covered, start+a.ext(b))
		*a.pos(b) = start - shift
	}
}

// bands groups cells whose extents along a overlap, in order along a:
// with yAxis the groups are rows, with xAxis they are columns.
func bands(cells []box, a axis) [][]*box {
	order := refs(cells)
	slices.SortFunc(order, func(p, q *box) int { return cmp.Compare(*a.pos(p), *a.pos(q)) })
	var groups [][]*box
	end := 0
	for _, b := range order {
		start := *a.pos(b)
		if len(groups) == 0 || start >= end {
			groups = append(groups, nil)
			end = start
		}
		groups[len(groups)-1] = append(groups[len(groups)-1], b)
		end = max(end, start+a.ext(b))
	}
	return groups
}

// compact closes up each row leftward, then each column upward, and
// reports whether any cell moved. It runs only while band closing has left
// the layout disconnected; on a grid one pass turns any set of survivors
// into a compact block, mixed sizes can need another.
func compact(cells []box) (moved bool) {
	before := slices.Clone(cells)
	for _, row := range bands(cells, yAxis) {
		closeGaps(row, xAxis)
	}
	for _, col := range bands(cells, xAxis) {
		closeGaps(col, yAxis)
	}
	for i := range cells {
		if cells[i].X != before[i].X || cells[i].Y != before[i].Y {
			return true
		}
	}
	return false
}

// adjacent mirrors mtk_rectangle_is_adjacent_to: the cells share an edge
// of positive length. A shared corner does not count.
func adjacent(a, b box) bool {
	ax2, ay2 := a.X+a.w, a.Y+a.h
	bx2, by2 := b.X+b.w, b.Y+b.h
	if (a.X == bx2 || ax2 == b.X) && ay2 > b.Y && a.Y < by2 {
		return true
	}
	if (a.Y == by2 || ay2 == b.Y) && ax2 > b.X && a.X < bx2 {
		return true
	}
	return false
}

// overlap reports whether the cells share any area.
func overlap(a, b box) bool {
	return a.X < b.X+b.w && b.X < a.X+a.w && a.Y < b.Y+b.h && b.Y < a.Y+a.h
}

// firstOverlap returns the first pair of overlapping cells.
func firstOverlap(cells []box) (i, j int, found bool) {
	for i := range cells {
		for j := range cells[:i] {
			if overlap(cells[i], cells[j]) {
				return i, j, true
			}
		}
	}
	return 0, 0, false
}

// connected mirrors mutter's is_connected_to_all: every cell is reachable
// from the first through adjacent cells.
func connected(cells []box) bool {
	if len(cells) <= 1 {
		return true
	}
	seen := make([]bool, len(cells))
	seen[0] = true
	stack := []int{0}
	count := 1
	for len(stack) > 0 {
		i := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		for j := range cells {
			if seen[j] || !adjacent(cells[i], cells[j]) {
				continue
			}
			seen[j] = true
			count++
			stack = append(stack, j)
		}
	}
	return count == len(cells)
}

// ensurePrimary leaves exactly one primary: the first one pinned, or when
// none is present the cell with the smallest y, then the smallest x.
func ensurePrimary(cells []box) {
	seen := false
	for i := range cells {
		if cells[i].Primary {
			cells[i].Primary = !seen
			seen = true
		}
	}
	if seen {
		return
	}
	best := 0
	for i, b := range cells {
		if b.Y < cells[best].Y || (b.Y == cells[best].Y && b.X < cells[best].X) {
			best = i
		}
	}
	cells[best].Primary = true
}

// edge returns the right edge of the region and the y of the topmost cell
// on that edge, so a cell placed at (right, top) shares an edge with it.
func edge(cells []box) (right, top int) {
	for i, b := range cells {
		r := b.X + b.w
		if i == 0 || r > right || (r == right && b.Y < top) {
			right, top = r, b.Y
		}
	}
	return right, top
}
