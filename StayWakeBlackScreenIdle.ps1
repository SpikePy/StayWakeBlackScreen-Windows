<#
Background idle guard - unlike StayWakeBlackScreen.ps1, this does NOT block
input or show the black screen immediately. It just prevents the PC from
sleeping/locking and keeps running in the background. Only after
-IdleMinutes (default 5) of no real keyboard/mouse activity does it show
the black screen and block all input, exactly like StayWakeBlackScreen.ps1.
Pressing Escape then dismisses the black screen and restores input, but the
script itself keeps running - the idle countdown simply restarts, and it
will black out again after another -IdleMinutes of inactivity, repeating
indefinitely.

This script does not exit on its own. To stop it, end the process (Task
Manager, Stop-Process, or Ctrl+C if run in a visible console).

Usage:
  powershell -STA -ExecutionPolicy Bypass -File .\StayWakeBlackScreenIdle.ps1
  Optional: -IdleMinutes 5 -HeartbeatSeconds 5
  Logging is OFF by default (no file, no output, no popups at all). Pass
  -EnableLogging to write diagnostics to StayWakeBlackScreenIdle.log next
  to the script/exe, for troubleshooting only.

While blacked out: ALL keyboard and mouse input is blocked system-wide (via
low-level WH_KEYBOARD_LL/WH_MOUSE_LL hooks) - nothing reaches any window,
including this one. Only pressing Escape ends the black screen. Note:
Windows never lets any hook suppress Ctrl+Alt+Del, so that combination
always remains available as a hard escape hatch regardless of anything
going wrong here.

Note: This does NOT power off the monitor. Turning it off via
SC_MONITORPOWER makes Windows treat that as an idle/wake transition and can
lock the session (especially with "require sign-in on wake" enabled) even
though SetThreadExecutionState is blocking sleep. Instead, the blackout
covers every screen with a real black window and keeps the display
explicitly "required" (on) the whole time this script runs, so Windows
never sees a display-off event and has no reason to lock.

Compiling with ps2exe:
  This script uses Windows Forms, which requires the STA apartment. A
  plain PowerShell console (powershell.exe) is STA by default, but a
  ps2exe-compiled exe is NOT unless told to be - compile with -STA:
    Invoke-ps2exe -inputFile StayWakeBlackScreenIdle.ps1 -outputFile StayWakeBlackScreenIdle.exe -STA -noConsole
  (-noConsole is optional but fits this being a GUI-only background tool.)

Everything below runs inside one top-level try/catch that never writes to
any output stream and never re-throws, so nothing can surface as console
text or a popup (ps2exe's -noConsole host shows its own error dialog for
any unhandled exception) - all diagnostics go only to the .log file next
to the script/exe.
#>

param(
    [int]$IdleMinutes = 5,
    [int]$HeartbeatSeconds = 5,
    [int]$PollMilliseconds = 250,
    [switch]$EnableLogging
)

$ErrorActionPreference = 'Stop'
$WarningPreference     = 'SilentlyContinue'
$ProgressPreference    = 'SilentlyContinue'

# $PSScriptRoot is unreliable inside a ps2exe-compiled exe, so fall back to
# the running process's own executable path (the exe itself when compiled,
# powershell.exe otherwise - in which case PSCommandPath already caught it).
$scriptDir =
    if ($PSScriptRoot) { $PSScriptRoot }
    elseif ($PSCommandPath) { Split-Path -Parent $PSCommandPath }
    else { Split-Path -Parent ([System.Diagnostics.Process]::GetCurrentProcess().MainModule.FileName) }
$logPath = Join-Path $scriptDir 'StayWakeBlackScreenIdle.log'

function Write-Log([string]$msg)
{
    if (-not $EnableLogging) { return }
    try { "$(Get-Date -Format 'yyyy-MM-dd HH:mm:ss.fff')  $msg" | Out-File -FilePath $logPath -Append -Encoding utf8 } catch { }
}

$forms = @()
$heartbeatTimer = $null
$fastTimer = $null
$cursorHidden = $false
$inputBlocked = $false
$script:blackedOut = $false
$script:lastActivityTick = [Environment]::TickCount

try
{
    if ([System.Threading.Thread]::CurrentThread.GetApartmentState() -ne 'STA')
    {
        throw "Wrong thread apartment (needs STA). Run via 'powershell -STA -File ...', or if compiled with ps2exe, recompile with the -STA switch."
    }

    Add-Type -AssemblyName System.Windows.Forms
    Add-Type -AssemblyName System.Drawing

    # Add-Type cannot redefine a type that already exists in this session,
    # which happens if the script is run more than once in the same
    # PowerShell console. If an OLDER version of this script already loaded
    # IdleGuardNative earlier in this same session, that stale definition is
    # stuck until the process restarts - detect that specifically instead of
    # failing on a confusing "method not found" error later on.
    $existingNative = ([System.Management.Automation.PSTypeName]'IdleGuardNative').Type
    if ($existingNative -and -not $existingNative.GetMethod('GetLastInputTick'))
    {
        throw "Stale incompatible 'IdleGuardNative' type already loaded in this session (left over from an older version of this script run here earlier). Needs a brand new process."
    }
    if (-not $existingNative)
    {
Add-Type @"
using System;
using System.Runtime.InteropServices;

public static class IdleGuardNative
{
    public const uint ES_CONTINUOUS       = 0x80000000;
    public const uint ES_SYSTEM_REQUIRED  = 0x00000001;
    public const uint ES_DISPLAY_REQUIRED = 0x00000002;

    private const byte VK_CAPITAL      = 0x14;
    private const uint KEYEVENTF_KEYUP = 0x0002;

    // Arbitrary marker stamped on our own synthetic Caps Lock keystrokes
    // (via keybd_event's dwExtraInfo) so the input-block hook can recognize
    // and pass through only these specific events - otherwise the hook
    // would swallow them like everything else, and the toggle/LED would
    // never actually update.
    private const uint OWN_INJECTED_MARKER = 0x53504143; // 'CAPS' ASCII

    [DllImport("kernel32.dll")]
    public static extern uint SetThreadExecutionState(uint esFlags);

    [DllImport("user32.dll")]
    private static extern void keybd_event(byte bVk, byte bScan, uint dwFlags, UIntPtr dwExtraInfo);

    [DllImport("user32.dll")]
    private static extern short GetKeyState(int nVirtKey);

    [DllImport("user32.dll")]
    private static extern bool SetProcessDpiAwarenessContext(IntPtr dpiContext);

    [DllImport("shcore.dll")]
    private static extern int SetProcessDpiAwareness(int value);

    [DllImport("user32.dll")]
    private static extern bool SetProcessDPIAware();

    [StructLayout(LayoutKind.Sequential)]
    private struct LASTINPUTINFO
    {
        public uint cbSize;
        public uint dwTime;
    }

    [DllImport("user32.dll")]
    private static extern bool GetLastInputInfo(ref LASTINPUTINFO plii);

    // Used only while NOT blacked out (the input-block hooks aren't
    // installed yet at that point), to notice real keyboard/mouse activity
    // and measure how long the system has been idle.
    public static int GetLastInputTick()
    {
        LASTINPUTINFO lii = new LASTINPUTINFO();
        lii.cbSize = (uint)Marshal.SizeOf(lii);
        GetLastInputInfo(ref lii);
        return unchecked((int)lii.dwTime);
    }

    private const int WH_KEYBOARD_LL = 13;
    private const int WH_MOUSE_LL    = 14;
    private const int WM_KEYDOWN     = 0x0100;
    private const int WM_SYSKEYDOWN  = 0x0104;
    private const int VK_ESCAPE      = 0x1B;

    [StructLayout(LayoutKind.Sequential)]
    private struct KBDLLHOOKSTRUCT
    {
        public uint vkCode;
        public uint scanCode;
        public uint flags;
        public uint time;
        public UIntPtr dwExtraInfo;
    }

    private delegate IntPtr HookProc(int nCode, IntPtr wParam, IntPtr lParam);

    [DllImport("user32.dll", SetLastError = true)]
    private static extern IntPtr SetWindowsHookEx(int idHook, HookProc lpfn, IntPtr hMod, uint dwThreadId);

    [DllImport("user32.dll", SetLastError = true)]
    private static extern bool UnhookWindowsHookEx(IntPtr hhk);

    [DllImport("user32.dll")]
    private static extern IntPtr CallNextHookEx(IntPtr hhk, int nCode, IntPtr wParam, IntPtr lParam);

    [DllImport("kernel32.dll", CharSet = CharSet.Auto)]
    private static extern IntPtr GetModuleHandle(string lpModuleName);

    // Kept as static fields (not locals) so the delegates are never
    // garbage-collected while the native hooks hold unmanaged references
    // to them.
    private static readonly HookProc _keyboardProc = KeyboardHookCallback;
    private static readonly HookProc _mouseProc    = MouseHookCallback;
    private static IntPtr _keyboardHookId = IntPtr.Zero;
    private static IntPtr _mouseHookId    = IntPtr.Zero;

    // Set from the keyboard hook when Escape is pressed while blocking is
    // active. Escape is swallowed like every other key (nothing gets
    // through the hooks), so the caller must poll this flag rather than
    // expect a normal KeyDown event to ever reach a window.
    public static volatile bool EscapeRequested = false;

    public static void InstallInputBlockHooks()
    {
        EscapeRequested = false;
        IntPtr hMod = GetModuleHandle(null);
        _keyboardHookId = SetWindowsHookEx(WH_KEYBOARD_LL, _keyboardProc, hMod, 0);
        _mouseHookId    = SetWindowsHookEx(WH_MOUSE_LL, _mouseProc, hMod, 0);
    }

    public static void RemoveInputBlockHooks()
    {
        if (_keyboardHookId != IntPtr.Zero) { UnhookWindowsHookEx(_keyboardHookId); _keyboardHookId = IntPtr.Zero; }
        if (_mouseHookId != IntPtr.Zero) { UnhookWindowsHookEx(_mouseHookId); _mouseHookId = IntPtr.Zero; }
        EscapeRequested = false;
    }

    private static IntPtr KeyboardHookCallback(int nCode, IntPtr wParam, IntPtr lParam)
    {
        if (nCode < 0)
        {
            return CallNextHookEx(_keyboardHookId, nCode, wParam, lParam);
        }

        KBDLLHOOKSTRUCT data = (KBDLLHOOKSTRUCT)Marshal.PtrToStructure(lParam, typeof(KBDLLHOOKSTRUCT));

        if (data.dwExtraInfo.ToUInt64() == OWN_INJECTED_MARKER)
        {
            // Our own synthetic Caps Lock heartbeat - let it through so the
            // toggle state and LED actually update.
            return CallNextHookEx(_keyboardHookId, nCode, wParam, lParam);
        }

        int msg = wParam.ToInt32();
        if (msg == WM_KEYDOWN || msg == WM_SYSKEYDOWN)
        {
            if (data.vkCode == VK_ESCAPE)
            {
                EscapeRequested = true;
            }
        }
        // Swallow every other key: do not call CallNextHookEx, so nothing -
        // not even our own window - ever receives this input.
        return (IntPtr)1;
    }

    private static IntPtr MouseHookCallback(int nCode, IntPtr wParam, IntPtr lParam)
    {
        if (nCode < 0)
        {
            return CallNextHookEx(_mouseHookId, nCode, wParam, lParam);
        }
        // Swallow every mouse event (move, click, wheel) the same way.
        return (IntPtr)1;
    }

    // Without this, a non-DPI-aware process (PowerShell) gets virtualized/
    // scaled monitor bounds from Screen.AllScreens whenever display scaling
    // isn't 100%, so the black overlay ends up the wrong size for the real
    // monitor. Try the modern per-monitor-v2 API first, then fall back for
    // older Windows versions.
    public static void EnableDpiAwareness()
    {
        try { if (SetProcessDpiAwarenessContext(new IntPtr(-4))) return; } catch { }
        try { if (SetProcessDpiAwareness(2) == 0) return; } catch { }
        try { SetProcessDPIAware(); } catch { }
    }

    public static void ToggleCapsLock()
    {
        UIntPtr marker = (UIntPtr)OWN_INJECTED_MARKER;
        keybd_event(VK_CAPITAL, 0x45, 0, marker);
        keybd_event(VK_CAPITAL, 0x45, KEYEVENTF_KEYUP, marker);
    }

    public static bool IsCapsLockOn()
    {
        return (GetKeyState(VK_CAPITAL) & 1) != 0;
    }
}
"@
    }

    # Must happen before any Screen/Form access so bounds come back as real
    # physical pixels instead of DPI-scaled/virtualized values.
    [IdleGuardNative]::EnableDpiAwareness()

    # Block sleep AND tell Windows the display must stay on for the entire
    # lifetime of this script (not just during blackout), so it never sees
    # a display-off/idle transition that could trigger a session lock.
    [IdleGuardNative]::SetThreadExecutionState(
        [IdleGuardNative]::ES_CONTINUOUS -bor
        [IdleGuardNative]::ES_SYSTEM_REQUIRED -bor
        [IdleGuardNative]::ES_DISPLAY_REQUIRED
    ) | Out-Null

    function Enter-Blackout
    {
        Write-Log 'Idle timeout reached - entering blackout'

        $keyDownHandler = {
            param($sender, $e)
            # Fallback only: normally the hooks below swallow this before it
            # ever reaches a window, and Escape is instead detected via
            # IdleGuardNative.EscapeRequested and the fast poll timer.
            if ($e.KeyCode -eq [System.Windows.Forms.Keys]::Escape)
            {
                Exit-Blackout
            }
        }

        foreach ($screen in [System.Windows.Forms.Screen]::AllScreens)
        {
            $f = New-Object System.Windows.Forms.Form
            $f.FormBorderStyle = 'None'
            $f.StartPosition   = 'Manual'
            $f.Bounds          = $screen.Bounds
            $f.BackColor       = [System.Drawing.Color]::Black
            $f.TopMost         = $true
            $f.ShowInTaskbar   = $false
            $f.KeyPreview      = $true
            $f.Cursor          = [System.Windows.Forms.Cursors]::None

            $f.Add_KeyDown($keyDownHandler)

            $script:forms += $f
        }

        foreach ($f in $script:forms) { $f.Show() }
        [void]$script:forms[0].Focus()
        [System.Windows.Forms.Cursor]::Hide()
        $script:cursorHidden = $true

        # Blocks ALL keyboard/mouse input system-wide at the OS level.
        [IdleGuardNative]::InstallInputBlockHooks()
        $script:inputBlocked = $true

        $script:heartbeatTimer.Start()
        $script:blackedOut = $true
    }

    function Exit-Blackout
    {
        Write-Log 'Escape pressed - exiting blackout, resuming idle watch'

        # Restore input FIRST, before anything else, so the user regains
        # control of their keyboard/mouse as soon as possible.
        if ($script:inputBlocked) { try { [IdleGuardNative]::RemoveInputBlockHooks() } catch { } }
        $script:inputBlocked = $false

        $script:heartbeatTimer.Stop()
        if ([IdleGuardNative]::IsCapsLockOn()) { [IdleGuardNative]::ToggleCapsLock() }

        if ($script:cursorHidden) { try { [System.Windows.Forms.Cursor]::Show() } catch { } }
        $script:cursorHidden = $false

        foreach ($f in $script:forms)
        {
            try { if ($f -and -not $f.IsDisposed) { $f.Close(); $f.Dispose() } } catch { }
        }
        $script:forms = @()

        # Restart the idle countdown fresh from now, rather than trusting
        # GetLastInputTick() - real input was being swallowed by our own
        # hooks for the duration of the blackout, so the OS-reported value
        # is stale and would otherwise cause an immediate re-trigger.
        $script:lastActivityTick = [Environment]::TickCount
        $script:blackedOut = $false
    }

    $idleThresholdMs = [Math]::Max(1, $IdleMinutes) * 60000

    $heartbeatTimer = New-Object System.Windows.Forms.Timer
    $heartbeatTimer.Interval = [Math]::Max(1, $HeartbeatSeconds) * 1000
    $heartbeatTimer.Add_Tick({
        [IdleGuardNative]::ToggleCapsLock()
        Start-Sleep -Milliseconds 150
        [IdleGuardNative]::ToggleCapsLock()
    })

    $fastTimer = New-Object System.Windows.Forms.Timer
    $fastTimer.Interval = [Math]::Max(50, $PollMilliseconds)
    $fastTimer.Add_Tick({
        if ($script:blackedOut)
        {
            if ([IdleGuardNative]::EscapeRequested)
            {
                Exit-Blackout
            }
            return
        }

        # Adopt the OS-reported last-input tick whenever it's newer than
        # what we're tracking (i.e. real activity happened).
        $osLast = [IdleGuardNative]::GetLastInputTick()
        if (($osLast - $script:lastActivityTick) -gt 0)
        {
            $script:lastActivityTick = $osLast
        }

        $idleMs = [Environment]::TickCount - $script:lastActivityTick
        if ($idleMs -ge $idleThresholdMs)
        {
            Enter-Blackout
        }
    })
    $fastTimer.Start()

    Write-Log "Entering Application.Run() (background idle guard, IdleMinutes=$IdleMinutes)"
    [System.Windows.Forms.Application]::Run()
    Write-Log 'Application.Run() returned (unexpected - this script is not meant to exit on its own).'
}
catch
{
    Write-Log "EXCEPTION: $($_.Exception.GetType().FullName): $($_.Exception.Message)"
    Write-Log ('  at ' + ($_.ScriptStackTrace -replace "`n", ' | '))
    # Deliberately not re-thrown: this may run as a ps2exe -noConsole exe,
    # where an unhandled exception would surface as an error popup. All
    # diagnostics live in the log file instead.
}
finally
{
    # Restore input FIRST, before anything else, so the user regains control
    # of their keyboard/mouse as soon as possible no matter what else below
    # might fail.
    if ($inputBlocked) { try { [IdleGuardNative]::RemoveInputBlockHooks() } catch { } }
    if ($cursorHidden) { try { [System.Windows.Forms.Cursor]::Show() } catch { } }
    if ($heartbeatTimer) { try { $heartbeatTimer.Stop(); $heartbeatTimer.Dispose() } catch { } }
    if ($fastTimer)      { try { $fastTimer.Stop();      $fastTimer.Dispose() } catch { } }
    foreach ($f in $forms)
    {
        try { if ($f -and -not $f.IsDisposed) { $f.Close(); $f.Dispose() } } catch { }
    }
    try
    {
        if (([System.Management.Automation.PSTypeName]'IdleGuardNative').Type)
        {
            [IdleGuardNative]::SetThreadExecutionState([IdleGuardNative]::ES_CONTINUOUS) | Out-Null
            if ([IdleGuardNative]::IsCapsLockOn()) { [IdleGuardNative]::ToggleCapsLock() }
        }
    }
    catch { }
    Write-Log "Cleanup done. Log at: $logPath"
}
