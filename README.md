# StayWakeBlackScreen (Windows)

Two small PowerShell tools that keep a Windows PC awake without letting the
screen show anything — instead of turning the monitor off, they cover every
screen with a real black window and keep telling Windows the display is
"required" (on). This avoids the session-lock-on-wake behavior that a real
monitor power-off can trigger (especially with "require sign-in on wake"
enabled).

Both are GUI-only tools: no console, no popups, nothing written to disk
unless you explicitly ask for a log.

## Scripts

### `StayWakeBlackScreen.ps1`

Blacks out every screen and blocks **all** keyboard and mouse input
system-wide immediately when run. The only way out is the **Escape** key.

```powershell
powershell -ExecutionPolicy Bypass -File .\StayWakeBlackScreen.ps1
# Optional:
powershell -ExecutionPolicy Bypass -File .\StayWakeBlackScreen.ps1 -HeartbeatSeconds 5 -EnableLogging
```

### `StayWakeBlackScreenIdle.ps1`

Runs quietly in the background — no black screen, no input blocking —
preventing sleep/lock, until the PC has been genuinely idle (no real
keyboard/mouse activity) for `-IdleMinutes` (default 5). At that point it
blacks out and blocks input exactly like the script above. Pressing
**Escape** dismisses the blackout and restores input, but the script keeps
running and the idle countdown restarts — it will black out again after
another idle period, indefinitely. It does not exit on its own; stop it via
Task Manager, `Stop-Process`, or Ctrl+C in a visible console.

```powershell
powershell -STA -ExecutionPolicy Bypass -File .\StayWakeBlackScreenIdle.ps1
# Optional:
powershell -STA -ExecutionPolicy Bypass -File .\StayWakeBlackScreenIdle.ps1 -IdleMinutes 5 -HeartbeatSeconds 5 -EnableLogging
```

## How it works

- `SetThreadExecutionState` with `ES_SYSTEM_REQUIRED | ES_DISPLAY_REQUIRED`
  tells Windows sleep and display-off must not happen.
- A borderless, topmost, black `Form` is created per monitor (via
  `Screen.AllScreens`) instead of powering the display off, so Windows never
  sees a display-off/idle transition and has no reason to lock the session.
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

## Logging

Off by default (nothing is written, no output, no popups). Pass
`-EnableLogging` to write diagnostics to a `.log` file next to the
script/exe, for troubleshooting only.

## Building the .exe

Both scripts use Windows Forms, which requires the STA apartment. A plain
`powershell.exe` console is STA by default, but a `ps2exe`-compiled exe is
**not** unless told to be — always compile with `-STA`:

```powershell
Install-Module -Name ps2exe -Scope CurrentUser
Invoke-ps2exe -inputFile StayWakeBlackScreen.ps1 -outputFile StayWakeBlackScreen.exe -STA -noConsole
Invoke-ps2exe -inputFile StayWakeBlackScreenIdle.ps1 -outputFile StayWakeBlackScreenIdle.exe -STA -noConsole
```

`-noConsole` is optional but fits these being GUI-only tools.

## Prebuilt releases

The GitHub Actions workflow in this repo (`.github/workflows/build.yml`)
builds both `.exe` files on `windows-latest` with `ps2exe` on every push to
`main` and on every `v*` tag, and attaches them to a
[GitHub Release](../../releases) for tagged pushes. Grab the latest exe
from the [Releases](../../releases) page rather than building it yourself.

## Requirements

- Windows (Windows Forms + Win32 hooks are Windows-only)
- PowerShell 5.1+ (Windows PowerShell or PowerShell 7+)

## Safety notes

- These tools intentionally block **all** keyboard and mouse input while
  blacked out. Escape is the only key that ends it; Ctrl+Alt+Del is the
  Windows-guaranteed hard fallback if anything goes wrong.
- Not intended to bypass any organizational policy — use only on machines
  and in contexts where preventing idle-lock/sleep is something you're
  authorized to do.
