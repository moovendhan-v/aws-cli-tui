package commands

import (
	"sort"
	"strings"

	"github.com/sahilm/fuzzy"

	"github.com/moovendhan-v/aws-cli-tui/devops/internal/storage"
)

type Match struct {
	Command *Command
	Score   int
	Reason  string // "exact", "text", "fuzzy"
}

// UseIndex wires the SQLite FTS5 index and usage statistics into search.
func (r *Registry) UseIndex(st *storage.Store) error {
	docs := make([]storage.IndexDoc, 0, len(r.ordered))
	for _, c := range r.ordered {
		docs = append(docs, storage.IndexDoc{
			ID: c.ID, Name: c.Name, Category: c.Category, Description: c.Description,
			Tags: strings.Join(c.Tags, " ") + " " + strings.ReplaceAll(c.ID, ".", " "),
		})
	}
	if err := st.IndexCommands(docs); err != nil {
		return err
	}
	if st.FTSEnabled() {
		r.fts = st.SearchCommands
	}
	r.usage = st.UsageCounts()
	return nil
}

// NoteUsage bumps a command's ranking after it runs.
func (r *Registry) NoteUsage(id string) { r.usage[id]++ }

// Search ranks registered commands: exact matches, then full-text (FTS5),
// then fuzzy matching. It only ever returns registered commands.
func (r *Registry) Search(q string, limit int) []Match {
	q = strings.TrimSpace(q)
	if q == "" {
		return nil
	}
	lq := strings.ToLower(q)
	scores := map[string]*Match{}
	add := func(c *Command, s int, why string) {
		if m, ok := scores[c.ID]; ok {
			m.Score += s
			return
		}
		scores[c.ID] = &Match{Command: c, Score: s, Reason: why}
	}

	// 1. Exact ID/name, then name prefix.
	for _, c := range r.ordered {
		switch {
		case strings.ToLower(c.ID) == lq || strings.ToLower(c.Name) == lq:
			add(c, 1000, "exact")
		case strings.HasPrefix(strings.ToLower(c.Name), lq):
			add(c, 600, "exact")
		}
	}
	// 2. Full-text (FTS5 with porter stemming), falling back to token matching.
	if ids, err := r.ftsSearch(q); err == nil && len(ids) > 0 {
		for i, id := range ids {
			if c := r.byID[id]; c != nil {
				add(c, 400-i*10, "text")
			}
		}
	} else {
		for _, c := range r.ordered {
			if n := tokenHits(c, lq); n > 0 {
				add(c, 200+n*40, "text")
			}
		}
	}
	// 3. Fuzzy on names for typos and abbreviations.
	names := make([]string, len(r.ordered))
	for i, c := range r.ordered {
		names[i] = c.Name + " " + c.Category
	}
	for _, m := range fuzzy.Find(q, names) {
		if m.Score > 0 {
			add(r.ordered[m.Index], min(m.Score, 150), "fuzzy")
		}
	}

	out := make([]Match, 0, len(scores))
	for _, m := range scores {
		m.Score += min(r.usage[m.Command.ID]*5, 50)
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].Command.Name < out[j].Command.Name
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (r *Registry) ftsSearch(q string) ([]string, error) {
	if r.fts == nil {
		return nil, nil
	}
	return r.fts(q, 25)
}

func tokenHits(c *Command, lq string) int {
	hay := strings.ToLower(c.Name + " " + c.Category + " " + c.Description + " " + strings.Join(c.Tags, " ") + " " + c.ID)
	n := 0
	for _, t := range strings.Fields(lq) {
		t = strings.TrimSuffix(t, "s")
		if len(t) > 1 && strings.Contains(hay, t) {
			n++
		}
	}
	return n
}
