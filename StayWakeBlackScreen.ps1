<#
Usage:
  powershell -ExecutionPolicy Bypass -File .\StayWakeBlackScreen.ps1
  Optional: -HeartbeatSeconds 5
  ALL keyboard and mouse input is blocked system-wide (via low-level
  WH_KEYBOARD_LL/WH_MOUSE_LL hooks) while this runs - nothing reaches any
  window, including this one. Only pressing Escape ends the black screen
  and exits. Note: Windows never lets any hook suppress Ctrl+Alt+Del, so
  that combination always remains available as a hard escape hatch
  regardless of anything going wrong here.
  Logging is OFF by default (no file, no output, no popups at all). Pass
  -EnableLogging to write diagnostics to StayWakeBlackScreen.log next to
  the script/exe, for troubleshooting only.

Note: This does NOT power off the monitor. Earlier versions used
SC_MONITORPOWER to physically turn the display off, but Windows treats a
display-off event as an idle/wake transition and can lock the session
(especially with "require sign-in on wake" enabled) even though
SetThreadExecutionState is blocking sleep. Instead, this covers every
screen with a real black window and keeps the display explicitly
"required" (on), so Windows never sees a display-off event and has no
reason to lock.

Compiling with ps2exe:
  This script uses Windows Forms, which requires the STA apartment. A
  plain PowerShell console (powershell.exe) is STA by default, but a
  ps2exe-compiled exe is NOT unless told to be - compile with -STA:
    Invoke-ps2exe -inputFile StayWakeBlackScreen.ps1 -outputFile StayWakeBlackScreen.exe -STA -noConsole
  (-noConsole is optional but fits this being a GUI-only tool.)

Everything below runs inside one top-level try/catch that never writes to
any output stream and never re-throws, so nothing can surface as console
text or a popup (ps2exe's -noConsole host shows its own error dialog for
any unhandled exception) - all diagnostics go only to the .log file next
to the script/exe.
#>

param(
    [int]$HeartbeatSeconds = 5,
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
$logPath = Join-Path $scriptDir 'StayWakeBlackScreen.log'
$script:exitReason = 'Application.Run() returned without any handler logging a reason (unexpected).'

function Write-Log([string]$msg)
{
    if (-not $EnableLogging) { return }
    try { "$(Get-Date -Format 'yyyy-MM-dd HH:mm:ss.fff')  $msg" | Out-File -FilePath $logPath -Append -Encoding utf8 } catch { }
}

$forms = @()
$heartbeatTimer = $null
$escapeWatchTimer = $null
$cursorHidden = $false
$inputBlocked = $false

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
    # NativeMethods earlier in this same session, that stale definition is
    # stuck until the process restarts - detect that specifically instead of
    # failing on a confusing "method not found" error later on.
    $existingNativeMethods = ([System.Management.Automation.PSTypeName]'NativeMethods').Type
    if ($existingNativeMethods -and -not $existingNativeMethods.GetMethod('InstallInputBlockHooks'))
    {
        throw "Stale incompatible 'NativeMethods' type already loaded in this session (left over from an older version of this script run here earlier). Needs a brand new process."
    }
    if (-not $existingNativeMethods)
    {
Add-Type @"
using System;
using System.Runtime.InteropServices;

public static class NativeMethods
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

    // Set from the keyboard hook when Escape is pressed. Escape is also
    // swallowed like every other key (nothing gets through the hooks), so
    // the caller must poll this flag rather than expect a normal KeyDown
    // event to ever reach a window.
    public static volatile bool EscapeRequested = false;

    public static void InstallInputBlockHooks()
    {
        IntPtr hMod = GetModuleHandle(null);
        _keyboardHookId = SetWindowsHookEx(WH_KEYBOARD_LL, _keyboardProc, hMod, 0);
        _mouseHookId    = SetWindowsHookEx(WH_MOUSE_LL, _mouseProc, hMod, 0);
    }

    public static void RemoveInputBlockHooks()
    {
        if (_keyboardHookId != IntPtr.Zero) { UnhookWindowsHookEx(_keyboardHookId); _keyboardHookId = IntPtr.Zero; }
        if (_mouseHookId != IntPtr.Zero) { UnhookWindowsHookEx(_mouseHookId); _mouseHookId = IntPtr.Zero; }
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
    [NativeMethods]::EnableDpiAwareness()

    # Block sleep AND tell Windows the display must stay on, so it never
    # sees a display-off/idle transition that could trigger a session lock.
    [NativeMethods]::SetThreadExecutionState(
        [NativeMethods]::ES_CONTINUOUS -bor
        [NativeMethods]::ES_SYSTEM_REQUIRED -bor
        [NativeMethods]::ES_DISPLAY_REQUIRED
    ) | Out-Null

    # Only Escape ends the black screen / program - no other key, click, or
    # mouse movement does.
    $keyDownHandler = {
        param($sender, $e)
        if ($e.KeyCode -eq [System.Windows.Forms.Keys]::Escape)
        {
            $script:exitReason = 'Escape pressed'
            [System.Windows.Forms.Application]::Exit()
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

        $forms += $f
        Write-Log "Created overlay form for screen '$($screen.DeviceName)' bounds=$($screen.Bounds)"
    }

    foreach ($f in $forms) { $f.Show() }
    [void]$forms[0].Focus()
    [System.Windows.Forms.Cursor]::Hide()
    $cursorHidden = $true

    # Blocks ALL keyboard/mouse input system-wide at the OS level - nothing
    # reaches any window, including this one's own KeyDown handler above.
    # Escape is detected inside the hook itself (NativeMethods.EscapeRequested)
    # and picked up by escapeWatchTimer below instead.
    [NativeMethods]::InstallInputBlockHooks()
    $inputBlocked = $true

    $heartbeatTimer = New-Object System.Windows.Forms.Timer
    $heartbeatTimer.Interval = [Math]::Max(1, $HeartbeatSeconds) * 1000
    $heartbeatTimer.Add_Tick({
        [NativeMethods]::ToggleCapsLock()
        Start-Sleep -Milliseconds 150
        [NativeMethods]::ToggleCapsLock()
    })
    $heartbeatTimer.Start()

    $escapeWatchTimer = New-Object System.Windows.Forms.Timer
    $escapeWatchTimer.Interval = 50
    $escapeWatchTimer.Add_Tick({
        if ([NativeMethods]::EscapeRequested)
        {
            $script:exitReason = 'Escape pressed (detected via input-block hook)'
            [System.Windows.Forms.Application]::Exit()
        }
    })
    $escapeWatchTimer.Start()

    Write-Log 'Entering Application.Run()'
    [System.Windows.Forms.Application]::Run()
    Write-Log "Application.Run() returned. Reason: $($script:exitReason)"
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
    if ($inputBlocked) { try { [NativeMethods]::RemoveInputBlockHooks() } catch { } }
    if ($cursorHidden) { try { [System.Windows.Forms.Cursor]::Show() } catch { } }
    if ($heartbeatTimer)   { try { $heartbeatTimer.Stop();   $heartbeatTimer.Dispose() } catch { } }
    if ($escapeWatchTimer) { try { $escapeWatchTimer.Stop(); $escapeWatchTimer.Dispose() } catch { } }
    foreach ($f in $forms)
    {
        try { if ($f -and -not $f.IsDisposed) { $f.Close(); $f.Dispose() } } catch { }
    }
    try
    {
        if (([System.Management.Automation.PSTypeName]'NativeMethods').Type)
        {
            [NativeMethods]::SetThreadExecutionState([NativeMethods]::ES_CONTINUOUS) | Out-Null
            if ([NativeMethods]::IsCapsLockOn()) { [NativeMethods]::ToggleCapsLock() }
        }
    }
    catch { }
    Write-Log "Cleanup done. Log at: $logPath"
}
