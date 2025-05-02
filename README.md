# Floorp Portable

<p align="center">
  <a href="https://github.com/Floorp-Projects/Floorp-Portable-v2/actions?workflow=build"><img src="https://img.shields.io/github/actions/workflow/status/Floorp-Projects/Floorp-Portable-v2/build.yml?label=build&logo=github&style=flat-square" alt="Build Status"></a>
  <a href="https://github.com/Floorp-Projects/Floorp-Portable-v2/releases/latest"><img src="https://img.shields.io/github/v/release/Floorp-Projects/Floorp-Portable-v2?style=flat-square" alt="GitHub Release"></a>
  <a href="https://github.com/Floorp-Projects/Floorp-Portable-v2/releases"><img src="https://img.shields.io/github/downloads/Floorp-Projects/Floorp-Portable-v2/total.svg?style=flat-square" alt="Total downloads"></a>
</p>

Portable version of [Floorp browser](https://floorp.app/) for Windows. Run it from any location without installation.

## About

Floorp Portable is packaged using the [Portapps](https://portapps.io/) framework. It allows you to run Floorp from any location, including USB drives, without leaving traces on the host computer.

## Usage

1. Download the latest version from the [releases page](https://github.com/Floorp-Projects/Floorp-Portable-v2/releases/latest)
2. Extract the archive to any location (e.g., USB drive)
3. Run `floorp-portable-win64.exe`

## Features

- Run Floorp without installation
- All user data stored in the application directory
- No registry changes or system-wide settings
- Easily move between computers

## Distribution & CI/CD

This repository uses GitHub Actions for Continuous Integration and Deployment:

### Automated Workflows

1. **Build Workflow** (.github/workflows/build.yml)

   - Builds Floorp Portable automatically on tag push or manual trigger
   - Creates GitHub releases with the packaged portable app

2. **Version Check Workflow** (.github/workflows/check-update.yml)
   - Runs every 12 hours to check for new Floorp versions
   - Creates a PR to update the version when a new version is detected
   - Can trigger an automatic build of the new version

### Manual Release

1. Go to Actions → build workflow → Run workflow
2. Enter the Floorp version number (e.g., 11.26.0)
3. When the workflow completes, find the draft release in the Releases section
4. Review and publish the release

### Distributing on Official Website

To distribute Floorp Portable on the official Floorp website:

1. Add links to the GitHub releases
2. Alternatively, set up a workflow that uploads the built artifacts to your web server
3. Add integration with the website's CMS to automatically update download links

Sample HTML for your website:

```html
<div class="download-section">
  <h2>Floorp Portable</h2>
  <p>Run Floorp from any location without installation</p>
  <a
    href="https://github.com/Floorp-Projects/Floorp-Portable-v2/releases/latest/download/floorp-portable-win64-latest.7z"
    class="download-button"
  >
    Download Floorp Portable
  </a>
  <p class="version-info">
    Latest version: <span id="portable-version">11.26.0</span>
  </p>
</div>
```

## Development

### Prerequisites

- Go 1.24+
- Java 11+
- Apache Ant 1.10+
- Node.js 20+

### Building Locally

1. Clone this repository
2. Clone the Portapps repository in a parallel directory
   ```
   git clone https://github.com/portapps/portapps.git ../portapps
   ```
3. Run the build process:
   ```
   ant release -Dcore.dir=../portapps
   ```
4. Find the packaged app in the `bin/release` directory

## License

Mozilla Public License 2.0 See `LICENSE` for more details.

## Credits

- [Floorp Browser](https://floorp.app/) by Ablaze
- [Portapps](https://portapps.io/) framework by CrazyMax
