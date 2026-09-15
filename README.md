# StayWakeBlackScreen (Windows)

Windows tools that keep a PC awake without letting the screen show
anything — instead of turning the monitor off, they cover every screen
with a real black window and keep telling Windows the display is
"required" (on). This avoids the session-lock-on-wake behavior that a real
monitor power-off can trigger (especially with "require sign-in on wake"
enabled).

Written in Go, calling the relevant Win32 APIs directly
(`golang.org/x/sys/windows`) — no .NET, no external GUI toolkit, no
runtime dependency beyond what Windows itself ships. Each program is a
single self-contained `.exe`.

## Programs

### `StayWakeBlackScreen.exe`

Blacks out every screen and blocks **all** keyboard and mouse input
system-wide immediately when run. The only way out is the **Escape** key.

```
StayWakeBlackScreen.exe
StayWakeBlackScreen.exe -heartbeat-seconds 5 -enable-logging
```

### `StayWakeBlackScreenIdle.exe`

Runs quietly in the background — no black screen, no input blocking, and
a tray icon — preventing sleep/lock, until the PC has been genuinely idle
(no real keyboard/mouse activity) for `idle_minutes` (default 3). At that
point it blacks out and blocks input exactly like the program above.
Pressing **Escape** dismisses the blackout and restores input, but it
keeps running and the idle countdown restarts — it will black out again
after another idle period, indefinitely.

```
StayWakeBlackScreenIdle.exe
StayWakeBlackScreenIdle.exe -idle-minutes 3 -heartbeat-seconds 5 -enable-logging
```

**Config file:** on first run it creates
`%LOCALAPPDATA%\StayWakeBlackScreen\config.yaml`:

```yaml
idle_minutes: 3
heartbeat_seconds: 5
poll_ms: 250
start_enabled: true
```

| Option | Default | Description |
| --- | --- | --- |
| `idle_minutes` | `3` | Minutes of inactivity (no real keyboard/mouse input) before the screen blacks out. |
| `heartbeat_seconds` | `5` | While blacked out, how often (seconds) the program toggles Caps Lock as a harmless "still alive" signal that keeps Windows from treating the session as idle. |
| `poll_ms` | `250` | How often (milliseconds) the program checks for idle time and for the Escape key while blacked out. Lower is more responsive but uses slightly more CPU. |
| `start_enabled` | `true` | Whether the idle guard is active as soon as the program starts. Set to `false` to start paused — no sleep blocking, no blackout — until enabled from the tray menu. |

Edit a value and restart the program to apply it. Each option also has a
matching command-line flag (`-idle-minutes`, `-heartbeat-seconds`,
`-poll-ms`, `-start-enabled`) which, if passed, overrides the config file
for that run only.

**Tray icon:** a monitor glyph (black frame and stand) appears in the
notification area, with a **black screen** while actively guarding and a
**light grey screen** while disabled.
- **Left-click** toggles it on/off.
- **Right-click** opens a menu: Enable, Disable, Configure, Exit.

Disabling immediately restores input (if blacked out) and lets Windows
sleep/lock normally again, without stopping the process — re-enable any
time from the same menu. **Configure** opens `config.yaml` in whatever
application Windows has associated with `.yaml` files (Notepad by
default), so you can change the settings above without a terminal. Exit
stops it for good.

This program does not exit on its own otherwise. To stop it: the tray
menu's *Exit*, Task Manager/`taskkill`, or the installer (which does this
automatically when updating).

### `Install_StayWake.exe`

Downloads the latest released `StayWakeBlackScreenIdle.exe`, installs it
to `%LOCALAPPDATA%\StayWakeBlackScreen\`, registers it to autostart at
login, and (re)starts it — stopping any already-running copy first so the
file can be replaced.

```
Install_StayWake.exe
```

Safe to re-run any time to update: it always ends up with exactly **one**
autostart entry (a single named registry value — re-running never creates
a duplicate) and exactly **one** running instance:
- The installer terminates any already-running copy before replacing the
  file and starting the new one.
- `StayWakeBlackScreenIdle.exe` also refuses to start a second copy of
  itself, via a named mutex — belt and suspenders even if it's ever
  launched some other way while already running.

Flags: `-install-dir <path>` (override the install location),
`-github-token <token>` (avoid GitHub's unauthenticated API rate limit),
`-no-launch` (install/update without starting it now), `-no-autostart`
(skip the registry entry). It only ever downloads the idle variant
(`StayWakeBlackScreenIdle.exe`) — `StayWakeBlackScreen.exe` is left as a
manual, run-when-you-want-it tool.

### `Uninstall_StayWake.exe`

Reverses what `Install_StayWake.exe` did: removes the autostart registry
entry, stops any running copy of `StayWakeBlackScreenIdle.exe` or
`StayWakeBlackScreen.exe`, and deletes the installed files.

```
Uninstall_StayWake.exe
```

Flags: `-install-dir <path>` (override the install location, same default
as the installer: `%LOCALAPPDATA%\StayWakeBlackScreen`), `-keep-files`
(remove autostart and stop the process, but leave the installed files in
place).

## How it works

- `SetThreadExecutionState` with `ES_SYSTEM_REQUIRED | ES_DISPLAY_REQUIRED`
  tells Windows sleep and display-off must not happen.
- A borderless, topmost, black window is created per monitor instead of
  powering the display off, so Windows never sees a display-off/idle
  transition and has no reason to lock the session.
- Low-level `WH_KEYBOARD_LL` / `WH_MOUSE_LL` hooks swallow all keyboard and
  mouse input system-wide while blacked out — nothing reaches any window,
  including the tool's own. Only Escape is detected (via the hook) to end
  the blackout.
- A periodic synthetic Caps Lock toggle acts as an activity heartbeat some
  environments use to avoid idle/lock detection; the hook lets only this
  specific synthetic keystroke through so the Caps Lock LED actually
  updates, and the final state is cleaned up on exit.
- **Ctrl+Alt+Del always remains available** — Windows never lets any hook
  suppress it — so it's a hard escape hatch no matter what else goes wrong.
- The tray icon is drawn at runtime (no image assets) as a 32×32
  alpha-blended `HICON` via `CreateDIBSection`/`CreateIconIndirect`.

## Logging

Off by default (nothing is written, no console output, no popups). Pass
`-enable-logging` to write diagnostics to a `.log` file next to the exe,
for troubleshooting only.

## Building from source

Requires Go 1.22+.

```
GOOS=windows GOARCH=amd64 go build -ldflags "-H=windowsgui -s -w" -o StayWakeBlackScreen.exe ./cmd/staywakeblackscreen
GOOS=windows GOARCH=amd64 go build -ldflags "-H=windowsgui -s -w" -o StayWakeBlackScreenIdle.exe ./cmd/staywakeblackscreenidle
GOOS=windows GOARCH=amd64 go build -ldflags "-s -w" -o Install_StayWake.exe ./cmd/stay-wake-install
GOOS=windows GOARCH=amd64 go build -ldflags "-s -w" -o Uninstall_StayWake.exe ./cmd/stay-wake-uninstall
```

`StayWakeBlackScreenIdle.exe`'s tray tooltip shows a version string,
stamped in via `-X main.version=v1.2.3` appended to its `-ldflags` (the
release build does this from the pushed tag); a build without it just
shows `dev`.

`-H=windowsgui` is what makes the two blackout programs run without a
console window; the installer and uninstaller are left as normal console
programs so their progress is visible when run from a terminal.

Package layout:

```
internal/blackout/       Win32 bindings: sleep/display block, input
                          hooks, overlay windows, DPI, idle detection
internal/tray/            Notification-area icon, menu, drawn icon
internal/singleinstance/  Named-mutex single-instance guard
cmd/staywakeblackscreen/     StayWakeBlackScreen.exe
cmd/staywakeblackscreenidle/ StayWakeBlackScreenIdle.exe
cmd/stay-wake-install/       Install_StayWake.exe
cmd/stay-wake-uninstall/     Uninstall_StayWake.exe
```

## Prebuilt releases

The GitHub Actions workflow (`.github/workflows/build.yml`) cross-compiles
all three `.exe` files on every push to `main` and on every `v*` tag, and
attaches them to a [GitHub Release](../../releases) for tagged pushes. Grab
the latest from the [Releases](../../releases) page, or just run
`Install_StayWake.exe` to fetch and install `StayWakeBlackScreenIdle.exe`
automatically.

## Requirements

- Windows (Windows Forms-equivalent GUI + Win32 hooks are Windows-only)

## Safety notes

- These tools intentionally block **all** keyboard and mouse input while
  blacked out. Escape is the only key that ends it; Ctrl+Alt+Del is the
  Windows-guaranteed hard fallback if anything goes wrong.
- Not intended to bypass any organizational policy — use only on machines
  and in contexts where preventing idle-lock/sleep is something you're
  authorized to do.
