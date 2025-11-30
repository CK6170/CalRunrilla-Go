param(
  [string]$Version = "1.0.0",
  [string]$Out = "calrunrilla.exe"
)

# Build script that sets AppVersion and AppBuild via ldflags
$buildnum = (Get-Date -Format yyyyMMdd)
$ldflags = "-X main.AppVersion=$Version -X main.AppBuild=$buildnum"
Write-Host "Building with ldflags: $ldflags"
go build -ldflags $ldflags -o $Out .
if ($LASTEXITCODE -eq 0) { Write-Host "Built $Out" } else { Write-Error "Build failed"; exit 1 }
