$ErrorActionPreference = 'Stop'
. (Join-Path $PSScriptRoot 'GrimPoster.Common.ps1')

$debugPort = 0
$capturePort = 47831
$allowedOrigin = 'http://tauri.localhost'
$appProcess = $null
$listener = $null
$oldBrowserArguments = [Environment]::GetEnvironmentVariable('WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS', 'Process')
# Keep WebView2's debugging connection ready between games; do not hold up End Game to reconnect.
$script:cdpSocket = $null
$script:cdpCommandId = 0
$script:nextWarmCheck = [DateTime]::UtcNow.AddSeconds(2)

$updateChecker = Join-Path $PSScriptRoot 'Check-GrimPosterUpdate.ps1'
if (Test-Path -LiteralPath $updateChecker -PathType Leaf) {
    & $updateChecker
}

function Write-CompanionLog {
    param([string]$Message)
    $line = "[{0}] {1}" -f (Get-Date -Format 'yyyy-MM-dd HH:mm:ss'), $Message
    Write-Host $line
}

function Send-HttpResponse {
    param(
        [System.Net.Sockets.NetworkStream]$Stream,
        [int]$StatusCode,
        [string]$Reason,
        [string]$ContentType,
        [byte[]]$Body,
        [string]$Origin,
        [switch]$Preflight
    )
    $headers = @(
        "HTTP/1.1 $StatusCode $Reason",
        "Content-Type: $ContentType",
        "Content-Length: $($Body.Length)",
        'Connection: close',
        'Cache-Control: no-store',
        "Access-Control-Allow-Origin: $Origin",
        'Vary: Origin'
    )
    if ($Preflight) {
        $headers += 'Access-Control-Allow-Methods: POST, OPTIONS'
        $headers += 'Access-Control-Allow-Headers: content-type'
        $headers += 'Access-Control-Allow-Private-Network: true'
        $headers += 'Access-Control-Max-Age: 60'
    }
    $headers += ''
    $headers += ''
    $headerBytes = [Text.Encoding]::ASCII.GetBytes(($headers -join "`r`n"))
    $Stream.Write($headerBytes, 0, $headerBytes.Length)
    if ($Body.Length -gt 0) { $Stream.Write($Body, 0, $Body.Length) }
    $Stream.Flush()
}

function Read-HttpRequest {
    param([System.Net.Sockets.NetworkStream]$Stream)
    $headerBytes = New-Object 'System.Collections.Generic.List[byte]'
    $tail = New-Object 'System.Collections.Generic.Queue[byte]'
    while ($headerBytes.Count -lt 16384) {
        $value = $Stream.ReadByte()
        if ($value -lt 0) { throw 'The local request ended before its headers were complete.' }
        $headerBytes.Add([byte]$value)
        $tail.Enqueue([byte]$value)
        if ($tail.Count -gt 4) { [void]$tail.Dequeue() }
        if ($tail.Count -eq 4 -and [Text.Encoding]::ASCII.GetString($tail.ToArray()) -eq "`r`n`r`n") { break }
    }
    if ($headerBytes.Count -ge 16384) { throw 'The local request headers exceeded the size limit.' }

    $headerText = [Text.Encoding]::ASCII.GetString($headerBytes.ToArray())
    $lines = $headerText -split "`r`n"
    $requestLine = $lines[0] -split ' ', 3
    if ($requestLine.Length -lt 2) { throw 'The local request line was invalid.' }
    $headers = @{}
    foreach ($line in $lines | Select-Object -Skip 1) {
        $separator = $line.IndexOf(':')
        if ($separator -gt 0) { $headers[$line.Substring(0, $separator).Trim().ToLowerInvariant()] = $line.Substring($separator + 1).Trim() }
    }
    $bodyLength = 0
    if ($headers.ContainsKey('content-length')) {
        if (-not [int]::TryParse($headers['content-length'], [ref]$bodyLength) -or $bodyLength -lt 0 -or $bodyLength -gt 4096) {
            throw 'The local request body size was invalid.'
        }
    }
    $remaining = $bodyLength
    while ($remaining -gt 0) {
        $discard = New-Object byte[] ([Math]::Min(1024, $remaining))
        $read = $Stream.Read($discard, 0, $discard.Length)
        if ($read -le 0) { throw 'The local request body was incomplete.' }
        $remaining -= $read
    }
    return [pscustomobject]@{ Method = $requestLine[0].ToUpperInvariant(); Path = $requestLine[1]; Headers = $headers }
}

function Send-CdpCommand {
    param(
        [System.Net.WebSockets.ClientWebSocket]$Socket,
        [int]$Id,
        [string]$Method,
        [hashtable]$Parameters
    )
    $payload = @{ id = $Id; method = $Method; params = $Parameters } | ConvertTo-Json -Compress -Depth 20
    $sendBytes = [Text.Encoding]::UTF8.GetBytes($payload)
    $sendSegment = [ArraySegment[byte]]::new($sendBytes)
    $sendTimeout = New-Object System.Threading.CancellationTokenSource
    $sendTimeout.CancelAfter(10000)
    try {
        # PowerShell can emit the awaited Task's VoidTaskResult to the output
        # pipeline. Suppress it or it contaminates Send-CdpCommand's return
        # value alongside the actual DevTools reply.
        [void]($Socket.SendAsync($sendSegment, [System.Net.WebSockets.WebSocketMessageType]::Text, $true, $sendTimeout.Token).GetAwaiter().GetResult())
    } finally { $sendTimeout.Dispose() }

    while ($true) {
        $buffer = New-Object byte[] 65536
        $memory = New-Object System.IO.MemoryStream
        $receiveTimeout = New-Object System.Threading.CancellationTokenSource
        $receiveTimeout.CancelAfter(20000)
        try {
            do {
                $segment = [ArraySegment[byte]]::new($buffer)
                $received = $Socket.ReceiveAsync($segment, $receiveTimeout.Token).GetAwaiter().GetResult()
                if ($received.MessageType -eq [System.Net.WebSockets.WebSocketMessageType]::Close) { throw 'The BOTC DevTools connection closed.' }
                $memory.Write($buffer, 0, $received.Count)
            } while (-not $received.EndOfMessage)
            $message = [Text.Encoding]::UTF8.GetString($memory.ToArray()) | ConvertFrom-Json
        } finally { $receiveTimeout.Dispose(); $memory.Dispose() }
        # WebView2 can send unsolicited DevTools events without an `id`, and
        # successful command replies omit `error`. StrictMode treats direct
        # access to either missing property as an exception, so inspect the
        # parsed object's properties before reading them.
        $messageId = $message.PSObject.Properties['id']
        if (-not $messageId -or $messageId.Value -ne $Id) { continue }
        $errorProperty = $message.PSObject.Properties['error']
        if ($errorProperty -and $errorProperty.Value) {
            throw "BOTC DevTools error: $($errorProperty.Value.message)"
        }
        $resultProperty = $message.PSObject.Properties['result']
        if (-not $resultProperty) {
            $responseJson = ConvertTo-Json -InputObject $message -Compress -Depth 12
            throw "BOTC DevTools returned no result for '$Method' (request $Id): $responseJson"
        }
        return $resultProperty.Value
    }
}

function Close-CdpConnection {
    if ($script:cdpSocket) {
        try { $script:cdpSocket.Dispose() } catch { }
        $script:cdpSocket = $null
    }
}

function Find-CdpPage {
    param([int]$Attempts = 1)
    for ($attempt = 0; $attempt -lt $Attempts; $attempt++) {
        try {
            $request = [System.Net.HttpWebRequest]::Create("http://127.0.0.1:$debugPort/json/list")
            $request.Proxy = $null
            $request.Timeout = 1200
            $response = $request.GetResponse()
            try {
                $reader = New-Object System.IO.StreamReader($response.GetResponseStream())
                try { $targets = $reader.ReadToEnd() | ConvertFrom-Json }
                finally { $reader.Dispose() }
            } finally { $response.Dispose() }
            $target = $targets | Where-Object { $_.type -eq 'page' -and $_.url -like 'http://tauri.localhost*' -and $_.webSocketDebuggerUrl } | Select-Object -First 1
            if ($target) { return $target }
        } catch { }
        if ($attempt -lt ($Attempts - 1)) { Start-Sleep -Milliseconds 250 }
    }
    return $null
}

function Open-CdpConnection {
    param([int]$Attempts = 1)
    if ($script:cdpSocket -and $script:cdpSocket.State -eq [System.Net.WebSockets.WebSocketState]::Open) {
        return $true
    }
    Close-CdpConnection
    $target = Find-CdpPage -Attempts $Attempts
    if (-not $target) { return $false }
    $socket = New-Object System.Net.WebSockets.ClientWebSocket
    $tokenSource = New-Object System.Threading.CancellationTokenSource
    $tokenSource.CancelAfter(5000)
    try {
        $socket.Options.KeepAliveInterval = [TimeSpan]::FromSeconds(20)
        [void]($socket.ConnectAsync([Uri]$target.webSocketDebuggerUrl, $tokenSource.Token).GetAwaiter().GetResult())
        $script:cdpSocket = $socket
        return $true
    } catch {
        $socket.Dispose()
        throw
    } finally { $tokenSource.Dispose() }
}

function Get-GrimoirePng {
    # Logs separately account for connection setup and the actual PNG capture.
    # Chromium's optimizeForSpeed flag does faster encoding; the image stays lossless.
    $watch = [Diagnostics.Stopwatch]::StartNew()
    for ($attempt = 0; $attempt -lt 2; $attempt++) {
        $wasWarm = [bool]($script:cdpSocket -and $script:cdpSocket.State -eq [System.Net.WebSockets.WebSocketState]::Open)
        try {
            if (-not (Open-CdpConnection -Attempts 3)) {
                throw 'Could not find the BOTC page through its local WebView2 DevTools endpoint. Start BOTC using this companion.'
            }
            $connectedAt = $watch.ElapsedMilliseconds
            $socket = $script:cdpSocket

            $metricsExpression = "(() => { const el = document.querySelector('#grimoire'); if (!el) return null; const r = el.getBoundingClientRect(); return {x:r.left,y:r.top,width:r.width,height:r.height}; })()"
            $metrics = Send-CdpCommand -Socket $socket -Id (++$script:cdpCommandId) -Method 'Runtime.evaluate' -Parameters @{ expression = $metricsExpression; returnByValue = $true; awaitPromise = $true }
            $evaluationProperty = $metrics.PSObject.Properties['result']
            if (-not $evaluationProperty) {
                $responseJson = ConvertTo-Json -InputObject $metrics -Compress -Depth 12
                throw "Runtime.evaluate returned no evaluation result: $responseJson"
            }
            $evaluation = $evaluationProperty.Value
            $exceptionProperty = $evaluation.PSObject.Properties['exceptionDetails']
            if ($exceptionProperty -and $exceptionProperty.Value) {
                $responseJson = ConvertTo-Json -InputObject $evaluation -Compress -Depth 12
                throw "Runtime.evaluate failed in the BOTC page: $responseJson"
            }
            $valueProperty = $evaluation.PSObject.Properties['value']
            if (-not $valueProperty) {
                $responseJson = ConvertTo-Json -InputObject $evaluation -Compress -Depth 12
                throw "Runtime.evaluate returned no by-value payload: $responseJson"
            }
            $clip = $valueProperty.Value
            if (-not $clip -or $clip.width -lt 100 -or $clip.height -lt 100) { throw 'The BOTC grimoire is not currently visible.' }
            $metricsAt = $watch.ElapsedMilliseconds

            $hideStatusExpression = "new Promise(resolve => requestAnimationFrame(() => { document.querySelector('#gp-capture-screen')?.classList.remove('gp-open'); requestAnimationFrame(resolve); }))"
            [void](Send-CdpCommand -Socket $socket -Id (++$script:cdpCommandId) -Method 'Runtime.evaluate' -Parameters @{ expression = $hideStatusExpression; returnByValue = $true; awaitPromise = $true })
            $overlayAt = $watch.ElapsedMilliseconds
            $shotParams = @{
                format = 'png'
                fromSurface = $true
                captureBeyondViewport = $true
                optimizeForSpeed = $true
                clip = @{ x = [double]$clip.x; y = [double]$clip.y; width = [double]$clip.width; height = [double]$clip.height; scale = 1 }
            }
            try {
                $shot = Send-CdpCommand -Socket $socket -Id (++$script:cdpCommandId) -Method 'Page.captureScreenshot' -Parameters $shotParams
            } catch {
                # Older Chromium builds may not know the optional speed flag.
                if ($_.Exception.Message -notmatch 'Invalid parameters|Unknown parameter|optimizeForSpeed') { throw }
                [void]$shotParams.Remove('optimizeForSpeed')
                $shot = Send-CdpCommand -Socket $socket -Id (++$script:cdpCommandId) -Method 'Page.captureScreenshot' -Parameters $shotParams
            }
            $imageDataProperty = $shot.PSObject.Properties['data']
            if (-not $imageDataProperty) {
                $responseJson = ConvertTo-Json -InputObject $shot -Compress -Depth 12
                throw "Page.captureScreenshot returned no image data: $responseJson"
            }
            $shotAt = $watch.ElapsedMilliseconds
            $png = [Convert]::FromBase64String([string]$imageDataProperty.Value)
            if ($png.Length -lt 8) { throw 'The native screenshot is empty.' }
            $doneAt = $watch.ElapsedMilliseconds
            Write-CompanionLog ("Capture timings: connection {0}ms; bounds {1}ms; overlay {2}ms; PNG {3}ms; decode {4}ms; total {5}ms; size {6:N0} bytes" -f $connectedAt, ($metricsAt - $connectedAt), ($overlayAt - $metricsAt), ($shotAt - $overlayAt), ($doneAt - $shotAt), $doneAt, $png.Length)
            # Critical: return the byte array as ONE object, not thousands of
            # individual PowerShell pipeline values; enumerating can be slow.
            return ,$png
        } catch {
            $message = $_.Exception.Message
            Close-CdpConnection
            if ($attempt -eq 0 -and $wasWarm -and $message -match 'WebSocket|connection|socket|closed|timed out|timeout') {
                Write-CompanionLog 'The cached DevTools connection ended; reconnecting once.'
                continue
            }
            throw
        }
    }
}

try {
    if (Get-Process -Name 'botc-app' -ErrorAction SilentlyContinue) {
        throw 'Close BOTC completely, then launch it with this companion.'
    }
    $appPath = Get-BotcExecutable
    $listener = [System.Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, $capturePort)
    $listener.Start()

    $portProbe = [System.Net.Sockets.TcpListener]::new([Net.IPAddress]::Loopback, 0)
    $portProbe.Start()
    $debugPort = ([Net.IPEndPoint]$portProbe.LocalEndpoint).Port
    $portProbe.Stop()

    if ($oldBrowserArguments -match '--remote-debugging-port') {
        throw 'WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS already specifies a debugging port. Remove that conflicting setting before using the companion.'
    }
    $newBrowserArguments = @($oldBrowserArguments, "--remote-debugging-port=$debugPort") -join ' '
    [Environment]::SetEnvironmentVariable('WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS', $newBrowserArguments, 'Process')
    $appProcess = Start-Process -FilePath $appPath -WorkingDirectory (Split-Path -Parent $appPath) -PassThru
    [Environment]::SetEnvironmentVariable('WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS', $oldBrowserArguments, 'Process')

    Write-CompanionLog 'BOTC started with a loopback-only WebView2 capture endpoint.'
    Write-CompanionLog 'Leave this window open while playing. Grim Poster will request a screenshot when it captures the Grimoire.'
    Write-CompanionLog 'Press Ctrl+C to stop the companion after closing BOTC.'

    while (-not $appProcess.HasExited -or (Get-Process -Name 'botc-app' -ErrorAction SilentlyContinue)) {
        if (-not $listener.Pending()) {
            # Warm the CDP connection during gameplay, NOT when ending a game.
            # Fail silently until BOTC has finished loading; screenshot requests
            # will still open a connection on demand if warming hasn't succeeded.
            if ((-not $script:cdpSocket -or $script:cdpSocket.State -ne [System.Net.WebSockets.WebSocketState]::Open) -and [DateTime]::UtcNow -ge $script:nextWarmCheck) {
                $script:nextWarmCheck = [DateTime]::UtcNow.AddSeconds(3)
                try {
                    if (Open-CdpConnection -Attempts 1) { Write-CompanionLog 'WebView2 screenshot connection ready.' }
                } catch { }
            }
            Start-Sleep -Milliseconds 120
            continue
        }
        $client = $listener.AcceptTcpClient()
        try {
            $stream = $client.GetStream()
            try {
                $request = Read-HttpRequest -Stream $stream
                $origin = [string]$request.Headers['origin']
                if ($origin -ne $allowedOrigin) {
                    $body = [Text.Encoding]::UTF8.GetBytes('Origin denied')
                    Send-HttpResponse -Stream $stream -StatusCode 403 -Reason 'Forbidden' -ContentType 'text/plain; charset=utf-8' -Body $body -Origin $allowedOrigin
                } elseif ($request.Method -eq 'OPTIONS' -and $request.Path -eq '/capture') {
                    Send-HttpResponse -Stream $stream -StatusCode 204 -Reason 'No Content' -ContentType 'text/plain' -Body ([byte[]]@()) -Origin $allowedOrigin -Preflight
                } elseif ($request.Method -eq 'POST' -and $request.Path -eq '/capture') {
                    try {
                        $png = Get-GrimoirePng
                        $responseWatch = [Diagnostics.Stopwatch]::StartNew()
                        Send-HttpResponse -Stream $stream -StatusCode 200 -Reason 'OK' -ContentType 'image/png' -Body $png -Origin $allowedOrigin
                        Write-CompanionLog ("Captured Grimoire ({0:N0} bytes); local HTTP transfer {1}ms." -f $png.Length, $responseWatch.ElapsedMilliseconds)
                    } catch {
                        $body = [Text.Encoding]::UTF8.GetBytes($_.Exception.Message)
                        Send-HttpResponse -Stream $stream -StatusCode 500 -Reason 'Capture Failed' -ContentType 'text/plain; charset=utf-8' -Body $body -Origin $allowedOrigin
                        Write-CompanionLog ("Capture failed: {0}" -f $_.Exception.Message)
                    }
                } else {
                    $body = [Text.Encoding]::UTF8.GetBytes('Not found')
                    Send-HttpResponse -Stream $stream -StatusCode 404 -Reason 'Not Found' -ContentType 'text/plain; charset=utf-8' -Body $body -Origin $allowedOrigin
                }
            } finally { $stream.Dispose() }
        } catch { Write-CompanionLog ("Local request failed: {0}" -f $_.Exception.Message) }
        finally { $client.Dispose() }
    }
} catch {
    Write-CompanionLog ("Stopped: {0}" -f $_.Exception.Message)
    Write-Host 'Press any key to close.'
    [void][Console]::ReadKey($true)
    exit 1
} finally {
    [Environment]::SetEnvironmentVariable('WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS', $oldBrowserArguments, 'Process')
    Close-CdpConnection
    if ($listener) { $listener.Stop(); $listener.Server.Dispose() }
}
