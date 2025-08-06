//go:generate go install -v github.com/kevinburke/go-bindata/v4/go-bindata
//go:generate go-bindata -prefix res/ -pkg assets -o assets/assets.go res/Floorp.lnk
//go:generate go install -v github.com/josephspurrier/goversioninfo/cmd/goversioninfo
//go:generate goversioninfo -icon=res/papp.ico -manifest=res/papp.manifest
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/Floorp-Projects/Floorp-Portable-v2/assets"
	"github.com/Jeffail/gabs"
	"github.com/bodgit/sevenzip"
	"github.com/pkg/errors"
	"github.com/portapps/portapps/v3"
	"github.com/portapps/portapps/v3/pkg/log"
	"github.com/portapps/portapps/v3/pkg/mutex"
	"github.com/portapps/portapps/v3/pkg/shortcut"
	"github.com/portapps/portapps/v3/pkg/utl"
	"github.com/portapps/portapps/v3/pkg/win"
)

type config struct {
	Profile           string `yaml:"profile" mapstructure:"profile"`
	MultipleInstances bool   `yaml:"multiple_instances" mapstructure:"multiple_instances"`
	Cleanup           bool   `yaml:"cleanup" mapstructure:"cleanup"`
	CheckForUpdates   bool   `yaml:"check_for_updates" mapstructure:"check_for_updates"`
	UpdateURL         string `yaml:"update_url" mapstructure:"update_url"`
}

var (
	app *portapps.App
	cfg *config
)

func init() {
	var err error

	// Default config
	cfg = &config{
		Profile:           "default",
		MultipleInstances: false,
		Cleanup:           false,
		CheckForUpdates:   true,
		UpdateURL:         "https://github.com/Floorp-Projects/Floorp/releases/latest",
	}

	// Init app
	if app, err = portapps.NewWithCfg("floorp-portable", "Floorp", cfg); err != nil {
		log.Fatal().Err(err).Msg("Cannot initialize application. See log file for more info.")
	}
}

func main() {
	utl.CreateFolder(app.DataPath)
	profileFolder := utl.CreateFolder(app.DataPath, "profile", cfg.Profile)

	// Check for updates if enabled
	if cfg.CheckForUpdates {
		log.Info().Msg("Update checking is enabled")
		updateAvailable, downloadURL, currentVersion, latestVersion := checkForUpdates()
		log.Info().Msgf("Update available: %v, Current: %s, Latest: %s, URL: %s",
			updateAvailable, currentVersion, latestVersion, downloadURL)

		if updateAvailable {
			confirmed := confirmUpdate(currentVersion, latestVersion)
			log.Info().Msgf("User confirmed update: %v", confirmed)

			if confirmed {
				log.Info().Msg("Starting update process...")
				err := downloadAndUpdate(downloadURL)
				if err == nil {
					log.Info().Msg("Update successful, restarting application...")
					restartApp()
					return
				} else {
					log.Error().Err(err).Msg("Update failed, continuing with normal startup")
					// Show error message to user
					win.MsgBox(
						fmt.Sprintf("%s update", app.Name),
						fmt.Sprintf("Failed to update: %s", err),
						win.MsgBoxBtnOk|win.MsgBoxIconError)
				}
			}
		}
	}

	app.Process = utl.PathJoin(app.AppPath, "floorp.exe")
	app.Args = []string{
		"--profile",
		profileFolder,
	}

	// Set env vars
	crashreporterFolder := utl.CreateFolder(app.DataPath, "crashreporter")
	pluginsFolder := utl.CreateFolder(app.DataPath, "plugins")
	os.Setenv("MOZ_CRASHREPORTER", "0")
	os.Setenv("MOZ_CRASHREPORTER_DATA_DIRECTORY", crashreporterFolder)
	os.Setenv("MOZ_CRASHREPORTER_DISABLE", "1")
	os.Setenv("MOZ_CRASHREPORTER_NO_REPORT", "1")
	os.Setenv("MOZ_DATA_REPORTING", "0")
	os.Setenv("MOZ_MAINTENANCE_SERVICE", "0")
	os.Setenv("MOZ_PLUGIN_PATH", pluginsFolder)
	os.Setenv("MOZ_UPDATER", "0")

	// Create and check mutex
	mu, err := mutex.Create(app.ID)
	defer mutex.Release(mu)
	if err != nil {
		if !cfg.MultipleInstances {
			log.Error().Msg("You have to enable multiple instances in your configuration if you want to launch another instance")
			if _, err = win.MsgBox(
				fmt.Sprintf("%s portable", app.Name),
				"Other instance detected. You have to enable multiple instances in your configuration if you want to launch another instance.",
				win.MsgBoxBtnOk|win.MsgBoxIconError); err != nil {
				log.Error().Err(err).Msg("Cannot create dialog box")
			}
			return
		} else {
			log.Warn().Msg("Another instance is already running")
		}
	}

	if cfg.Cleanup {
		defer func() {
			utl.Cleanup([]string{
				path.Join(os.Getenv("APPDATA"), "Floorp"),
				path.Join(os.Getenv("LOCALAPPDATA"), "Floorp"),
				path.Join(os.Getenv("USERPROFILE"), "AppData", "LocalLow", "Floorp"),
				path.Join(os.Getenv("USERPROFILE"), "AppData", "Roaming", "Floorp"),
			})
		}()
	}

	// Multiple instances
	if cfg.MultipleInstances {
		log.Info().Msg("Multiple instances enabled")
		app.Args = append(app.Args, "--no-remote")
	}

	// Policies
	if err := createPolicies(); err != nil {
		log.Fatal().Err(err).Msg("Cannot create policies")
	}

	// Autoconfig
	prefFolder := utl.CreateFolder(app.AppPath, "defaults/pref")
	autoconfig := utl.PathJoin(prefFolder, "autoconfig.js")
	if err := utl.CreateFile(autoconfig, `//
pref("general.config.filename", "portapps.cfg");
pref("general.config.obscure_value", 0);`); err != nil {
		log.Fatal().Err(err).Msg("Cannot write autoconfig.js")
	}

	// Mozilla cfg
	mozillaCfgPath := utl.PathJoin(app.AppPath, "portapps.cfg")
	mozillaCfgFile, err := os.Create(mozillaCfgPath)
	if err != nil {
		log.Fatal().Err(err).Msg("Cannot create portapps.cfg")
	}
	mozillaCfgTpl := template.Must(template.New("mozillaCfg").Parse(`// Extensions scopes
lockPref("extensions.enabledScopes", 4);
lockPref("extensions.autoDisableScopes", 3);

// Don't show 'know your rights' on first run
pref("browser.rights.3.shown", true);

// Don't show WhatsNew on first run after every update
pref("browser.startup.homepage_override.mstone", "ignore");
`))
	if err := mozillaCfgTpl.Execute(mozillaCfgFile, nil); err != nil {
		log.Fatal().Err(err).Msg("Cannot write portapps.cfg")
	}

	// Fix extensions path
	if err := updateAddonStartup(profileFolder); err != nil {
		log.Error().Err(err).Msg("Cannot fix extensions path")
	}

	// Copy default shortcut
	shortcutPath := path.Join(os.Getenv("APPDATA"), "Microsoft", "Windows", "Start Menu", "Programs", "Floorp Portable.lnk")
	defaultShortcut, err := assets.Asset("Floorp.lnk")
	if err != nil {
		log.Error().Err(err).Msg("Cannot load asset Floorp.lnk")
	}
	err = os.WriteFile(shortcutPath, defaultShortcut, 0644)
	if err != nil {
		log.Error().Err(err).Msg("Cannot write default shortcut")
	}

	// Update default shortcut
	err = shortcut.Create(shortcut.Shortcut{
		ShortcutPath:     shortcutPath,
		TargetPath:       app.Process,
		Arguments:        shortcut.Property{Clear: true},
		Description:      shortcut.Property{Value: "Floorp Portable"},
		IconLocation:     shortcut.Property{Value: app.Process},
		WorkingDirectory: shortcut.Property{Value: app.AppPath},
	})
	if err != nil {
		log.Error().Err(err).Msg("Cannot create shortcut")
	}
	defer func() {
		if err := os.Remove(shortcutPath); err != nil {
			log.Error().Err(err).Msg("Cannot remove shortcut")
		}
	}()

	defer app.Close()
	app.Launch(os.Args[1:])
}

// checkForUpdates checks if a new version of Floorp is available
// Returns true if an update is available, the download URL, current version, and latest version
func checkForUpdates() (bool, string, string, string) {
	log.Info().Msg("Checking for Floorp updates...")

	// Get current version from portapp.json
	currentVersion, err := getPortappVersion()
	if err != nil {
		log.Error().Err(err).Msg("Failed to determine current version from portapp.json")
		currentVersion = "unknown"
	}
	log.Info().Msgf("Current version from portapp.json: %s", currentVersion)

	// Get latest version from GitHub API
	latestVersion, releaseURL, err := getLatestGitHubRelease()
	if err != nil {
		log.Error().Err(err).Msg("Failed to determine latest version from GitHub")
		latestVersion = "unknown"
		return false, "", currentVersion, latestVersion
	}
	log.Info().Msgf("Latest version from GitHub: %s", latestVersion)

	// Construct download URL
	downloadURL := strings.Replace(releaseURL, "tag", "download", 1) + "/floorp-win64.installer.exe"
	log.Info().Msgf("Download URL: %s", downloadURL)

	// Compare versions
	if latestVersion != "unknown" && currentVersion != "unknown" {
		updateAvailable := compareVersions(currentVersion, latestVersion) < 0
		log.Info().Msgf("Update available: %v", updateAvailable)
		return updateAvailable, downloadURL, currentVersion, latestVersion
	}

	// If we couldn't determine versions, assume no update is available
	return false, "", currentVersion, latestVersion
}

// getPortappVersion reads the current version from portapp.json
func getPortappVersion() (string, error) {
	// Get the directory of the executable
	execPath, err := os.Executable()
	if err != nil {
		return "", errors.Wrap(err, "failed to get executable path")
	}

	execDir := filepath.Dir(execPath)
	portappPath := filepath.Join(execDir, "portapp.json")

	// Check if portapp.json exists
	if _, err := os.Stat(portappPath); os.IsNotExist(err) {
		return "", errors.New("portapp.json not found")
	}

	// Read and parse the JSON file
	jsonData, err := os.ReadFile(portappPath)
	if err != nil {
		return "", errors.Wrap(err, "failed to read portapp.json")
	}

	// Parse JSON
	var portappData struct {
		Version string `json:"version"`
	}

	if err := json.Unmarshal(jsonData, &portappData); err != nil {
		return "", errors.Wrap(err, "failed to parse portapp.json")
	}

	// Check if version is empty
	if portappData.Version == "" {
		return "", errors.New("version field not found in portapp.json")
	}

	return portappData.Version, nil
}

// getLatestGitHubRelease gets the latest version from GitHub releases
func getLatestGitHubRelease() (string, string, error) {
	// GitHub API URL for latest release
	apiURL := "https://api.github.com/repos/Floorp-Projects/Floorp/releases/latest"

	// Create a client with timeout
	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	// Make the request
	req, err := http.NewRequest("GET", apiURL, nil)
	if err != nil {
		return "", "", errors.Wrap(err, "failed to create request")
	}

	// Set User-Agent to avoid GitHub API limitations
	req.Header.Set("User-Agent", "Floorp-Portable-Updater")

	// Send the request
	resp, err := client.Do(req)
	if err != nil {
		return "", "", errors.Wrap(err, "failed to send request")
	}
	defer resp.Body.Close()

	// Check response
	if resp.StatusCode != http.StatusOK {
		return "", "", fmt.Errorf("GitHub API returned non-OK status: %s", resp.Status)
	}

	// Read the response body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", errors.Wrap(err, "failed to read response body")
	}

	// Parse JSON
	var release struct {
		TagName string `json:"tag_name"`
		HTMLURL string `json:"html_url"`
	}

	if err := json.Unmarshal(body, &release); err != nil {
		return "", "", errors.Wrap(err, "failed to parse GitHub API response")
	}

	// Remove 'v' prefix from version if present
	version := release.TagName
	if strings.HasPrefix(version, "v") {
		version = version[1:]
	}

	return version, release.HTMLURL, nil
}

// getCurrentVersion tries to extract the current version of Floorp
func getCurrentVersion() (string, error) {
	// Check application.ini in the app path
	appIniPath := filepath.Join(app.AppPath, "application.ini")
	if _, err := os.Stat(appIniPath); err == nil {
		// Read application.ini
		data, err := os.ReadFile(appIniPath)
		if err != nil {
			return "", err
		}

		// Parse for version
		lines := strings.Split(string(data), "\n")
		for _, line := range lines {
			if strings.HasPrefix(line, "Version=") {
				return strings.TrimSpace(strings.TrimPrefix(line, "Version=")), nil
			}
		}
	}

	// Fallback: try to read from floorp.exe
	execPath := filepath.Join(app.AppPath, "floorp.exe")
	fileInfo, err := os.Stat(execPath)
	if err != nil {
		return "", errors.New("cannot determine current version")
	}

	// Use file modification time as fallback version indicator
	modTime := fileInfo.ModTime().Format("2006.01.02")
	return modTime, nil
}

// compareVersions compares two version strings
// Returns -1 if v1 < v2, 0 if v1 == v2, 1 if v1 > v2
func compareVersions(v1, v2 string) int {
	// Split versions into components
	v1Parts := strings.Split(strings.TrimPrefix(v1, "v"), ".")
	v2Parts := strings.Split(strings.TrimPrefix(v2, "v"), ".")

	// Compare each component
	maxLen := len(v1Parts)
	if len(v2Parts) > maxLen {
		maxLen = len(v2Parts)
	}

	for i := 0; i < maxLen; i++ {
		// If one version has fewer components, treat missing components as 0
		var num1, num2 int
		if i < len(v1Parts) {
			num1, _ = strconv.Atoi(v1Parts[i])
		}
		if i < len(v2Parts) {
			num2, _ = strconv.Atoi(v2Parts[i])
		}

		if num1 < num2 {
			return -1
		} else if num1 > num2 {
			return 1
		}
	}

	return 0 // Versions are equal
}

// confirmUpdate asks the user if they want to update
func confirmUpdate(currentVersion, latestVersion string) bool {
	message := fmt.Sprintf(
		"A new version of Floorp is available.\n\n"+
		"Current version: %s\n"+
		"Latest version: %s\n\n"+
		"Do you want to update now?",
		currentVersion, latestVersion)

	result, err := win.MsgBox(
		fmt.Sprintf("%s update", app.Name),
		message,
		win.MsgBoxBtnYesNo|win.MsgBoxIconQuestion)

	if err != nil {
		log.Error().Err(err).Msg("Cannot create dialog box")
		return false
	}

	// Using IDYES (6) constant since it appears win.MsgBoxBtnYes might not be defined
	return result == 6 // IDYES = 6 in Windows API
}

// Global variables for update progress tracking
var isUpdating bool
var updateProgress int
var updateMessage string

// showUpdateProgress updates the progress information
func showUpdateProgress(message string, percent int) {
	// Update the global progress variables
	updateProgress = percent
	updateMessage = message
	
	// Log the progress (only visible in log files, not console)
	log.Info().Msgf("Update progress: %d%% - %s", percent, message)
	
	// Only show the completion notification
	if percent == 100 {
		// For completion, show a final message that requires acknowledgment
		isUpdating = false
		win.MsgBox(
			fmt.Sprintf("%s Update Complete", app.Name),
			"The update has been successfully installed. The application will restart now.",
			win.MsgBoxBtnOk|win.MsgBoxIconInformation)
	}
}

// downloadAndUpdate downloads and installs the update
func downloadAndUpdate(downloadURL string) error {
	// Show update starting dialog
	showUpdateProgress("Starting update process...", 0)
	
	// Create temporary directory for download
	tempDir, err := os.MkdirTemp("", "floorp-update")
	if err != nil {
		log.Error().Err(err).Msg("Cannot create temp directory for update")
		return err
	}
	defer os.RemoveAll(tempDir)
	
	zipPath := filepath.Join(tempDir, "floorp-update.exe")
	
	// Download the file
	log.Info().Msgf("Downloading update from: %s", downloadURL)
	log.Info().Msgf("Saving to: %s", zipPath)
	
	// Show download progress dialog
	showUpdateProgress("Downloading update...", 20)
	err = downloadFile(downloadURL, zipPath)
	if err != nil {
		log.Error().Err(err).Msg("Failed to download update")
		return err
	}
	
	// Check if the file exists and has content
	fileInfo, err := os.Stat(zipPath)
	if err != nil {
		log.Error().Err(err).Msg("Cannot stat downloaded file")
		return err
	}
	log.Info().Msgf("Downloaded file size: %d bytes", fileInfo.Size())
	
	if fileInfo.Size() == 0 {
		log.Error().Msg("Downloaded file is empty")
		return errors.New("downloaded file is empty")
	}
	
	// Show extracting progress dialog
	showUpdateProgress("Extracting update files...", 50)
	
	// Extract and update
	log.Info().Msg("Installing update...")
	return extractAndUpdate(zipPath)
}

// extractAndUpdate extracts the zip file and updates the application
func extractAndUpdate(zipPath string) error {
	// Create a temporary directory for extraction
	extractDir, err := os.MkdirTemp("", "floorp-extract")
	if err != nil {
		return err
	}
	defer os.RemoveAll(extractDir)
	
	// Extract 7z file using sevenzip library instead of executing installer
	log.Info().Msg("Extracting 7z archive...")
	if err := extract7zArchive(zipPath, extractDir); err != nil {
		log.Error().Err(err).Msg("Failed to extract 7z archive")
		return err
	}
	
	// Find the app directory in the extracted content
	appDir, err := findAppDir(extractDir)
	if err != nil {
		return err
	}
	
	log.Info().Msg("Found app directory: " + appDir)
	
	// Show update progress dialog
	showUpdateProgress("Updating files...", 75)
	
	// Backup current app path
	backupPath := app.AppPath + ".bak"
	if err := os.Rename(app.AppPath, backupPath); err != nil {
		return err
	}
	
	// Create new app directory
	if err := os.MkdirAll(app.AppPath, 0755); err != nil {
		// Restore from backup if failed
		os.Rename(backupPath, app.AppPath)
		return err
	}
	
	// Copy files from extracted app directory to app path
	if err := copyDir(appDir, app.AppPath); err != nil {
		// Restore from backup if failed
		os.RemoveAll(app.AppPath)
		os.Rename(backupPath, app.AppPath)
		return err
	}
	
	// Remove backup after successful update
	os.RemoveAll(backupPath)
	
	// Update portapp.json with new version information
	if err := updatePortappJson(); err != nil {
		log.Warn().Err(err).Msg("Failed to update portapp.json version")
		// Continue even if this fails - it's not critical
	}
	
	// Show completion dialog
	showUpdateProgress("Update completed successfully!", 100)
	
	log.Info().Msg("Update completed successfully!")
	return nil
}

// downloadFile downloads a file from URL to the specified path
func downloadFile(url string, filepath string) error {
	log.Info().Msgf("Starting download from: %s", url)

	// Create the file
	out, err := os.Create(filepath)
	if err != nil {
		log.Error().Err(err).Msg("Failed to create output file")
		return err
	}
	defer out.Close()

	// Get the data
	client := &http.Client{
		Timeout: 10 * time.Minute, // Set a generous timeout
	}
	resp, err := client.Get(url)
	if err != nil {
		log.Error().Err(err).Msg("HTTP request failed")
		return err
	}
	defer resp.Body.Close()

	// Check server response
	if resp.StatusCode != http.StatusOK {
		log.Error().Msgf("Bad status: %s", resp.Status)
		return fmt.Errorf("bad status: %s", resp.Status)
	}

	log.Info().Msgf("Response status: %s, content length: %d", resp.Status, resp.ContentLength)

	// Write the body to file
	n, err := io.Copy(out, resp.Body)
	if err != nil {
		log.Error().Err(err).Msg("Failed to save downloaded content")
		return err
	}

	log.Info().Msgf("Download complete: %d bytes written", n)
	return nil
}

// updatePortappJson updates the version in portapp.json to the latest version
func updatePortappJson() error {
	// Get the directory of the executable
	execPath, err := os.Executable()
	if err != nil {
		return errors.Wrap(err, "failed to get executable path")
	}

	execDir := filepath.Dir(execPath)
	portappPath := filepath.Join(execDir, "portapp.json")

	// Check if portapp.json exists
	if _, err := os.Stat(portappPath); os.IsNotExist(err) {
		return errors.New("portapp.json not found")
	}

	// Read and parse the JSON file
	jsonData, err := os.ReadFile(portappPath)
	if err != nil {
		return errors.Wrap(err, "failed to read portapp.json")
	}

	// Parse JSON
	var portappData map[string]interface{}
	if err := json.Unmarshal(jsonData, &portappData); err != nil {
		return errors.Wrap(err, "failed to parse portapp.json")
	}

	// Get latest version from GitHub
	latestVersion, _, err := getLatestGitHubRelease()
	if err != nil {
		return errors.Wrap(err, "failed to get latest version")
	}

	// Update version in the JSON
	portappData["version"] = latestVersion

	// Convert back to JSON with pretty-printing
	updatedJson, err := json.MarshalIndent(portappData, "", "  ")
	if err != nil {
		return errors.Wrap(err, "failed to marshal updated JSON")
	}

	// Write back to file
	if err := os.WriteFile(portappPath, updatedJson, 0644); err != nil {
		return errors.Wrap(err, "failed to write updated portapp.json")
	}

	log.Info().Msgf("Updated portapp.json version to %s", latestVersion)
	return nil
}

// extract7zArchive extracts a 7z file to the specified destination using sevenzip library
func extract7zArchive(archivePath string, destPath string) error {
	log.Info().Msgf("Opening 7z archive: %s", archivePath)

	// Open the 7z archive
	sz, err := sevenzip.OpenReader(archivePath)
	if err != nil {
		log.Error().Err(err).Msg("Failed to open 7z archive")
		return errors.Wrap(err, "failed to open 7z archive")
	}
	defer sz.Close()

	log.Info().Msgf("Archive opened successfully, file count: %d", len(sz.File))

	// Extract each file from the archive
	extractedCount := 0
	for _, file := range sz.File {
		log.Debug().Msgf("Processing file: %s, isDir: %v", file.Name, file.FileInfo().IsDir())

		// Skip directories, they will be created when needed
		if file.FileInfo().IsDir() {
			continue
		}

		// Create destination file path
		destFilePath := filepath.Join(destPath, file.Name)

		// Create parent directories if they don't exist
		dirPath := filepath.Dir(destFilePath)
		if err := os.MkdirAll(dirPath, 0755); err != nil {
			log.Error().Err(err).Msgf("Failed to create directory: %s", dirPath)
			return errors.Wrap(err, "failed to create directory")
		}

		// Open source file
		src, err := file.Open()
		if err != nil {
			log.Error().Err(err).Msgf("Failed to open file in archive: %s", file.Name)
			return errors.Wrap(err, "failed to open file in archive")
		}

		// Create destination file
		dst, err := os.Create(destFilePath)
		if err != nil {
			src.Close()
			log.Error().Err(err).Msgf("Failed to create destination file: %s", destFilePath)
			return errors.Wrap(err, "failed to create destination file")
		}

		// Copy file content
		n, err := io.Copy(dst, src)
		src.Close()
		dst.Close()

		if err != nil {
			log.Error().Err(err).Msgf("Failed to extract file: %s", file.Name)
			return errors.Wrap(err, "failed to extract file")
		}

		log.Debug().Msgf("Extracted file: %s (%d bytes)", file.Name, n)
		extractedCount++

		// Set file mode to match original
		if err := os.Chmod(destFilePath, file.Mode()); err != nil {
			log.Warn().Err(err).Msgf("Failed to set file permissions for %s", destFilePath)
		}
	}

	log.Info().Msgf("Extraction complete: %d files extracted", extractedCount)

	// List the top-level directories in the extraction path to help with debugging
	entries, err := os.ReadDir(destPath)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to read extraction directory")
	} else {
		log.Info().Msg("Contents of extraction directory:")
		for _, entry := range entries {
			log.Info().Msgf("- %s (isDir: %v)", entry.Name(), entry.IsDir())
		}
	}

	return nil
}

// findAppDir finds the app directory in the extracted content
func findAppDir(extractDir string) (string, error) {
	log.Info().Msgf("Searching for app directory in: %s", extractDir)
	
	// List all top-level directories for debugging
	entries, err := os.ReadDir(extractDir)
	if err == nil {
		log.Info().Msg("Top-level directories:")
		for _, entry := range entries {
			if entry.IsDir() {
				log.Info().Msgf("- Directory: %s", entry.Name())
			} else {
				log.Info().Msgf("- File: %s", entry.Name())
			}
		}
	}
	
	// Look for the app directory directly
	appDir := filepath.Join(extractDir, "app")
	if _, err := os.Stat(appDir); err == nil {
		log.Info().Msgf("Found app directory at: %s", appDir)
		
		// Verify it contains floorp.exe
		if _, err := os.Stat(filepath.Join(appDir, "floorp.exe")); err == nil {
			log.Info().Msg("Found floorp.exe in app directory")
			return appDir, nil
		} else {
			log.Warn().Msg("app directory found but does not contain floorp.exe")
		}
	} else {
		log.Info().Msg("No direct 'app' directory found, searching recursively...")
	}
	
	// Try to find app directory recursively
	var foundAppDir string
	err = filepath.Walk(extractDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			log.Warn().Err(err).Msgf("Error accessing path: %s", path)
			return nil // Continue walking despite errors
		}
		if info.IsDir() && filepath.Base(path) == "app" {
			log.Info().Msgf("Found potential app directory: %s", path)

			// Check if this app directory contains floorp.exe
			if _, err := os.Stat(filepath.Join(path, "floorp.exe")); err == nil {
				log.Info().Msgf("Found floorp.exe in: %s", path)
				foundAppDir = path
				return filepath.SkipDir
			} else {
				log.Info().Msgf("Directory %s does not contain floorp.exe", path)
			}
		}
		return nil
	})

	if err != nil {
		log.Error().Err(err).Msg("Error during directory walk")
	}

	if foundAppDir != "" {
		log.Info().Msgf("Using app directory: %s", foundAppDir)
		return foundAppDir, nil
	}

	// Fallback: Look for any directory containing floorp.exe
	log.Info().Msg("No app directory found, looking for any directory with floorp.exe")
	var floorpDir string
	err = filepath.Walk(extractDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // Continue walking despite errors
		}

		if !info.IsDir() && filepath.Base(path) == "floorp.exe" {
			log.Info().Msgf("Found floorp.exe at: %s", path)
			floorpDir = filepath.Dir(path)
			return filepath.SkipDir
		}
		return nil
	})

	if floorpDir != "" {
		log.Info().Msgf("Using directory with floorp.exe: %s", floorpDir)
		return floorpDir, nil
	}

	log.Error().Msg("Could not find app directory or floorp.exe in extracted content")
	return "", fmt.Errorf("could not find app directory in extracted content")
}

// copyDir recursively copies a directory tree
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Get relative path
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}

		targetPath := filepath.Join(dst, relPath)

		// Create directories with same permissions
		if info.IsDir() {
			return os.MkdirAll(targetPath, info.Mode())
		}

		// Copy files
		return copyFile(path, targetPath)
	})
}

// copyFile copies a single file
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	if err != nil {
		return err
	}

	// Get and set file mode
	si, err := os.Stat(src)
	if err != nil {
		return err
	}

	return os.Chmod(dst, si.Mode())
}

// restartApp restarts the application
func restartApp() {
	log.Info().Msg("Restarting application after update...")

	// Build command to restart the application
	executable, err := os.Executable()
	if err != nil {
		log.Error().Err(err).Msg("Failed to get executable path")
		return
	}

	// Start the new process
	cmd := exec.Command(executable)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		log.Error().Err(err).Msg("Failed to restart application")
		return
	}

	// Exit current process
	os.Exit(0)
}

func createPolicies() error {
	appFile := utl.PathJoin(utl.CreateFolder(app.AppPath, "distribution"), "policies.json")
	dataFile := utl.PathJoin(app.DataPath, "policies.json")
	defaultPolicies := struct {
		Policies map[string]interface{} `json:"policies"`
	}{
		Policies: map[string]interface{}{
			"DisableAppUpdate":        true,
			"DontCheckDefaultBrowser": true,
		},
	}

	jsonPolicies, err := gabs.Consume(defaultPolicies)
	if err != nil {
		return errors.Wrap(err, "Cannot consume default policies")
	}
	log.Debug().Msgf("Default policies: %s", jsonPolicies.String())

	if utl.Exists(dataFile) {
		rawCustomPolicies, err := os.ReadFile(dataFile)
		if err != nil {
			return errors.Wrap(err, "Cannot read custom policies")
		}

		jsonPolicies, err = gabs.ParseJSON(rawCustomPolicies)
		if err != nil {
			return errors.Wrap(err, "Cannot consume custom policies")
		}
		log.Debug().Msgf("Custom policies: %s", jsonPolicies.String())

		jsonPolicies.Set(true, "policies", "DisableAppUpdate")
		jsonPolicies.Set(true, "policies", "DontCheckDefaultBrowser")
	}

	log.Debug().Msgf("Applied policies: %s", jsonPolicies.String())
	err = os.WriteFile(appFile, []byte(jsonPolicies.StringIndent("", "  ")), 0644)
	if err != nil {
		return errors.Wrap(err, "Cannot write policies")
	}

	return nil
}

func updateAddonStartup(profileFolder string) error {
	lz4File := path.Join(profileFolder, "addonStartup.json.lz4")
	if !utl.Exists(lz4File) || app.Prev.RootPath == "" {
		return nil
	}

	lz4Raw, err := mozLz4Decompress(lz4File)
	if err != nil {
		return err
	}

	prevPathLin := strings.Replace(utl.FormatUnixPath(app.Prev.RootPath), ` `, `%20`, -1)
	currPathLin := strings.Replace(utl.FormatUnixPath(app.RootPath), ` `, `%20`, -1)
	lz4Str := strings.Replace(string(lz4Raw), prevPathLin, currPathLin, -1)

	prevPathWin := strings.Replace(strings.Replace(utl.FormatWindowsPath(app.Prev.RootPath), `\`, `\\`, -1), ` `, `%20`, -1)
	currPathWin := strings.Replace(strings.Replace(utl.FormatWindowsPath(app.RootPath), `\`, `\\`, -1), ` `, `%20`, -1)
	lz4Str = strings.Replace(lz4Str, prevPathWin, currPathWin, -1)

	lz4Enc, err := mozLz4Compress([]byte(lz4Str))
	if err != nil {
		return err
	}

	return os.WriteFile(lz4File, lz4Enc, 0644)
}
