# Windows StayWakeBlackScreen

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
start_enabled: true
```

| Option | Default | Description |
| --- | --- | --- |
| `idle_minutes` | `3` | Minutes of inactivity (no real keyboard/mouse input) before the screen blacks out. |
| `heartbeat_seconds` | `5` | While blacked out, how often (seconds) the program toggles Caps Lock as a harmless "still alive" signal that keeps Windows from treating the session as idle. |
| `start_enabled` | `true` | Whether the idle guard is active as soon as the program starts. Set to `false` to start paused — no sleep blocking, no blackout — until enabled from the tray menu. |

Edit a value and restart the program to apply it. Each option also has a
matching command-line flag (`-idle-minutes`, `-heartbeat-seconds`,
`-start-enabled`) which, if passed, overrides the config file for that
run only. Config files from older versions may still contain a
`poll_ms` line; it's no longer used (nothing polls any more) and is
ignored.

**Tray icon:** a monitor glyph (black frame, screen, and stand) appears in
the notification area while actively guarding, and the same glyph **greyed
out with a diagonal red strike** through it while disabled.
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

### `Setup_StayWakeBlackScreenIdle.exe`

Run it with no arguments (e.g. double-click it) and it shows an
interactive menu:

```
Windows StayWakeBlackScreen - Setup

  1) Install / update
  2) Uninstall

Choose an option [1-2] (installing/updating automatically in 5 seconds if nothing is chosen):
```

If nothing is chosen within 5 seconds of the first prompt, it goes ahead
with **Install / update** on its own — so double-clicking it and walking
away still gets the tool installed/updated. Typing anything (even an
invalid choice) cancels the countdown for the rest of that run. When the
action was auto-chosen this way, the window also closes itself 3 seconds
after finishing (instead of waiting for Enter) — nobody was there to
pick it, so there's likely nobody there to dismiss it either. If that
unattended run fails, though, the window stays open and waits for Enter,
so the error is still on screen when you come back.

**Install / update** downloads the latest released
`StayWakeBlackScreenIdle.exe`, installs it to
`%LOCALAPPDATA%\StayWakeBlackScreen\`, registers it to autostart at
login, and (re)starts it — stopping any already-running copy first so the
file can be replaced. Safe to re-run any time to update: it always ends
up with exactly **one** autostart entry (a single named registry value —
re-running never creates a duplicate) and exactly **one** running
instance:
- Setup terminates any already-running copy before replacing the file
  and starting the new one.
- `StayWakeBlackScreenIdle.exe` also refuses to start a second copy of
  itself, via a named mutex — belt and suspenders even if it's ever
  launched some other way while already running.

It only ever downloads the idle variant (`StayWakeBlackScreenIdle.exe`)
— `StayWakeBlackScreen.exe` is left as a manual, run-when-you-want-it
tool.

**Uninstall** removes the autostart registry entry, stops any running
copy of `StayWakeBlackScreenIdle.exe` or `StayWakeBlackScreen.exe`, and
deletes the installed files.

For scripted use, `-mode install` or `-mode uninstall` skips the menu
entirely. Other flags: `-install-dir <path>` (override the install
location), `-github-token <token>` (avoid GitHub's unauthenticated API
rate limit, install only), `-no-launch` (install/update without starting
it now, install only), `-no-autostart` (skip the registry entry, install
only), `-keep-files` (remove autostart and stop the process, but leave
the installed files in place, uninstall only).

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
- Nothing polls. The idle guard sets a single timer for the moment the
  idle threshold would be reached (re-arming it for the rest if there was
  input in the meantime), and the input hook wakes the program directly
  when Escape is pressed.
- Setup downloads through WinINet, Windows' own HTTP stack, so it uses
  your system proxy settings and Windows' certificate store.
- **Ctrl+Alt+Del always remains available** — Windows never lets any hook
  suppress it — so it's a hard escape hatch no matter what else goes wrong.
- The tray icon is drawn at runtime (no image assets) as a 32×32
  alpha-blended `HICON` via `CreateDIBSection`/`CreateIconIndirect`.

## Logging

Off by default (nothing is written, no console output, no popups). Pass
`-enable-logging` to write diagnostics to a `.log` file next to the exe,
for troubleshooting only.

## Building from source

Requires Go 1.26+ (matching the `go` directive in `go.mod`).

```
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-H=windowsgui -s -w" -o StayWakeBlackScreen.exe ./cmd/staywakeblackscreen
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-H=windowsgui -s -w" -o StayWakeBlackScreenIdle.exe ./cmd/staywakeblackscreenidle
GOOS=windows GOARCH=amd64 go build -trimpath -ldflags "-s -w" -o Setup_StayWakeBlackScreenIdle.exe ./cmd/stay-wake-setup
```

Run the tests with `go test ./...`. They cover the OS-independent parts
(config loading, the icon glyph and generator, the Setup menu, and the
timing limits) and run on any platform.

`StayWakeBlackScreenIdle.exe`'s tray tooltip shows a version string,
stamped in via `-X main.version=v1.2.3` appended to its `-ldflags` (the
release build does this from the pushed tag); a build without it just
shows `dev`.

`-H=windowsgui` is what makes the two blackout programs run without a
console window; the setup tool is left as a normal console program so
its progress (and menu) is visible when run from a terminal.

All three `.exe` files also carry the same monitor glyph as their
Explorer/taskbar file icon (the same one `StayWakeBlackScreenIdle.exe`
draws at runtime for its tray icon), embedded via a
`rsrc_windows_amd64.syso` resource file already committed in each `cmd/`
directory - `go build` picks these up automatically, no extra step
needed. If the glyph in `internal/monitoricon` ever changes, regenerate
them with:

```
go run ./tools/genicon monitor.ico
go run github.com/akavel/rsrc@latest -ico monitor.ico -arch amd64 -o cmd/staywakeblackscreen/rsrc_windows_amd64.syso
go run github.com/akavel/rsrc@latest -ico monitor.ico -arch amd64 -o cmd/staywakeblackscreenidle/rsrc_windows_amd64.syso
go run github.com/akavel/rsrc@latest -ico monitor.ico -arch amd64 -o cmd/stay-wake-setup/rsrc_windows_amd64.syso
```

Package layout:

```
internal/blackout/       Win32 bindings: sleep/display block, input
                          hooks, overlay windows, DPI, idle detection
internal/tray/            Notification-area icon, menu, drawn icon
internal/win32/           Win32 declarations shared by blackout and tray
internal/monitoricon/     The monitor glyph's geometry, shared by the
                          tray icon and the generated .exe file icon
internal/singleinstance/  Named-mutex single-instance guard
internal/setup/           Install/uninstall logic shared by Setup_StayWakeBlackScreenIdle.exe
internal/setupmenu/       Setup's interactive console menu (OS-independent)
internal/config/          Loads, and on first run creates, config.yaml
internal/applog/          Opt-in diagnostics log next to the exe
tools/genicon/            Renders internal/monitoricon as a .ico file
cmd/staywakeblackscreen/     StayWakeBlackScreen.exe
cmd/staywakeblackscreenidle/ StayWakeBlackScreenIdle.exe
cmd/stay-wake-setup/         Setup_StayWakeBlackScreenIdle.exe
```

## Prebuilt releases

The GitHub Actions workflow (`.github/workflows/build.yml`) cross-compiles
all three `.exe` files and publishes them to a [GitHub Release](../../releases)
whenever a `v*` tag is pushed (or the workflow is triggered manually). Grab
the latest from the [Releases](../../releases) page, or just run
`Setup_StayWakeBlackScreenIdle.exe` and choose "Install / update" to fetch and install
`StayWakeBlackScreenIdle.exe` automatically.

Every push and pull request to `main` also runs `.github/workflows/ci.yml`:
the tests, vet, and a compile of all three `.exe` files, without
publishing anything.

## Requirements

- Windows (Windows Forms-equivalent GUI + Win32 hooks are Windows-only)

## Safety notes

- These tools intentionally block **all** keyboard and mouse input while
  blacked out. Escape is the only key that ends it; Ctrl+Alt+Del is the
  Windows-guaranteed hard fallback if anything goes wrong.
- Not intended to bypass any organizational policy — use only on machines
  and in contexts where preventing idle-lock/sleep is something you're
  authorized to do.

## License

MIT - see [LICENSE](LICENSE).
