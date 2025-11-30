<#
Simple git setup helper for Windows PowerShell.

Usage: Run this script from the repository root and follow prompts.
It will initialize a git repo (if needed), create an initial commit, add a remote,
and push the initial branch.
#>

param(
    [string]$RemoteUrl = '',
    [string]$Branch = 'main',
    [string]$CommitMessage = 'Initial commit: add CalRunrilla source and CI helpers'
)

function Invoke-GitCommand {
    param($GitArgs)
    # Allow either an array of args or a single string. Convert to array for invocation.
    if ($GitArgs -is [System.Array]) {
        $parts = $GitArgs
    } else {
        # Simple split on spaces; for more complex needs pass an array
        $parts = $GitArgs -split ' '
    }

    $cmd = "git " + ($parts -join ' ')
    Write-Host "> $cmd"
    & git @parts
    return $LASTEXITCODE
}

# Ensure git is available
try {
    git --version > $null 2>&1
} catch {
    Write-Error "git is not installed or not in PATH. Install Git and re-run this script."
    exit 1
}

# Confirm working directory
Write-Host "Working directory: $(Get-Location)"

# Initialize repo if needed
if (-not (Test-Path -Path .git)) {
    Write-Host "No .git found - initializing repository..."
    if (Invoke-GitCommand @('init') -ne 0) { Write-Error 'git init failed'; exit 1 }
    Write-Host "Repository initialized."
} else {
    Write-Host ".git already present - using existing repository."
}

# Ask for remote if not provided
if (-not $RemoteUrl -or $RemoteUrl -eq '') {
    $RemoteUrl = Read-Host 'Enter remote repository URL (e.g. https://github.com/you/CalRunrilla.git)'
}

if (-not $RemoteUrl -or $RemoteUrl -eq '') {
    Write-Error 'No remote provided - aborting.'
    exit 1
}

# Add files and commit
Write-Host "Adding files and committing with message:`n  $CommitMessage"
if (Invoke-GitCommand @('add','--all') -ne 0) { Write-Error 'git add failed'; exit 1 }
# Check if there's anything to commit
$changes = (& git status --porcelain)
if (-not $changes) {
    Write-Host 'No changes to commit.'
} else {
    if (Invoke-GitCommand @('commit','-m',$CommitMessage) -ne 0) { Write-Error 'git commit failed'; exit 1 }
}

# Check if remote already exists
$existingRemotes = (& git remote)
if ($existingRemotes -contains 'origin') {
    Write-Host "Remote 'origin' already exists. Will set URL to provided value."
    if (Invoke-GitCommand @('remote','set-url','origin',$RemoteUrl) -ne 0) { Write-Error 'git remote set-url failed'; exit 1 }
} else {
    if (Invoke-GitCommand @('remote','add','origin',$RemoteUrl) -ne 0) { Write-Error 'git remote add failed'; exit 1 }
}

# Push branch
Write-Host "Pushing branch '$Branch' to origin..."
if (Invoke-GitCommand @('push','-u','origin',$Branch) -ne 0) {
    Write-Error 'git push failed. If the remote repository is empty, ensure you have permission and the branch name is correct.'
    exit 1
}

Write-Host "Push complete." -ForegroundColor Green

# Offer to push a tag
$pushTag = Read-Host 'Create and push a tag? (y/N)'
if ($pushTag -match '^[Yy]') {
    $tag = Read-Host 'Enter tag name (e.g. v1.0.0)'
    if ($tag -ne '') {
        if (Invoke-GitCommand @('tag',$tag) -ne 0) { Write-Error 'git tag creation failed'; exit 1 }
        if (Invoke-GitCommand @('push','origin',$tag) -ne 0) { Write-Error 'git push tag failed'; exit 1 }
        Write-Host "Tag $tag pushed." -ForegroundColor Green
    }
}

Write-Host 'Repository setup complete.' -ForegroundColor Green
