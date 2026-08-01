# macOS LaunchAgents

Per-user LaunchAgent templates for `cellard` and (optionally) `cellar-gateway`.
They are installed by `make install` / `install.sh` into `~/Library/LaunchAgents`
with placeholders substituted; install does **not** load the agents.

## Placeholders

| Placeholder | Typical value | Purpose |
| ----------- | ------------- | ------- |
| `@BINDIR@` | `$HOME/.local/bin` | Directory containing `cellard` / `cellar-gateway` |
| `@DATA_DIR@` | `$HOME/.cellar` | Cellar data directory (`--data-dir`) |
| `@LOG_DIR@` | `$HOME/Library/Logs/cellar` | Stdout/stderr log files |
| `@HOME@` | `$HOME` | Used for `DOCKER_HOST` (Docker Desktop user socket) |

## Manual install

```bash
BINDIR="$HOME/.local/bin"
DATA_DIR="$HOME/.cellar"
LOG_DIR="$HOME/Library/Logs/cellar"
HOME_DIR="$HOME"
AGENT_DIR="$HOME/Library/LaunchAgents"

mkdir -p "$AGENT_DIR" "$LOG_DIR"

sed -e "s|@BINDIR@|$BINDIR|g" \
    -e "s|@DATA_DIR@|$DATA_DIR|g" \
    -e "s|@LOG_DIR@|$LOG_DIR|g" \
    -e "s|@HOME@|$HOME_DIR|g" \
    com.prodioslabs.cellard.plist \
  > "$AGENT_DIR/com.prodioslabs.cellard.plist"

sed -e "s|@BINDIR@|$BINDIR|g" \
    -e "s|@DATA_DIR@|$DATA_DIR|g" \
    -e "s|@LOG_DIR@|$LOG_DIR|g" \
    -e "s|@HOME@|$HOME_DIR|g" \
    com.prodioslabs.cellar-gateway.plist \
  > "$AGENT_DIR/com.prodioslabs.cellar-gateway.plist"

launchctl bootstrap "gui/$(id -u)" "$AGENT_DIR/com.prodioslabs.cellard.plist"
# optional:
launchctl bootstrap "gui/$(id -u)" "$AGENT_DIR/com.prodioslabs.cellar-gateway.plist"
```

Unload with:

```bash
launchctl bootout "gui/$(id -u)/com.prodioslabs.cellard"
launchctl bootout "gui/$(id -u)/com.prodioslabs.cellar-gateway"
```
