# CalRunrilla

CalRunrilla is a Go CLI tool for calibrating Runrilla load cell bars.

## Quick build (PowerShell)

Build locally and embed a version:

```powershell
.
# Example: build 1.2.3 with today's date as build number
.
$version = '1.2.3'
.
./build.ps1 -Version $version -Out calrunrilla.exe
```

## GitHub Actions

A workflow is included in `.github/workflows/build.yml`. When you push a tag like `v1.2.3` the workflow will build `calrunrilla` and attach it as an artifact. The workflow sets `AppVersion` to the tag (without the leading `v`) and `AppBuild` to the build date (YYYYMMDD).

## Versioning

The binary accepts `-v` or `--version` to print the embedded AppVersion and AppBuild. When saving calibrated JSON the tool writes an adjacent `.version` file containing `AppVersion AppBuild` for traceability.

## First-time Git setup helper

There's a small PowerShell helper `git-setup.ps1` that initializes a git repository, creates the initial commit, adds an `origin` remote, pushes the initial branch, and optionally creates and pushes a tag.

Run it from the repository root (PowerShell):

```powershell
./git-setup.ps1
```

You'll be prompted for the remote URL. If you prefer non-interactive use, pass the `-RemoteUrl` and `-Branch` parameters.


