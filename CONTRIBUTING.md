# Contributing

This is a small public file-sharing server. Patches that keep it small
are welcome. Auth, accounts, and a second storage backend are a different
program.

## Before you write code

Open an [issue](https://github.com/erfnzdeh/erfanzadeh.ir/issues) if the
change is more than a typo or an obvious bug.

Things to keep:

- **Uploads land in `uploads/`, permanent files in `assets/`.**
  `filepath.Base` on the submitted name is load-bearing. A download that
  can leave those two directories is a security bug.
- **Eviction is download-count among files older than seven days**, then
  oldest overall. Tests for `pickVictim` live in `main_test.go`. Change
  the rule there first.
- **The list endpoint is public and includes uploader IPs.** That is
  current behaviour, not an accident. If you hide IPs, say so in the PR;
  do not silently drop the field.

```bash
go test ./...
```

That is the check CI runs. You do not need a live listener.

## Setup

```bash
go test ./...
LISTEN=:8813 go run .
```

`ASSETS_DIR`, `UPLOADS_DIR`, `COUNTS_FILE`, and `IPS_FILE` override the
defaults. Uploads are capped at 10 GB of temp data; the server evicts to
stay under that.

## Pull requests

- One change per PR.
- `gofmt` the Go. Add a test for eviction or path behaviour.
- Match the surrounding comments. They explain why. No em dashes.
- Do not commit `uploads/`, `.downloads.json`, or `.ips.json` from a
  machine that has seen real traffic.

## Surfaces

| Path | What it is |
|---|---|
| `main.go` | HTTP server: list, upload, download, stats, eviction. |
| `main_test.go` | `pickVictim` cases. |
| `index.html` | Drag-and-drop UI, embedded at build time. |

Contributions are under the same MIT license as the rest of the repo.
