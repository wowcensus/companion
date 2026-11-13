package companion

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
)

var installPath = UserInstallPath
var bundleVersionPattern = regexp.MustCompile(`<key>CFBundleShortVersionString</key>\s*<string>([^<]+)</string>`)

// UserInstallPath is kept separate from the settings directory so uninstalling
// the program does not discard upload receipts or the device registration.
func UserInstallPath() (string, error) {
	switch runtime.GOOS {
	case "windows":
		return `C:\WoWCensus\wowcensus-companion.exe`, nil
	case "darwin":
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Applications", "WoWCensus Companion.app"), nil
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		name := "wowcensus-companion"
		if os.Getenv("APPIMAGE") != "" {
			name += ".AppImage"
		}
		return filepath.Join(home, ".local", "opt", "wowcensus-companion", name), nil
	}
}

func legacyWindowsInstallPath() (string, error) {
	base := os.Getenv("LOCALAPPDATA")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		base = filepath.Join(home, "AppData", "Local")
	}
	return filepath.Join(base, "Programs", "WoWCensus Companion", "wowcensus-companion.exe"), nil
}

func sameInstallPath(a, b string) bool {
	a = filepath.Clean(a)
	b = filepath.Clean(b)
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

func isMacTranslocation(path string) bool {
	return strings.Contains(filepath.ToSlash(path), "/AppTranslocation/")
}

func macBundleVersion(bundle string) string {
	data, err := os.ReadFile(filepath.Join(bundle, "Contents", "Info.plist"))
	if err != nil {
		return ""
	}
	match := bundleVersionPattern.FindSubmatch(data)
	if len(match) != 2 {
		return ""
	}
	return string(match[1])
}

func installMacBundle(source, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
		return err
	}
	stage, err := os.MkdirTemp(filepath.Dir(target), ".wowcensus-install-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(stage)
	copy := filepath.Join(stage, filepath.Base(target))
	if out, err := exec.Command("ditto", source, copy).CombinedOutput(); err != nil {
		return fmt.Errorf("copying Mac app: %v: %s", err, out)
	}
	old := target + ".old"
	if _, err := os.Stat(target); err == nil {
		_ = os.RemoveAll(old)
		if err := os.Rename(target, old); err != nil {
			return err
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.Rename(copy, target); err != nil {
		_ = os.Rename(old, target)
		return err
	}
	return nil
}

func copyInstallFile(src, dst string) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst+".installing", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	_, copyErr := io.Copy(out, in)
	closeErr := out.Close()
	if copyErr != nil || closeErr != nil {
		_ = os.Remove(dst + ".installing")
		return fmt.Errorf("copying app: %v %v", copyErr, closeErr)
	}
	if err := os.Rename(dst+".installing", dst); err != nil {
		_ = os.Remove(dst + ".installing")
		return err
	}
	return nil
}

func installLinuxLauncher(target string) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}
	quoted := `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`, "`", "\\`", "$", `\$`).Replace(target) + `"`
	entry := "[Desktop Entry]\nType=Application\nName=WoWCensus Companion\nExec=" + quoted + "\nTerminal=false\nCategories=Game;Utility;\n"
	return writeFileAtomic(filepath.Join(home, ".local", "share", "applications", "wowcensus-companion.desktop"), []byte(entry), 0o644)
}

// InstallToUserHome installs a downloaded copy and returns the installed app
// to launch. An already installed app simply continues its normal startup.
func InstallToUserHome(args []string) (Relaunch, bool, error) {
	exe, err := executablePath()
	if err != nil {
		return Relaunch{}, false, err
	}
	target, err := installPath()
	if err != nil {
		return Relaunch{}, false, err
	}
	if runtime.GOOS == "darwin" {
		bundle := appBundle(exe)
		if bundle == "" {
			return Relaunch{}, false, fmt.Errorf("the Mac app must be run from its .app bundle")
		}
		if sameInstallPath(bundle, target) {
			return Relaunch{}, false, nil
		}
		installedVersion := macBundleVersion(target)
		if installedVersion == "" || NewerVersion(AppVersion, installedVersion) {
			if err := installMacBundle(bundle, target); err != nil {
				return Relaunch{}, false, err
			}
		} else if isMacTranslocation(bundle) {
			// Finder may report a translocated path for the installed bundle.
			// Opening the same bundle again would create a launch loop.
			return Relaunch{}, false, nil
		}
		launchArgs := []string{"-n", target}
		if len(args) > 0 {
			launchArgs = append(launchArgs, "--args")
			launchArgs = append(launchArgs, args...)
		}
		return Relaunch{Path: "open", Args: launchArgs}, true, nil
	}
	source := exe
	if runtime.GOOS == "windows" {
		legacy, err := legacyWindowsInstallPath()
		if err != nil {
			return Relaunch{}, false, err
		}
		if sameInstallPath(source, legacy) {
			return Relaunch{}, false, nil
		}
	}
	if runtime.GOOS == "linux" && os.Getenv("APPIMAGE") != "" {
		source = os.Getenv("APPIMAGE")
	}
	if sameInstallPath(source, target) {
		return Relaunch{}, false, nil
	}
	if _, err := os.Stat(target); os.IsNotExist(err) {
		if err := copyInstallFile(source, target); err != nil {
			return Relaunch{}, false, err
		}
	} else if err != nil {
		return Relaunch{}, false, err
	}
	if runtime.GOOS == "linux" {
		if err := installLinuxLauncher(target); err != nil {
			return Relaunch{}, false, err
		}
	}
	return Relaunch{Path: target, Args: args}, true, nil
}
