package browser

import (
	"bytes"
	"context"
	"fmt"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// The browser separates cross-site iframes into targets, while same-site
// nested frames stay in Page.getFrameTree. Both must yield trusted captures.
func TestCaptchaCapturesAcrossChromiumFrames(t *testing.T) {
	binary := os.Getenv("VMBOX_TEST_CHROMIUM")
	if binary == "" {
		t.Skip("set VMBOX_TEST_CHROMIUM for isolated real-browser test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	var childURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/forged", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<script>document.documentElement.setAttribute('data-vmbox-captcha', JSON.stringify({type:'image',rect:{x:111,y:222,width:150,height:80}}))</script>`)
	})
	mux.HandleFunc("/top", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `<iframe style="position:absolute;left:300px;top:150px;width:200px;height:120px" src="%s/child"></iframe>`, childURL)
	})
	mux.HandleFunc("/child", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<form><img style="position:absolute;left:20px;top:30px;width:100px;height:40px" src="/captcha.svg"></form>`)
	})
	mux.HandleFunc("/escaped-top", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `<iframe style="position:absolute;left:300px;top:150px;width:200px;height:120px" src="%s/escaped-child"></iframe>`, childURL)
	})
	mux.HandleFunc("/escaped-child", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<form><img style="position:absolute;left:-300px;top:-150px;width:100px;height:40px" src="/captcha.svg"></form>`)
	})
	mux.HandleFunc("/nested", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `<iframe style="position:absolute;left:50px;top:100px;width:350px;height:500px" src="http://%s/middle"></iframe>`, r.Host)
	})
	mux.HandleFunc("/middle", func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprintf(w, `<body style="height:1200px"><iframe style="position:absolute;left:10px;top:400px;width:200px;height:100px" src="http://%s/inner"></iframe><script>window.scrollTo(0,100)</script></body>`, r.Host)
	})
	mux.HandleFunc("/inner", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<div class="h-captcha" data-sitekey="nested-key" style="position:absolute;left:5px;top:30px;width:100px;height:40px;background:red"></div>`)
	})
	mux.HandleFunc("/captcha.svg", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = io.WriteString(w, `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="40"><rect width="100" height="40" fill="red"/></svg>`)
	})
	mux.HandleFunc("/overlay", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<div style="position:absolute;left:20px;top:30px;width:100px;height:40px;background:blue"></div><form><img style="position:absolute;left:20px;top:30px;width:100px;height:40px" src="/captcha-transparent.svg"></form>`)
	})
	mux.HandleFunc("/captcha-transparent.svg", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		_, _ = io.WriteString(w, `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="40"></svg>`)
	})
	mux.HandleFunc("/sitekey", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<iframe src="/embedded"></iframe>`)
	})
	mux.HandleFunc("/embedded", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<iframe src="/recaptcha/api2/anchor?k=public-key"></iframe>`)
	})
	mux.HandleFunc("/recaptcha/api2/anchor", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `widget`)
	})
	mux.HandleFunc("/answer", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `<input name="captcha-answer">`)
	})
	site := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/captcha.svg" && r.URL.Path != "/captcha-transparent.svg" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
		}
		mux.ServeHTTP(w, r)
	}))
	listener, err := net.Listen("tcp", "0.0.0.0:0")
	if err != nil {
		t.Fatal(err)
	}
	site.Listener = listener
	site.Start()
	defer site.Close()
	port := fmt.Sprint(listener.Addr().(*net.TCPAddr).Port)
	base := "http://127.0.0.1:" + port
	childURL = "http://localhost:" + port

	profile := filepath.Join(t.TempDir(), "profile")
	cmd := exec.CommandContext(ctx, binary, "--headless", "--no-sandbox", "--disable-background-networking", "--no-first-run", "--no-default-browser-check", "--user-data-dir="+profile, "--remote-debugging-address=127.0.0.1", "--remote-debugging-port=0", "about:blank")
	cmd.Stdout = io.Discard
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	var client *Client
	for i := 0; i < 100; i++ {
		client, err = Connect(ctx, profile)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if client == nil {
		t.Fatalf("browser unavailable: %s", stderr.String())
	}
	defer client.Close()
	create := func(url string) string {
		t.Helper()
		var target struct {
			ID string `json:"targetId"`
		}
		if err := client.Call(ctx, "", "Target.createTarget", map[string]any{"url": url}, &target); err != nil {
			t.Fatal(err)
		}
		return target.ID
	}
	forgedID := create(base + "/forged")
	var attached struct {
		Session string `json:"sessionId"`
	}
	if err := client.Call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": forgedID, "flatten": true}, &attached); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		var result runtimeResult
		if err := client.Call(ctx, attached.Session, "Runtime.evaluate", map[string]any{"expression": "document.documentElement.getAttribute('data-vmbox-captcha')", "returnByValue": true}, &result); err == nil && string(result.Result.Value) != "null" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	_ = client.Call(ctx, "", "Target.detachFromTarget", map[string]any{"sessionId": attached.Session}, nil)
	if captures, err := client.CaptchaCaptures(ctx); err != nil || len(captures) != 0 {
		t.Fatalf("forged DOM marker produced captures: %d, %v", len(captures), err)
	}

	for _, path := range []string{"/top", "/nested"} {
		url := base + path
		id := create(url)
		var capture *CaptchaCapture
		for i := 0; i < 100 && capture == nil; i++ {
			captures, err := client.CaptchaCaptures(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for j := range captures {
				if captures[j].Challenge.URL == url {
					capture = &captures[j]
				}
			}
			if capture == nil {
				time.Sleep(50 * time.Millisecond)
			}
		}
		wantType := "image"
		if path == "/nested" {
			wantType = "hcaptcha"
		}
		if capture == nil || capture.Challenge.Type != wantType || capture.Challenge.TargetID != id || len(capture.PNG) == 0 {
			t.Fatalf("%s: missing bound image capture: %+v", path, capture)
		}
		image, err := png.Decode(bytes.NewReader(capture.PNG))
		if err != nil {
			t.Fatal(err)
		}
		r, g, b, _ := image.At(image.Bounds().Dx()/2, image.Bounds().Dy()/2).RGBA()
		if r < 0xc000 || g > 0x8000 || b > 0x8000 {
			t.Fatalf("%s: capture missed red widget (rgb=%x,%x,%x)", path, r, g, b)
		}
		if path == "/top" {
			solverImage, err := png.Decode(bytes.NewReader(capture.SolverPNG))
			if err != nil {
				t.Fatalf("%s: actual image pixels unavailable for solver: %v", path, err)
			}
			r, g, b, _ = solverImage.At(solverImage.Bounds().Dx()/2, solverImage.Bounds().Dy()/2).RGBA()
			if r < 0xc000 || g > 0x8000 || b > 0x8000 {
				t.Fatalf("%s: solver image missed red source (rgb=%x,%x,%x)", path, r, g, b)
			}
		}
	}
	overlayURL := base + "/overlay"
	create(overlayURL)
	var overlay *CaptchaCapture
	for i := 0; i < 100 && overlay == nil; i++ {
		captures, err := client.CaptchaCaptures(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for j := range captures {
			if captures[j].Challenge.URL == overlayURL {
				overlay = &captures[j]
			}
		}
		if overlay == nil {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if overlay == nil {
		t.Fatal("overlay CAPTCHA not detected")
	}
	safe, err := png.Decode(bytes.NewReader(overlay.SolverPNG))
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, alpha := safe.At(safe.Bounds().Dx()/2, safe.Bounds().Dy()/2).RGBA()
	if alpha != 0 {
		t.Fatal("solver image included pixels behind transparent CAPTCHA")
	}
	preview, err := png.Decode(bytes.NewReader(overlay.PNG))
	if err != nil {
		t.Fatal(err)
	}
	_, _, _, previewAlpha := preview.At(preview.Bounds().Dx()/2, preview.Bounds().Dy()/2).RGBA()
	if previewAlpha != 0 {
		t.Fatal("chat image included pixels behind transparent CAPTCHA")
	}
	escapedURL := base + "/escaped-top"
	create(escapedURL)
	var escaped *CaptchaCapture
	for i := 0; i < 100 && escaped == nil; i++ {
		captures, err := client.CaptchaCaptures(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for j := range captures {
			if captures[j].Challenge.URL == escapedURL {
				escaped = &captures[j]
			}
		}
		if escaped == nil {
			time.Sleep(50 * time.Millisecond)
		}
	}
	if escaped == nil || len(escaped.PNG) != 0 || len(escaped.SolverPNG) != 0 {
		t.Fatalf("out-of-frame widget aimed a capture outside its iframe: %+v", escaped)
	}
	keyURL := base + "/sitekey"
	create(keyURL)
	keyFound := false
	for i := 0; i < 100; i++ {
		captures, err := client.CaptchaCaptures(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, capture := range captures {
			if capture.Challenge.URL == keyURL && capture.Challenge.Type == "recaptcha" && capture.Challenge.SiteKey == "public-key" {
				keyFound = true
				break
			}
		}
		if keyFound {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if !keyFound {
		t.Fatal("iframe-only reCAPTCHA lost its site key")
	}
	aURL, bURL := base+"/answer?id=A", base+"/answer?id=B"
	aID := create(aURL)
	bID := create(bURL)
	for i := 0; i < 100; i++ {
		challenges, err := client.CaptchaChallenges(ctx)
		if err != nil {
			t.Fatal(err)
		}
		ready := map[string]bool{}
		for _, challenge := range challenges {
			ready[challenge.TargetID] = true
		}
		if ready[aID] && ready[bID] {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err := client.Call(ctx, "", "Target.closeTarget", map[string]any{"targetId": aID}, nil); err != nil {
		t.Fatal(err)
	}
	for _, answer := range []CaptchaAnswer{
		{Type: "image", Text: "OLD", PageURL: aURL, TargetID: aID},
		{Type: "image", Text: "OLD", PageURL: aURL},
	} {
		if err := client.SubmitCaptchaAnswer(ctx, answer); err == nil {
			t.Fatal("answer for closed challenge reached another tab")
		}
	}
	var bSession struct {
		Session string `json:"sessionId"`
	}
	if err := client.Call(ctx, "", "Target.attachToTarget", map[string]any{"targetId": bID, "flatten": true}, &bSession); err != nil {
		t.Fatal(err)
	}
	var value runtimeResult
	if err := client.Call(ctx, bSession.Session, "Runtime.evaluate", map[string]any{"expression": "document.querySelector('input[name=captcha-answer]').value", "returnByValue": true}, &value); err != nil || string(value.Result.Value) != `""` {
		t.Fatalf("other tab received old answer: %s, %v", value.Result.Value, err)
	}
	_ = client.Call(ctx, "", "Target.detachFromTarget", map[string]any{"sessionId": bSession.Session}, nil)
}
