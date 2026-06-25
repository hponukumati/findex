package search

import (
	"database/sql"
	"fmt"
	"math"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"

	"findex/internal/util"
)

type QueryOptions struct {
	Limit      int
	ExtFilter  map[string]struct{} // e.g. {"pdf":{}}
	Shortlist  int                 // how many candidates to pull from DB
	RelevanceK int                 // how many top-scoring candidates to keep before ranking by recency
}

type Result struct {
	Path       string
	Filename   string
	Ext        string
	Mtime      int64
	LastOpened int64
	Size       int64
	Score      float64
}

func DefaultQueryOptions() QueryOptions {
	return QueryOptions{
		Limit:      30,
		Shortlist:  800, // tune: 200–2000 depending on disk size
		RelevanceK: 50,  // only the top-K relevant candidates get a real "last opened" lookup
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
	if opts.RelevanceK <= 0 {
		opts.RelevanceK = 50
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

	// Stage 1: keep only the most relevant candidates. Sorting the full
	// shortlist by recency would let a weak match with a recent mtime
	// outrank a strong match that's older, so relevance gates first.
	sort.SliceStable(cands, func(i, j int) bool {
		return cands[i].Score > cands[j].Score
	})
	if len(cands) > opts.RelevanceK {
		cands = cands[:opts.RelevanceK]
	}

	// Stage 2: within the relevant set, rank by true "last opened" time
	// (falling back to mtime when that's unavailable), so the freshest
	// relevant file surfaces first.
	for i := range cands {
		cands[i].LastOpened = lastOpenedUnix(cands[i].Path, cands[i].Mtime)
	}

	sort.SliceStable(cands, func(i, j int) bool {
		if cands[i].LastOpened != cands[j].LastOpened {
			return cands[i].LastOpened > cands[j].LastOpened
		}
		return cands[i].Score > cands[j].Score
	})

	if len(cands) > opts.Limit {
		cands = cands[:opts.Limit]
	}
	return cands, nil
}

// lastOpenedUnix returns the file's true last-opened time via Spotlight
// metadata (kMDItemLastUsedDate) on macOS, falling back to mtime when
// Spotlight has no record for the file or we're not on macOS.
func lastOpenedUnix(path string, mtimeFallback int64) int64 {
	if runtime.GOOS != "darwin" {
		return mtimeFallback
	}
	t, err := mdlsLastUsedDate(path)
	if err != nil || t.IsZero() {
		return mtimeFallback
	}
	return t.Unix()
}

// mdlsLastUsedDateLayout matches mdls -raw output, e.g. "2024-06-20 14:23:11 +0000".
const mdlsLastUsedDateLayout = "2006-01-02 15:04:05 -0700"

func mdlsLastUsedDate(path string) (time.Time, error) {
	out, err := exec.Command("mdls", "-raw", "-name", "kMDItemLastUsedDate", path).Output()
	if err != nil {
		return time.Time{}, err
	}
	raw := strings.TrimSpace(string(out))
	if raw == "" || raw == "(null)" {
		return time.Time{}, nil
	}
	return time.Parse(mdlsLastUsedDateLayout, raw)
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
