# Findex

A fast, predictable file search tool for macOS that indexes once and searches instantly.

## Why Findex?

Traditional file search methods scan your filesystem every time you search, which is slow and unpredictable as the number of files grows. Tools like Spotlight mix indexing, ranking, and UI behavior in opaque ways.

**Findex separates the problem into two phases:**

1. **Index once** – Scan your filesystem and store lightweight metadata in a local SQLite database
2. **Query instantly** – Search against the database instead of walking the filesystem

This design makes search performance independent of filesystem size, delivering deterministic and blazing-fast results.

## What Findex Stores

For every file discovered during indexing, Findex stores only:

- File path
- File name
- File extension
- Last modified timestamp (mtime)

**It does NOT store:**
- File contents
- File size
- Binary data

This keeps the index small, fast, and simple.

## Features

- ⚡ **Lightning-fast searches** – Query a database instead of scanning directories
- 🎯 **Predictable results** – Ranked by filename match quality and recency
- 🔍 **Interactive file picker** – Uses `fzf` for selection
- 📱 **Open anything** – Files, directories, and macOS applications
- 🕒 **Time-based filtering** – Search files modified within specific timeframes
- 🎛️ **Configurable scope** – Choose exactly which directories to index

## Installation

### 1. Build the Binary

```bash
go build -o findex .
```

### 2. (Optional) Make It Globally Available

```bash
mkdir -p ~/.local/bin
cp findex ~/.local/bin
```

Ensure `~/.local/bin` is in your PATH:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

Add this to your `~/.zshrc` or `~/.bashrc` to make it permanent.

### 3. Install Dependencies

Findex uses `fzf` for interactive file selection:

```bash
brew install fzf
```

## Quick Start

### 1. Index Your Files

Before searching, you must index at least one directory.

**Index your home directory:**

```bash
findex index --root ~
```

**Index home directory and macOS applications (recommended):**

```bash
findex index --root ~ --root /Applications --root /System/Applications
```

This creates a local SQLite database at `~/.findex/index.db`.

### 2. Search Files

**Print search results to terminal:**

```bash
findex q resume
```

**Filter by recency (files modified in last 7 days):**

```bash
findex q --since 7d report
```

### 3. Open Files Interactively

**Launch interactive picker and open selected file:**

```bash
findex open resume
```

This uses `fzf` to let you choose from results and opens your selection with macOS `open` command.

### 4. Open Applications

After indexing `/Applications`:

```bash
findex open chrome
findex open slack
findex open terminal
```

Applications are treated just like files and opened using the macOS `open` command.

## Commands

### `index`

Build or rebuild the file index.

```bash
findex index --root <directory> [--root <directory>...]
```

**Options:**
- `--root` – Directory to index (can be specified multiple times)

**Example:**

```bash
findex index --root ~ --root /Applications
```

### `q` (query)

Search indexed files and print results.

```bash
findex q [flags] <search-term>
```

**Options:**
- `--since` – Filter by modification time (e.g., `7d`, `2w`, `1m`)

**Examples:**

```bash
findex q presentation
findex q --since 30d budget
```

### `open`

Search indexed files and open the selected result interactively.

```bash
findex open [flags] <search-term>
```

**Options:**
- `--since` – Filter by modification time

**Examples:**

```bash
findex open todo
findex open --since 7d meeting-notes
```

## How It Works

### Indexing Phase

1. Findex walks the specified directory trees
2. For each file, it extracts: path, name, extension, and mtime
3. This metadata is stored in a local SQLite database (`~/.findex/index.db`)

### Search Phase

1. When you search, Findex queries the SQLite database
2. Results are ranked based on:
   - Filename match quality
   - Recency (mtime)
3. Top results are returned instantly without touching the filesystem

### Opening Phase

1. Selected files are passed to the macOS `open` command
2. This works for:
   - Regular files
   - Directories
   - macOS `.app` bundles

## Design Principles

1. **Filesystem traversal is expensive** – Avoid repeated scans by indexing once
2. **File metadata is small and stable** – Store only what's needed to locate files
3. **Database queries are fast** – Structured data beats filesystem traversal
4. **Index once, query many** – One-time indexing enables unlimited fast searches

## Why This Design Matters

- Search performance is **independent of filesystem size**
- Results are **deterministic and predictable**
- Indexing scope is **explicit and configurable**
- The system is **extensible** (watch mode, ignore rules, ranking tweaks)
- Behaves like a **real system, not a script**

## Future Enhancements

Potential improvements for Findex:

- File watching to keep index up-to-date automatically
- Ignore patterns (e.g., `.gitignore` style rules)
- Configurable ranking algorithms
- Content-based search (optional)
- Export/import index functionality

## Contributing

Contributions are welcome! Please open an issue or submit a pull request.

---

**Built for developers who value speed, predictability, and control over their file search experience.**
