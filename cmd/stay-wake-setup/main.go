//go:build windows

// Command stay-wake-setup is the single entry point for installing,
// updating, and uninstalling StayWakeBlackScreenIdle.exe. Run it with no
// arguments (e.g. by double-clicking Setup_StayWakeBlackScreenIdle.exe)
// and it shows an interactive menu to choose "Install / update" or
// "Uninstall". Pass -mode to skip the prompt for scripted use.
package main

import (
	"bufio"
	"flag"
	"fmt"
	"os"
	"strings"

	"windows-stay-wake-black-screen/internal/setup"
)

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
	if interactive {
		var err error
		action, err = promptAction()
		if err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		if action == "exit" {
			return
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
		pause()
	}
	if err != nil {
		os.Exit(1)
	}
}

// promptAction shows the interactive menu and returns "install",
// "uninstall", or "exit".
func promptAction() (string, error) {
	reader := bufio.NewReader(os.Stdin)
	for {
		fmt.Println("Windows StayWakeBlackScreen - Setup")
		fmt.Println()
		fmt.Println("  1) Install / update")
		fmt.Println("  2) Uninstall")
		fmt.Println("  3) Exit")
		fmt.Println()
		fmt.Print("Choose an option [1-3]: ")

		line, err := reader.ReadString('\n')
		if err != nil {
			return "", fmt.Errorf("reading input: %w", err)
		}
		switch strings.TrimSpace(line) {
		case "1":
			return "install", nil
		case "2":
			return "uninstall", nil
		case "3":
			return "exit", nil
		default:
			fmt.Println("Please enter 1, 2, or 3.")
			fmt.Println()
		}
	}
}

// pause keeps the console window open (double-clicking the exe opens one
// that would otherwise close immediately on exit) until the user
// acknowledges the result.
func pause() {
	fmt.Println()
	fmt.Print("Press Enter to exit...")
	bufio.NewReader(os.Stdin).ReadString('\n')
}
