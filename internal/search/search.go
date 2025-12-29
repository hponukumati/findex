package search

import (
	"database/sql"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"findex/internal/util"
)

type QueryOptions struct {
	Limit      int
	ExtFilter  map[string]struct{} // e.g. {"pdf":{}}
	Shortlist  int                 // how many candidates to pull from DB
}

type Result struct {
	Path     string
	Filename string
	Ext      string
	Mtime    int64
	Size     int64
	Score    float64
}

func DefaultQueryOptions() QueryOptions {
	return QueryOptions{
		Limit:     30,
		Shortlist: 800, // tune: 200–2000 depending on disk size
	}
}

func Search(db *sql.DB, q string, opts QueryOptions) ([]Result, error) {
	qNorm := util.Normalize(q)
	qTokens := util.Tokenize(qNorm)

	if opts.Limit <= 0 {
		opts.Limit = 30
	}
	if opts.Shortlist <= 0 {
		opts.Shortlist = 800
	}

	if qNorm == "" {
		return nil, nil
	}

	// Build SQL to shortlist candidates.
	// Strategy: require that filename_norm matches at least one token (or the whole query)
	likeParts := make([]string, 0, len(qTokens)+1)
	args := make([]any, 0, len(qTokens)+2)

	// whole query as substring
	likeParts = append(likeParts, "filename_norm LIKE ?")
	args = append(args, "%"+qNorm+"%")

	for _, t := range qTokens {
		likeParts = append(likeParts, "filename_norm LIKE ?")
		args = append(args, "%"+t+"%")
	}

	where := "(" + strings.Join(likeParts, " OR ") + ")"

	// Extension filter
	if opts.ExtFilter != nil && len(opts.ExtFilter) > 0 {
		exts := make([]string, 0, len(opts.ExtFilter))
		for e := range opts.ExtFilter {
			exts = append(exts, e)
		}
		sort.Strings(exts)
		placeholders := make([]string, 0, len(exts))
		for range exts {
			placeholders = append(placeholders, "?")
		}
		where += " AND ext IN (" + strings.Join(placeholders, ",") + ")"
		for _, e := range exts {
			args = append(args, e)
		}
	}

	sqlQ := fmt.Sprintf(`
		SELECT path, filename, ext, mtime, size
		FROM files
		WHERE %s AND is_dir = 0
		ORDER BY mtime DESC
		LIMIT %d
	`, where, opts.Shortlist)

	rows, err := db.Query(sqlQ, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cands := make([]Result, 0, opts.Shortlist)
	for rows.Next() {
		var r Result
		if err := rows.Scan(&r.Path, &r.Filename, &r.Ext, &r.Mtime, &r.Size); err != nil {
			continue
		}
		cands = append(cands, r)
	}

	// Rank
	qTri := util.Trigrams(qNorm)
	now := time.Now().Unix()

	for i := range cands {
		fnNorm := util.Normalize(cands[i].Filename)
		fnTokens := util.Tokenize(fnNorm)
		fnTri := util.Trigrams(fnNorm)

		score := 0.0

		// 1) substring match
		if strings.Contains(fnNorm, qNorm) {
			score += 6.0
		}

		// 2) token overlap
		overlap := tokenOverlapCount(qTokens, fnTokens)
		score += 2.2 * float64(overlap)

		// 3) prefix bonus (good for live typing)
		if strings.HasPrefix(fnNorm, qNorm) {
			score += 2.5
		}

		// 4) trigram similarity (typo tolerance)
		j := util.Jaccard(qTri, fnTri)
		score += 4.0 * j

		// 5) recency boost (log-scaled)
		ageDays := float64(max(0, now-cands[i].Mtime)) / 86400.0
		rec := 1.0 / (1.0 + math.Log1p(ageDays)) // newer => closer to 1
		score += 1.5 * rec

		// 6) tiny bonus for shorter filenames (often cleaner)
		score += 0.15 * (1.0 / (1.0 + float64(len(fnNorm))/40.0))

		cands[i].Score = score
	}

	sort.SliceStable(cands, func(i, j int) bool {
	// Primary: latest modified
	if cands[i].Mtime != cands[j].Mtime {
		return cands[i].Mtime > cands[j].Mtime
	}
	// Secondary: relevance score
	return cands[i].Score > cands[j].Score
	})


	if len(cands) > opts.Limit {
		cands = cands[:opts.Limit]
	}
	return cands, nil
}

func tokenOverlapCount(a, b []string) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	set := make(map[string]struct{}, len(b))
	for _, t := range b {
		set[t] = struct{}{}
	}
	c := 0
	for _, t := range a {
		if _, ok := set[t]; ok {
			c++
		}
	}
	return c
}

func max(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
