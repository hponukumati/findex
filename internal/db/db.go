package db

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

type DB struct {
	Conn *sql.DB
}

func Open(path string) (*DB, error) {
	// WAL for concurrency + speed, NORMAL sync for good durability/perf tradeoff
	dsn := fmt.Sprintf("file:%s?_busy_timeout=5000&_journal_mode=WAL&_synchronous=NORMAL&_foreign_keys=ON", path)
	conn, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1) // SQLite: keep simple, avoid contention
	conn.SetConnMaxLifetime(0)
	conn.SetConnMaxIdleTime(0)

	db := &DB{Conn: conn}
	if err := db.migrate(); err != nil {
		_ = conn.Close()
		return nil, err
	}
	return db, nil
}

func (d *DB) Close() error { return d.Conn.Close() }

func (d *DB) migrate() error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS files (
			id INTEGER PRIMARY KEY,
			path TEXT NOT NULL UNIQUE,
			filename TEXT NOT NULL,
			filename_norm TEXT NOT NULL,
			ext TEXT,
			mtime INTEGER,
			size INTEGER,
			is_dir INTEGER NOT NULL DEFAULT 0,
			seen_gen INTEGER NOT NULL DEFAULT 0
		);`,
		`CREATE INDEX IF NOT EXISTS idx_filename_norm ON files(filename_norm);`,
		`CREATE INDEX IF NOT EXISTS idx_ext ON files(ext);`,
		`CREATE INDEX IF NOT EXISTS idx_mtime ON files(mtime);`,
		`CREATE INDEX IF NOT EXISTS idx_seen_gen ON files(seen_gen);`,
	}
	for _, s := range stmts {
		if _, err := d.Conn.Exec(s); err != nil {
			return err
		}
	}
	return nil
}

type FileRow struct {
	Path         string
	Filename     string
	FilenameNorm string
	Ext          string
	Mtime        int64
	Size         int64
	IsDir        bool
	SeenGen      int64
}

func (d *DB) BeginTx() (*sql.Tx, error) {
	return d.Conn.Begin()
}

func NowUnix() int64 { return time.Now().Unix() }
