package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"findex/internal/db"
	"findex/internal/indexer"
	"findex/internal/search"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	cmd := os.Args[1]

	switch cmd {
	case "index":
		runIndex(os.Args[2:])
	case "q":
		runQuery(os.Args[2:], false)
	case "open":
		runPick(os.Args[2:], false)
	case "reveal":
		runPick(os.Args[2:], true)
	default:
		usage()
		os.Exit(2)
	}
}

func usage() {
	fmt.Println(`findex — fast filename index + search

Usage:
  findex index  [--db <path>] [--root <path> ...] [--pdf] [--img]
  findex q      [--db <path>] [--limit N] [--since 7d] <query>
  findex open   [--db <path>] [--since 7d] <query>
  findex reveal [--db <path>] [--since 7d] <query>

Examples:
  findex index --root ~ --pdf
  findex q passport --since 7d
  findex open invoice --since 24h
`)
}

/* ---------- paths ---------- */

func defaultDBPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".findex", "index.db")
}

func ensureDir(p string) error {
	return os.MkdirAll(filepath.Dir(p), 0o755)
}

/* ---------- shared flags ---------- */

func parseCommon(fs *flag.FlagSet) (dbPath string, roots multiFlag, onlyPDF bool, onlyIMG bool) {
	fs.StringVar(&dbPath, "db", defaultDBPath(), "path to sqlite db")
	fs.Var(&roots, "root", "root to index (repeatable). default: ~")
	fs.BoolVar(&onlyPDF, "pdf", false, "filter to PDFs (index/search)")
	fs.BoolVar(&onlyIMG, "img", false, "filter to images (index/search)")
	return
}

/* ---------- index ---------- */

func runIndex(args []string) {
	fs := flag.NewFlagSet("index", flag.ExitOnError)
	dbPath, roots, onlyPDF, onlyIMG := parseCommon(fs)
	follow := fs.Bool("follow", false, "follow symlinks")
	hidden := fs.Bool("hidden", false, "include hidden files")
	fs.Parse(args)

	if err := ensureDir(dbPath); err != nil {
		fatal(err)
	}

	d, err := db.Open(dbPath)
	if err != nil {
		fatal(err)
	}
	defer d.Close()

	rootList := []string(roots)
	if len(rootList) == 0 {
		rootList = []string{"~"}
	}

	extFilter := make(map[string]struct{})
	if onlyPDF {
		extFilter["pdf"] = struct{}{}
	}
	if onlyIMG {
		for _, e := range []string{"png", "jpg", "jpeg", "heic", "webp", "gif", "tiff"} {
			extFilter[e] = struct{}{}
		}
	}

	var extMap map[string]struct{}
	if len(extFilter) > 0 {
		extMap = extFilter
	}

	tx, err := d.BeginTx()
	if err != nil {
		fatal(err)
	}

	gen := time.Now().Unix()
	ix := indexer.New(indexer.Options{
		Roots:          rootList,
		IncludeHidden:  *hidden,
		FollowSymlinks: *follow,
		OnlyExtensions: extMap,
		BatchSize:      1000,
	})

	start := time.Now()
	n, err := ix.Run(tx, gen)
	if err != nil {
		_ = tx.Rollback()
		fatal(err)
	}
	if err := tx.Commit(); err != nil {
		fatal(err)
	}

	fmt.Printf("Indexed %d files in %s\n", n, time.Since(start).Round(time.Millisecond))
}

/* ---------- query ---------- */

func runQuery(args []string, quiet bool) {
	fs := flag.NewFlagSet("q", flag.ExitOnError)
	dbPath, _, onlyPDF, onlyIMG := parseCommon(fs)
	limit := fs.Int("limit", 30, "max results")
	sinceFlag := fs.String("since", "", "time window like 24h, 7d, 2w")
	fs.Parse(args)

	q := strings.Join(fs.Args(), " ")
	if strings.TrimSpace(q) == "" {
		fatal(fmt.Errorf("query required"))
	}

	d, err := db.Open(dbPath)
	if err != nil {
		fatal(err)
	}
	defer d.Close()

	opts := search.DefaultQueryOptions()
	opts.Limit = *limit
	opts.ExtFilter = buildExtFilter(onlyPDF, onlyIMG)

	res, err := search.Search(d.Conn, q, opts)
	if err != nil {
		fatal(err)
	}

	res = applySinceFilter(res, sinceFlag)

	if quiet {
		for _, r := range res {
			fmt.Println(r.Path)
		}
		return
	}

	for _, r := range res {
		fmt.Printf("%.2f  %s\n", r.Score, r.Path)
	}
}

/* ---------- interactive open / reveal ---------- */

func runPick(args []string, reveal bool) {
	fs := flag.NewFlagSet("open", flag.ExitOnError)
	dbPath, _, onlyPDF, onlyIMG := parseCommon(fs)
	limit := fs.Int("limit", 80, "how many to pass to picker")
	sinceFlag := fs.String("since", "", "time window like 24h, 7d, 2w")
	fs.Parse(args)

	q := strings.Join(fs.Args(), " ")
	if strings.TrimSpace(q) == "" {
		fatal(fmt.Errorf("query required"))
	}

	d, err := db.Open(dbPath)
	if err != nil {
		fatal(err)
	}
	defer d.Close()

	opts := search.DefaultQueryOptions()
	opts.Limit = *limit
	opts.ExtFilter = buildExtFilter(onlyPDF, onlyIMG)

	res, err := search.Search(d.Conn, q, opts)
	if err != nil {
		fatal(err)
	}

	res = applySinceFilter(res, sinceFlag)

	if len(res) == 0 {
		fmt.Println("No matches.")
		return
	}

	choice, err := pickWithFzf(res)
	if err != nil {
		fatal(err)
	}
	if strings.TrimSpace(choice) == "" {
		return
	}

	var cmd *exec.Cmd
	if reveal {
		cmd = exec.Command("open", "-R", choice)
	} else {
		cmd = exec.Command("open", choice)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	_ = cmd.Run()
}

/* ---------- fzf ---------- */

func pickWithFzf(res []search.Result) (string, error) {
	fzfPath, err := exec.LookPath("fzf")
	if err != nil {
		return "", fmt.Errorf("fzf not found. Install with: brew install fzf")
	}

	cmd := exec.Command(fzfPath, "--prompt", "🔍 findex > ", "--height", "40%", "--reverse")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return "", err
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		return "", err
	}

	go func() {
		w := bufio.NewWriter(stdin)
		for _, r := range res {
			fmt.Fprintln(w, r.Path)
		}
		w.Flush()
		stdin.Close()
	}()

	out, _ := io.ReadAll(stdout)
	_ = cmd.Wait()
	return strings.TrimSpace(string(out)), nil
}

/* ---------- helpers ---------- */

type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func buildExtFilter(pdf, img bool) map[string]struct{} {
	if !pdf && !img {
		return nil
	}
	m := make(map[string]struct{})
	if pdf {
		m["pdf"] = struct{}{}
	}
	if img {
		for _, e := range []string{"png", "jpg", "jpeg", "heic", "webp", "gif", "tiff"} {
			m[e] = struct{}{}
		}
	}
	return m
}

/* ---------- since filter ---------- */

func parseSince(s string) (int64, error) {
	if s == "" {
		return 0, nil
	}

	unit := s[len(s)-1]
	value, err := strconv.Atoi(s[:len(s)-1])
	if err != nil {
		return 0, fmt.Errorf("invalid --since format (use 24h, 7d, 2w)")
	}

	var dur time.Duration
	switch unit {
	case 'h':
		dur = time.Duration(value) * time.Hour
	case 'd':
		dur = time.Duration(value) * 24 * time.Hour
	case 'w':
		dur = time.Duration(value) * 7 * 24 * time.Hour
	default:
		return 0, fmt.Errorf("invalid --since unit (use h, d, or w)")
	}

	return time.Now().Add(-dur).Unix(), nil
}

func applySinceFilter(res []search.Result, sinceFlag *string) []search.Result {
	cutoff, err := parseSince(*sinceFlag)
	if err != nil {
		fatal(err)
	}

	if cutoff == 0 {
		return res
	}

	filtered := res[:0]
	for _, r := range res {
		if r.Mtime >= cutoff {
			filtered = append(filtered, r)
		}
	}
	return filtered
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "Error:", err)
	os.Exit(1)
}
