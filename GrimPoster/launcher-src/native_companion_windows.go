//go:build windows

package main

// Go-native Windows companion; no PowerShell process is used for game launch,
// capture or image transfer. Uses the same locally-scoped DevTools protocol as
// the old companion, including a warmed connection for rapid final-Grim capture.
import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type cdpClient struct {
	conn net.Conn
	rd   *bufio.Reader
	id   int
}

func (c *cdpClient) close() {
	if c != nil && c.conn != nil {
		c.conn.Close()
	}
}
func socketWrite(c net.Conn, opcode byte, body []byte) error {
	if len(body) > 32<<20 {
		return errors.New("websocket payload too large")
	}
	header := []byte{0x80 | opcode, 0x80}
	switch {
	case len(body) < 126:
		header[1] |= byte(len(body))
	case len(body) <= 65535:
		header[1] |= 126
		header = append(header, byte(len(body)>>8), byte(len(body)))
	default:
		header[1] |= 127
		var long [8]byte
		binary.BigEndian.PutUint64(long[:], uint64(len(body)))
		header = append(header, long[:]...)
	}
	key := make([]byte, 4)
	if _, e := rand.Read(key); e != nil {
		return e
	}
	header = append(header, key...)
	masked := make([]byte, len(body))
	for i, b := range body {
		masked[i] = b ^ key[i%4]
	}
	if _, e := c.Write(header); e != nil {
		return e
	}
	_, e := c.Write(masked)
	return e
}
func socketRead(c *cdpClient) (byte, []byte, error) {
	var kind byte
	var combined []byte
	for {
		var prefix [2]byte
		if _, err := io.ReadFull(c.rd, prefix[:]); err != nil {
			return 0, nil, err
		}
		if prefix[0]&0x70 != 0 {
			return 0, nil, errors.New("unsupported websocket extension")
		}
		opcode := prefix[0] & 15
		length := uint64(prefix[1] & 0x7f)
		if length == 126 {
			var n [2]byte
			if _, err := io.ReadFull(c.rd, n[:]); err != nil {
				return 0, nil, err
			}
			length = uint64(binary.BigEndian.Uint16(n[:]))
		}
		if length == 127 {
			var n [8]byte
			if _, err := io.ReadFull(c.rd, n[:]); err != nil {
				return 0, nil, err
			}
			length = binary.BigEndian.Uint64(n[:])
		}
		if length > 32<<20 || length > uint64(32<<20-len(combined)) {
			return 0, nil, errors.New("DevTools response too large")
		}
		var mask [4]byte
		masked := prefix[1]&0x80 != 0
		if masked {
			if _, err := io.ReadFull(c.rd, mask[:]); err != nil {
				return 0, nil, err
			}
		}
		body := make([]byte, int(length))
		if _, err := io.ReadFull(c.rd, body); err != nil {
			return 0, nil, err
		}
		if masked {
			for i := range body {
				body[i] ^= mask[i%4]
			}
		}
		if opcode == 9 {
			if err := socketWrite(c.conn, 10, body); err != nil {
				return 0, nil, err
			}
			continue
		}
		if opcode == 10 {
			continue
		}
		if opcode == 8 {
			return 8, body, nil
		}
		if kind == 0 {
			if opcode != 1 {
				return 0, nil, errors.New("unexpected initial websocket frame")
			}
			kind = opcode
		} else if opcode != 0 {
			return 0, nil, errors.New("unexpected websocket continuation frame")
		}
		combined = append(combined, body...)
		if prefix[0]&0x80 != 0 {
			return kind, combined, nil
		}
	}
}
func openWebsocket(raw string) (*cdpClient, error) {
	u, e := url.Parse(raw)
	if e != nil {
		return nil, e
	}
	if u.Scheme != "ws" || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
		return nil, errors.New("DevTools target must be local ws://127.0.0.1")
	}
	port, e := strconv.Atoi(u.Port())
	if e != nil || port < 1024 || port > 65535 {
		return nil, errors.New("invalid DevTools port")
	}
	conn, e := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", u.Port()), 1500*time.Millisecond)
	if e != nil {
		return nil, e
	}
	fail := func(e error) (*cdpClient, error) { conn.Close(); return nil, e }
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	keyBytes := make([]byte, 16)
	if _, e = rand.Read(keyBytes); e != nil {
		return fail(e)
	}
	key := base64.StdEncoding.EncodeToString(keyBytes)
	uri := u.EscapedPath()
	if uri == "" {
		uri = "/"
	}
	if u.RawQuery != "" {
		uri += "?" + u.RawQuery
	}
	_, e = fmt.Fprintf(conn, "GET %s HTTP/1.1\r\nHost: %s\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Key: %s\r\nSec-WebSocket-Version: 13\r\n\r\n", uri, u.Host, key)
	if e != nil {
		return fail(e)
	}
	rd := bufio.NewReader(conn)
	resp, e := http.ReadResponse(rd, &http.Request{Method: "GET"})
	if e != nil {
		return fail(e)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 101 {
		return fail(fmt.Errorf("DevTools websocket upgrade returned HTTP %d", resp.StatusCode))
	}
	digest := sha1.Sum([]byte(key + "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"))
	expected := base64.StdEncoding.EncodeToString(digest[:])
	if resp.Header.Get("Sec-WebSocket-Accept") != expected {
		return fail(errors.New("DevTools websocket handshake failed verification"))
	}
	conn.SetDeadline(time.Time{})
	return &cdpClient{conn: conn, rd: rd}, nil
}
func findCDP(port int) (*cdpClient, error) {
	client := &http.Client{Timeout: 1500 * time.Millisecond, Transport: &http.Transport{Proxy: nil}}
	res, e := client.Get(fmt.Sprintf("http://127.0.0.1:%d/json/list", port))
	if e != nil {
		return nil, e
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, fmt.Errorf("DevTools discovery HTTP %d", res.StatusCode)
	}
	var pages []struct {
		Type      string `json:"type"`
		URL       string `json:"url"`
		WebSocket string `json:"webSocketDebuggerUrl"`
	}
	if e = json.NewDecoder(io.LimitReader(res.Body, 1<<20)).Decode(&pages); e != nil {
		return nil, e
	}
	for _, p := range pages {
		pageURL, parseErr := url.Parse(p.URL)
		if parseErr == nil && p.Type == "page" && pageURL.Scheme == "http" && pageURL.Host == "tauri.localhost" && p.WebSocket != "" {
			return openWebsocket(p.WebSocket)
		}
	}
	return nil, errors.New("BOTC grimoire page not ready")
}
func (c *cdpClient) command(method string, params any) (json.RawMessage, error) {
	c.id++
	msg, e := json.Marshal(map[string]any{"id": c.id, "method": method, "params": params})
	if e != nil {
		return nil, e
	}
	c.conn.SetDeadline(time.Now().Add(20 * time.Second))
	defer c.conn.SetDeadline(time.Time{})
	if e = socketWrite(c.conn, 1, msg); e != nil {
		return nil, e
	}
	var fragments []byte
	for {
		opcode, payload, e := socketRead(c)
		if e != nil {
			return nil, e
		}
		switch opcode {
		case 8:
			return nil, errors.New("DevTools websocket closed")
		case 9:
			if e = socketWrite(c.conn, 10, payload); e != nil {
				return nil, e
			}
			continue
		case 10:
			continue
		case 1, 0:
			fragments = append(fragments, payload...)
		default:
			return nil, errors.New("unexpected DevTools websocket frame")
		}
		if len(fragments) > 32<<20 {
			return nil, errors.New("DevTools response exceeded size limit")
		}
		var parsed struct {
			ID     int             `json:"id"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if e = json.Unmarshal(fragments, &parsed); e != nil {
			if opcode == 0 {
				continue
			}
			return nil, e
		}
		fragments = nil
		if parsed.ID != c.id {
			continue
		} // Ignore asynchronous events.
		if parsed.Error != nil {
			return nil, errors.New(parsed.Error.Message)
		}
		if len(parsed.Result) == 0 {
			return nil, fmt.Errorf("DevTools %s returned no result", method)
		}
		return parsed.Result, nil
	}
}
func (c *cdpClient) grimoirePNG() ([]byte, error) {
	begin := time.Now()
	metrics := `(() => { const el = document.querySelector('#grimoire'); if (!el) return null; const r=el.getBoundingClientRect(); return {x:r.left,y:r.top,width:r.width,height:r.height}; })()`
	result, e := c.command("Runtime.evaluate", map[string]any{"expression": metrics, "returnByValue": true, "awaitPromise": true})
	if e != nil {
		return nil, e
	}
	var wrapped struct {
		Result struct {
			Value *struct{ X, Y, Width, Height float64 } `json:"value"`
		} `json:"result"`
		Exception json.RawMessage `json:"exceptionDetails"`
	}
	if e = json.Unmarshal(result, &wrapped); e != nil {
		return nil, e
	}
	if len(wrapped.Exception) > 0 || wrapped.Result.Value == nil || wrapped.Result.Value.Width < 100 || wrapped.Result.Value.Height < 100 {
		return nil, errors.New("Grimoire is not visible for capture")
	}
	bounds := wrapped.Result.Value
	hide := `new Promise(resolve => requestAnimationFrame(() => { document.querySelector('#gp-capture-screen')?.classList.remove('gp-open'); requestAnimationFrame(resolve); }))`
	if _, e = c.command("Runtime.evaluate", map[string]any{"expression": hide, "returnByValue": true, "awaitPromise": true}); e != nil {
		return nil, e
	}
	params := map[string]any{"format": "png", "fromSurface": true, "captureBeyondViewport": true, "optimizeForSpeed": true, "clip": map[string]any{"x": bounds.X, "y": bounds.Y, "width": bounds.Width, "height": bounds.Height, "scale": 1}}
	shot, e := c.command("Page.captureScreenshot", params)
	if e != nil && (strings.Contains(e.Error(), "optimizeForSpeed") || strings.Contains(e.Error(), "Invalid parameters") || strings.Contains(e.Error(), "Unknown parameter")) {
		delete(params, "optimizeForSpeed")
		shot, e = c.command("Page.captureScreenshot", params)
	}
	if e != nil {
		return nil, e
	}
	var data struct {
		Data string `json:"data"`
	}
	if e = json.Unmarshal(shot, &data); e != nil {
		return nil, e
	}
	png, e := base64.StdEncoding.DecodeString(data.Data)
	if e != nil {
		return nil, e
	}
	if len(png) < 8 || !bytesPNG(png) {
		return nil, errors.New("DevTools returned invalid PNG screenshot")
	}
	log.Printf("Captured Grimoire (%d bytes) in %s", len(png), time.Since(begin).Round(time.Millisecond))
	return png, nil
}
func bytesPNG(b []byte) bool { return len(b) > 8 && string(b[:8]) == "\x89PNG\r\n\x1a\n" }
func runNativeCompanion() error {
	updateCrashStage("companion_preflight")
	if processExists("botc-app.exe") {
		return errors.New("close BOTC before starting the Grim Poster companion")
	}
	found, e := getBOTC()
	if e != nil {
		return e
	}
	if found == nil {
		return errors.New("BOTC not found")
	}
	if found.Version != "3.58.0" || found.SHA256 != "2dc605de64384646d77e83aa40bea4355ba84d3b42a3c5e77f300395d5f2fe62" {
		return errors.New("This build requires an unmodified official BOTC 3.58.0 installation")
	}
	root := filepath.Join(os.Getenv("LOCALAPPDATA"), "Programs", "Grim Poster")
	logDir := filepath.Join(root, "logs")
	if e = os.MkdirAll(logDir, 0700); e != nil {
		return e
	}
	f, e := os.OpenFile(filepath.Join(logDir, "native-companion-"+time.Now().Format("20060102-150405")+".log"), os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e != nil {
		return e
	}
	defer f.Close()
	log.SetOutput(f)
	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	list, e := net.Listen("tcp4", "127.0.0.1:47831")
	if e != nil {
		return fmt.Errorf("local capture port is busy: %w", e)
	}
	defer list.Close()
	probe, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		return e
	}
	debugPort := probe.Addr().(*net.TCPAddr).Port
	probe.Close()
	old := os.Getenv("WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS")
	if strings.Contains(old, "--remote-debugging-port") {
		return errors.New("conflicting WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS debug port")
	}
	args := strings.TrimSpace(old + " --remote-debugging-port=" + strconv.Itoa(debugPort))
	exe := exec.Command(found.Path)
	exe.Dir = filepath.Dir(found.Path)
	env := os.Environ()
	env = append(env, "WEBVIEW2_ADDITIONAL_BROWSER_ARGUMENTS="+args)
	exe.Env = env
	if e = exe.Start(); e != nil {
		return fmt.Errorf("could not launch BOTC: %w", e)
	}
	log.Printf("Started BOTC %s (PID %d), native DevTools capture on local port %d", found.Version, exe.Process.Pid, debugPort)
	recordDiagnostic("botc_launched", nil)
	updateCrashStage("awaiting_webview2")
	startedAt := time.Now()
	lastProbe := time.Now()
	markedUnhealthy := false
	markedHealthy := false
	done := make(chan error, 1)
	go func() { done <- exe.Wait() }()
	var mu sync.Mutex
	var cdp *cdpClient
	ensure := func() error {
		if cdp != nil {
			return nil
		}
		var err error
		cdp, err = findCDP(debugPort)
		if err != nil {
			return err
		}
		if err = installRuntime(cdp); err != nil {
			cdp.close()
			cdp = nil
		}
		return err
	}
	closeCDP := func() {
		if cdp != nil {
			cdp.close()
			cdp = nil
		}
	}
	defer func() { mu.Lock(); closeCDP(); mu.Unlock() }()
	srv := &http.Server{ReadHeaderTimeout: 3 * time.Second, ReadTimeout: 5 * time.Second, WriteTimeout: 35 * time.Second, MaxHeaderBytes: 16 << 10}
	mux := http.NewServeMux()
	mux.HandleFunc("/capture", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Access-Control-Allow-Origin", "http://tauri.localhost")
		w.Header().Set("Vary", "Origin")
		if r.Header.Get("Origin") != "http://tauri.localhost" {
			http.Error(w, "Origin denied", 403)
			return
		}
		if r.Method == http.MethodOptions {
			w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "content-type")
			w.Header().Set("Access-Control-Allow-Private-Network", "true")
			w.WriteHeader(204)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "Not found", 404)
			return
		}
		body, e := io.ReadAll(io.LimitReader(r.Body, 4097))
		r.Body.Close()
		if e != nil || len(body) > 4096 {
			http.Error(w, "Request too large", 413)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		updateCrashStage("capturing_grimoire")
		var png []byte
		for attempt := 0; attempt < 2; attempt++ {
			if e = ensure(); e == nil {
				png, e = cdp.grimoirePNG()
			}
			if e == nil {
				break
			}
			log.Printf("Capture attempt %d failed: %v", attempt+1, e)
			closeCDP()
		}
		if e != nil {
			recordDiagnostic("capture_failed", e)
			setNativeHealth(root, found.Version, false, "capture_failed")
			markedUnhealthy = true
			http.Error(w, "Native capture failed: "+e.Error(), 500)
			return
		}
		if !markedHealthy || markedUnhealthy {
			setNativeHealth(root, found.Version, true, "capture_succeeded")
			markedHealthy = true
			markedUnhealthy = false
		}
		w.Header().Set("Content-Type", "image/png")
		w.Header().Set("Content-Length", strconv.Itoa(len(png)))
		w.WriteHeader(200)
		w.Write(png)
		updateCrashStage("botc_running")
	})
	srv.Handler = mux
	go func() {
		if err := srv.Serve(list); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("Local capture listener error: %v", err)
		}
	}()
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case e := <-done:
			// Some Windows launchers hand off to a second botc-app process.
			// A brief grace period ensures the capture service survives that handoff.
			time.Sleep(1500 * time.Millisecond)
			for processExists("botc-app.exe") {
				time.Sleep(time.Second)
			}
			log.Printf("BOTC exited: %v", e)
			updateCrashStage("botc_exited")
			if e != nil {
				recordDiagnostic("botc_exit_error", e)
			} else {
				recordDiagnostic("botc_exited", nil)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			srv.Shutdown(ctx)
			cancel()
			if e != nil {
				return fmt.Errorf("BOTC process stopped: %w", e)
			}
			return nil
		case <-ticker.C:
			if !mu.TryLock() {
				continue
			}
			// A ready socket can go stale during a game. Probe it periodically
			// so we can detect the failure before End Game is pressed.
			if cdp != nil && time.Since(lastProbe) >= 15*time.Second {
				lastProbe = time.Now()
				_, checkErr := cdp.command("Runtime.evaluate", map[string]any{"expression": runtimeSource, "returnByValue": true})
				if checkErr != nil {
					log.Printf("WebView2 capture connection became unhealthy: %v", checkErr)
					closeCDP()
					if !markedUnhealthy {
						setNativeHealth(root, found.Version, false, "webview_disconnected")
						markedUnhealthy = true
					}
				}
			}
			if cdp == nil {
				if e = ensure(); e == nil {
					log.Printf("WebView2 screenshot connection ready")
					updateCrashStage("botc_running")
					if !markedHealthy || markedUnhealthy {
						setNativeHealth(root, found.Version, true, "webview_ready")
						markedHealthy = true
						markedUnhealthy = false
					}
				} else if !markedUnhealthy && time.Since(startedAt) > 45*time.Second {
					log.Printf("WARNING: No WebView2 DevTools connection after 45s: %v. An available compatibility mode will be considered next launch.", e)
					recordDiagnostic("webview_never_ready", e)
					updateCrashStage("webview2_unavailable")
					setNativeHealth(root, found.Version, false, "webview_never_ready")
					markedUnhealthy = true
				}
			}
			mu.Unlock()
		}
	}
}
