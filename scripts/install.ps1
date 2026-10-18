$ErrorActionPreference = "Stop"
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Write-Error "Go is required. Install it from https://go.dev/dl/ or with winget install GoLang.Go."
}
$goVersion = (go env GOVERSION).TrimStart("go")
if ([version]$goVersion -lt [version]"1.24") {
    Write-Error "Go 1.24 or newer is required; found go$goVersion."
}
Write-Host "Installing Dorker from this source tree. No system packages or configuration will be changed."
go install ./cmd/dorker
Write-Host "Installed. If dorker is not found, add (go env GOPATH)\bin (or GOBIN) to PATH."
