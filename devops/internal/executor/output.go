package executor

// ring keeps the most recent N output lines and an absolute line counter so
// viewers can ask for "lines since n" without re-reading everything.
type ring struct {
	buf   []string
	start int // absolute index of buf[0]
	cap   int
}

func newRing(n int) *ring { return &ring{cap: n} }

func (r *ring) push(s string) {
	r.buf = append(r.buf, s)
	if len(r.buf) > r.cap {
		drop := len(r.buf) - r.cap
		r.buf = append([]string(nil), r.buf[drop:]...)
		r.start += drop
	}
}

// len is the absolute number of lines ever written.
func (r *ring) len() int { return r.start + len(r.buf) }

func (r *ring) since(from int) []string {
	i := max(0, from-r.start)
	if i >= len(r.buf) {
		return nil
	}
	return append([]string(nil), r.buf[i:]...)
}
