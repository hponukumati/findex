package indexer

import (
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"findex/internal/util"
)

type Options struct {
	Roots           []string
	IncludeHidden   bool
	FollowSymlinks  bool
	IgnoreDirs      []string
	OnlyExtensions  map[string]struct{} // optional filter: {"pdf":{}, "png":{}}
	BatchSize       int
}

func DefaultIgnoreDirs() []string {
	return []string{
		".git", "node_modules", "Library", "Caches", ".Trash", ".Trash-1000",
		".DS_Store",
	}
}

type Indexer struct {
	opts Options
}

func New(opts Options) *Indexer {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 1000
	}
	if len(opts.IgnoreDirs) == 0 {
		opts.IgnoreDirs = DefaultIgnoreDirs()
	}
	return &Indexer{opts: opts}
}

func (ix *Indexer) Run(tx *sql.Tx, gen int64) (int64, error) {
	if len(ix.opts.Roots) == 0 {
		return 0, errors.New("no roots provided")
	}

	upsertStmt, err := tx.Prepare(`
		INSERT INTO files (path, filename, filename_norm, ext, mtime, size, is_dir, seen_gen)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(path) DO UPDATE SET
		  filename=excluded.filename,
		  filename_norm=excluded.filename_norm,
		  ext=excluded.ext,
		  mtime=excluded.mtime,
		  size=excluded.size,
		  is_dir=excluded.is_dir,
		  seen_gen=excluded.seen_gen
	`)
	if err != nil {
		return 0, err
	}
	defer upsertStmt.Close()

	var indexed int64 = 0

	ignoreSet := make(map[string]struct{}, len(ix.opts.IgnoreDirs))
	for _, d := range ix.opts.IgnoreDirs {
		ignoreSet[d] = struct{}{}
	}

	walkFn := func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			// Permission issues are common; keep going
			return nil
		}

		name := d.Name()

		// Skip ignored dirs early
		if d.IsDir() {
			if _, ok := ignoreSet[name]; ok {
				return fs.SkipDir
			}
			// Hidden dirs (optional)
			if !ix.opts.IncludeHidden && strings.HasPrefix(name, ".") && path != "." {
				return fs.SkipDir
			}
			return nil
		}

		// Hidden files (optional)
		if !ix.opts.IncludeHidden && strings.HasPrefix(name, ".") {
			return nil
		}

		// Extension filter (optional)
		ext := util.ExtLower(name)
		if ix.opts.OnlyExtensions != nil && len(ix.opts.OnlyExtensions) > 0 {
			if _, ok := ix.opts.OnlyExtensions[ext]; !ok {
				return nil
			}
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}

		// Symlink policy
		if info.Mode()&os.ModeSymlink != 0 && !ix.opts.FollowSymlinks {
			return nil
		}

		norm := util.Normalize(name)

		mtime := info.ModTime().Unix()
		size := info.Size()

		_, err = upsertStmt.Exec(path, name, norm, ext, mtime, size, 0, gen)
		if err != nil {
			return nil
		}
		indexed++
		return nil
	}

	for _, root := range ix.opts.Roots {
		root = expandHome(root)
		root = filepath.Clean(root)

		// Ensure root exists
		if _, err := os.Stat(root); err != nil {
			fmt.Fprintf(os.Stderr, "skip root %s: %v\n", root, err)
			continue
		}

		_ = filepath.WalkDir(root, walkFn)
	}

	// Sweep anything not seen in this generation
	if _, err := tx.Exec(`DELETE FROM files WHERE seen_gen <> ?`, gen); err != nil {
		return indexed, err
	}

	return indexed, nil
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}
