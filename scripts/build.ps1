param([switch]$SkipDownload, [ValidateSet('x86','arm','all')][string]$Platform='x86')
$ErrorActionPreference = 'Stop'
$root = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$go = Join-Path $root '.tools/go/bin/go.exe'
$fnpack = Join-Path $root '.tools/fnpack.exe'
if (!(Test-Path $go)) { $go = (Get-Command go -ErrorAction Stop).Source }
if (!(Test-Path $fnpack)) { $fnpack = (Get-Command fnpack -ErrorAction Stop).Source }
$tools = Join-Path $root '.tools/mihomo'
New-Item -ItemType Directory -Force $tools | Out-Null
$release = 'v1.19.31'
$appVersion = '0.1.10'
$targets = @(
    @{ arch='amd64'; platform='x86'; asset='mihomo-linux-amd64-compatible-v1.19.31.gz'; sha='04cf9f09671704f839ddbee2e93069dc831a4123a75281e725d1d96ab9ac1afc' },
    @{ arch='arm64'; platform='arm'; asset='mihomo-linux-arm64-v1.19.31.gz'; sha='9e0f11afbf38426b8bd88fdc594678f8161c57eccb4e1b77acb12b493904f1d4' }
)
$targets = @($targets | Where-Object { $Platform -eq 'all' -or $_.platform -eq $Platform })
$ruleCommit = '0f3410e013082b242f381750899a533078914f34'
$ruleCache = Join-Path $root '.tools/rules'
New-Item -ItemType Directory -Force $ruleCache | Out-Null
$ruleFiles = @(
    @{ name='cn-domain.mrs'; upstream='geo/geosite/cn.mrs'; sha='6c4f403acc88c339a9aa77ecd7f711bc2506699cf810681b868547fcd1a480a1' },
    @{ name='cn-ip.mrs'; upstream='geo/geoip/cn.mrs'; sha='4cc9ab3b7e2bbd18e0420e09af42818d0748a596dd3daaed470bb2d9958d118d' }
)
foreach ($rule in $ruleFiles) {
    $cached = Join-Path $ruleCache $rule.name
    if (!(Test-Path $cached)) {
        if ($SkipDownload) { throw "Missing $cached" }
        $uri = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/$ruleCommit/$($rule.upstream)"
        curl.exe -L --fail --retry 2 -o $cached $uri
        if ($LASTEXITCODE -ne 0) { throw "Failed to download $uri" }
    }
    if ((Get-FileHash $cached -Algorithm SHA256).Hash.ToLowerInvariant() -ne $rule.sha) { throw "Rule data SHA256 mismatch: $($rule.name)" }
}
python (Join-Path $root 'scripts/make_icons.py')
New-Item -ItemType Directory -Force (Join-Path $root 'dist') | Out-Null
$builtFiles = @()
foreach ($target in $targets) {
    $archive = Join-Path $tools "$($target.arch).gz"
    if (!(Test-Path $archive)) {
        if ($SkipDownload) { throw "Missing $archive" }
        $uri = "https://github.com/MetaCubeX/mihomo/releases/download/$release/$($target.asset)"
        curl.exe -L --fail --retry 2 -o $archive $uri
        if ($LASTEXITCODE -ne 0) { throw "Failed to download $uri" }
    }
    $digest = (Get-FileHash $archive -Algorithm SHA256).Hash.ToLowerInvariant()
    if ($digest -ne $target.sha) { throw "Mihomo SHA256 mismatch for $($target.arch)" }
    $stage = Join-Path $root "build/$($target.platform)"
    $stageFull = [System.IO.Path]::GetFullPath($stage)
    $rootFull = [System.IO.Path]::GetFullPath($root).TrimEnd('\') + '\'
    if (!$stageFull.StartsWith($rootFull, [StringComparison]::OrdinalIgnoreCase)) { throw "Unsafe staging path: $stageFull" }
    if (Test-Path $stage) { Remove-Item -LiteralPath $stage -Recurse -Force }
    New-Item -ItemType Directory -Force "$stage/app/bin", "$stage/app/www", "$stage/app/ui/images", "$stage/app/licenses", "$stage/app/rules", "$stage/cmd", "$stage/config", "$stage/wizard" | Out-Null
    Copy-Item "$root/web/*" "$stage/app/www/"
    foreach ($rule in $ruleFiles) { Copy-Item (Join-Path $ruleCache $rule.name) (Join-Path $stage "app/rules/$($rule.name)") }
    Copy-Item "$root/packaging/ui/config" "$stage/app/ui/config"
    Copy-Item "$root/packaging/config/*" "$stage/config/"
    Copy-Item "$root/packaging/cmd/*" "$stage/cmd/"
    $expectedMachine = if ($target.arch -eq 'amd64') { 'x86_64' } else { 'aarch64' }
    $installInit = [System.IO.File]::ReadAllText((Join-Path $root 'packaging/cmd/install_init')).Replace('@ARCH@', $expectedMachine)
    [System.IO.File]::WriteAllText((Join-Path $stage 'cmd/install_init'), $installInit, [System.Text.UTF8Encoding]::new($false))
    Copy-Item "$root/packaging/wizard/.keep" "$stage/wizard/.keep"
    Copy-Item "$root/packaging/icons/icon_64.png" "$stage/app/ui/images/icon_64.png"
    Copy-Item "$root/packaging/icons/icon_256.png" "$stage/app/ui/images/icon_256.png"
    Copy-Item "$root/packaging/icons/icon_64.png" "$stage/app/ui/images/fnproxy_0110_64.png"
    Copy-Item "$root/packaging/icons/icon_256.png" "$stage/app/ui/images/fnproxy_0110_256.png"
    Copy-Item "$root/packaging/icons/icon_64.png" "$stage/ICON.PNG"
    Copy-Item "$root/packaging/icons/icon_256.png" "$stage/ICON_256.PNG"
    Copy-Item "$root/LICENSE" "$stage/LICENSE"
    Copy-Item "$root/licenses/Mihomo.LICENSE" "$stage/app/licenses/MIHOMO_LICENSE"
    Copy-Item "$root/licenses/yaml.v3.LICENSE" "$stage/app/licenses/YAML_LICENSE"
    Copy-Item "$root/licenses/MetaRulesDat.LICENSE" "$stage/app/licenses/RULES_LICENSE"
    $displayName = 'fnProxy'
    $manifest = @"
appname=fnvpn
version=$appVersion
display_name=$displayName
desc=Host TUN network proxy manager for fnOS
source=thirdparty
platform=$($target.platform)
maintainer=fnProxy contributors
desktop_uidir=ui
desktop_applaunchname=fnvpn.main
os_min_version=1.1.3100
ctl_stop=true
checkport=false
disable_authorization_path=true
"@
    [System.IO.File]::WriteAllText((Join-Path $stage 'manifest'), $manifest + "`n", [System.Text.UTF8Encoding]::new($false))
    $env:GOOS='linux'; $env:GOARCH=$target.arch; $env:CGO_ENABLED='0'
    Push-Location $root
    try {
        & $go build -trimpath -ldflags '-s -w' -o (Join-Path $stage 'app/bin/fnvpn') ./cmd/fnvpn
        if ($LASTEXITCODE -ne 0) { throw "Go build failed for $($target.arch)" }
    } finally { Pop-Location }
    $inputStream = [System.IO.File]::OpenRead($archive)
    try {
        $gzip = [System.IO.Compression.GZipStream]::new($inputStream, [System.IO.Compression.CompressionMode]::Decompress)
        try {
            $outputStream = [System.IO.File]::Create((Join-Path $stage 'app/bin/mihomo'))
            try { $gzip.CopyTo($outputStream) } finally { $outputStream.Dispose() }
        } finally { $gzip.Dispose() }
    } finally { $inputStream.Dispose() }
    Push-Location $stage
    try {
        & $fnpack build
        if ($LASTEXITCODE -ne 0) { throw "fnpack failed for $($target.platform)" }
    } finally { Pop-Location }
    $fpk = Get-ChildItem -LiteralPath $stage -Filter '*.fpk' | Select-Object -First 1
    if (!$fpk) { throw "fnpack did not produce FPK for $($target.platform)" }
    python (Join-Path $root 'scripts/fix_fpk_modes.py') $fpk.FullName
    if ($LASTEXITCODE -ne 0) { throw "Failed to set Unix modes for $($target.platform)" }
    $destination = Join-Path $root "dist/fnproxy_$($appVersion)_$($target.platform).fpk"
    Copy-Item $fpk.FullName $destination -Force
    $builtFiles += $destination
}
python (Join-Path $root 'scripts/verify_fpk.py') @builtFiles
if ($LASTEXITCODE -ne 0) { throw 'FPK structural verification failed' }
$hashes = Get-FileHash $builtFiles -Algorithm SHA256
$sumLines = $hashes | ForEach-Object { "$($_.Hash.ToLowerInvariant())  $([System.IO.Path]::GetFileName($_.Path))" }
[System.IO.File]::WriteAllLines((Join-Path $root 'dist/SHA256SUMS.txt'), $sumLines, [System.Text.UTF8Encoding]::new($false))
$sumLines
