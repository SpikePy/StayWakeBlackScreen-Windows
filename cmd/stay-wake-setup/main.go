//go:build windows

// Command stay-wake-setup is the single entry point for installing,
// updating, and uninstalling StayWakeBlackScreenIdle.exe. Run it with no
// arguments (e.g. by double-clicking Setup_StayWakeBlackScreenIdle.exe)
// and it shows an interactive menu to choose "Install / update" or
// "Uninstall" - defaulting to "Install / update" on its own if nothing
// is chosen within promptTimeout. Pass -mode to skip the prompt for
// scripted use.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"windows-stay-wake-black-screen/internal/setup"
)

// promptTimeout is how long the menu waits for a first keypress before
// defaulting to "install" on its own - so double-clicking the exe and
// walking away still gets the tool installed/updated.
const promptTimeout = 5 * time.Second

// autoExitTimeout is how long the final "press Enter to exit" wait is
// capped at when the action itself was auto-chosen (nobody was at the
// keyboard for promptTimeout, so there's nobody left to press Enter
// either): it shows the result briefly, then exits on its own.
const autoExitTimeout = 3 * time.Second

func main() {
	mode := flag.String("mode", "", "skip the interactive menu and run this action directly: install or uninstall")
	installDir := flag.String("install-dir", "", "directory to install into/remove from (default: %LOCALAPPDATA%\\StayWakeBlackScreen)")
	githubToken := flag.String("github-token", "", "optional GitHub token, to avoid the unauthenticated API rate limit (install only)")
	noLaunch := flag.Bool("no-launch", false, "install/update and register autostart, but don't start it now (install only)")
	noAutostart := flag.Bool("no-autostart", false, "don't register (or update) the autostart entry (install only)")
	keepFiles := flag.Bool("keep-files", false, "remove autostart and stop the process, but don't delete the installed files (uninstall only)")
	flag.Parse()

	interactive := *mode == ""
	action := strings.ToLower(*mode)
	autoChosen := false

	var in stdinLines
	if interactive {
		in = readStdinLines()
		var err error
		action, autoChosen, err = promptAction(in)
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	}

	var err error
	switch action {
	case "install":
		err = setup.Install(setup.InstallOptions{
			InstallDir:  *installDir,
			GitHubToken: *githubToken,
			NoLaunch:    *noLaunch,
			NoAutostart: *noAutostart,
		})
	case "uninstall":
		err = setup.Uninstall(setup.UninstallOptions{
			InstallDir: *installDir,
			KeepFiles:  *keepFiles,
		})
	default:
		fmt.Fprintf(os.Stderr, "error: unknown -mode %q (want install or uninstall)\n", *mode)
		os.Exit(1)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
	}
	if interactive {
		var exitAfter time.Duration
		if autoChosen {
			exitAfter = autoExitTimeout
		}
		waitForEnter(in, exitAfter)
	}
	if err != nil {
		os.Exit(1)
	}
}

// stdinLines delivers stdin line by line from one background reader that
// lives for the whole process, so promptAction and the final waitForEnter
// share it instead of racing two separate reads against the same console
// input.
type stdinLines struct {
	lines <-chan string
	errs  <-chan error
}

func readStdinLines() stdinLines {
	lines := make(chan string)
	errs := make(chan error, 1)
	go func() {
		reader := bufio.NewReader(os.Stdin)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				errs <- err
				return
			}
			lines <- line
		}
	}()
	return stdinLines{lines: lines, errs: errs}
}

// promptAction shows the interactive menu and returns "install" or
// "uninstall", plus whether it was auto-chosen (see autoExitTimeout). If
// nothing is chosen within promptTimeout of the first prompt, it returns
// "install" on its own; once the user has typed anything (even an
// invalid choice), later reprompts within the same call wait
// indefinitely - they've shown they're there.
func promptAction(in stdinLines) (action string, auto bool, err error) {
	countdown := promptTimeout
	for {
		fmt.Println("Windows StayWakeBlackScreen - Setup")
		fmt.Println()
		fmt.Println("  1) Install / update")
		fmt.Println("  2) Uninstall")
		fmt.Println()

		var expired <-chan time.Time // stays nil (never fires) once the countdown is used up
		if countdown > 0 {
			fmt.Printf("Choose an option [1-2] (installing/updating automatically in %d seconds if nothing is chosen): ", int(countdown.Seconds()))
			expired = time.After(countdown)
			countdown = 0
		} else {
			fmt.Print("Choose an option [1-2]: ")
		}

		var line string
		select {
		case line = <-in.lines:
		case e := <-in.errs:
			return "", false, fmt.Errorf("reading input: %w", e)
		case <-expired:
			fmt.Println()
			fmt.Println("No input received - installing/updating automatically.")
			return "install", true, nil
		}

		switch strings.TrimSpace(line) {
		case "1":
			return "install", false, nil
		case "2":
			return "uninstall", false, nil
		default:
			fmt.Println("Please enter 1 or 2.")
			fmt.Println()
		}
	}
}

// waitForEnter keeps the console window open (double-clicking the exe
// opens one that would otherwise close immediately on exit) until the
// user presses Enter - or, if exitAfter > 0, until that much time has
// passed, since an auto-chosen action means nobody is likely there to
// press it.
func waitForEnter(in stdinLines, exitAfter time.Duration) {
	fmt.Println()
	var expired <-chan time.Time // nil (never fires) unless exitAfter > 0
	if exitAfter > 0 {
		fmt.Printf("Exiting automatically in %d seconds (press Enter to exit now)...", int(exitAfter.Seconds()))
		expired = time.After(exitAfter)
	} else {
		fmt.Print("Press Enter to exit...")
	}
	select {
	case <-in.lines:
	case <-in.errs:
	case <-expired:
	}
	if exitAfter > 0 {
		fmt.Println()
	}
}
