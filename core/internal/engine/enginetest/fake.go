// Package enginetest is a stand-in for sing-box in tests. A test binary
// re-executes itself with EnvScript set and becomes the fake: it serves the
// Clash API's /version on the port it was given and crashes, hangs or stays
// mute on demand. No real engine, network or admin rights are involved.
package enginetest

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"
)

const (
	// EnvScript turns a process into the fake engine. Its value lists what
	// each start in a row does, e.g. "crash,crash,ok"; the last repeats.
	//   ok    serve the API, exit cleanly when asked to stop
	//   crash print a reason and exit with code 3 at once
	//   hang  serve the API, ignore every request to stop
	//   mute  run without ever serving the API
	EnvScript = "STRELKA_FAKE_ENGINE"
	// EnvDir is a folder where the fake counts its starts (file "starts").
	EnvDir = "STRELKA_FAKE_DIR"
	// EnvAPI and EnvSecret give the Clash API address directly. Without
	// them the fake reads both from the config passed as "-c <file>", the
	// way the real engine learns them.
	EnvAPI    = "STRELKA_FAKE_API"
	EnvSecret = "STRELKA_FAKE_SECRET"
)

// Main must be the first call of TestMain. In an ordinary test run it
// returns at once; in a process started as the fake engine it never returns.
func Main() {
	script := os.Getenv(EnvScript)
	if script == "" {
		return
	}
	run(strings.Split(script, ","))
	os.Exit(0)
}

// Env is the environment that makes a re-executed test binary the fake.
func Env(script, dir, api, secret string) []string {
	env := append(os.Environ(), EnvScript+"="+script, EnvDir+"="+dir)
	if api != "" {
		env = append(env, EnvAPI+"="+api, EnvSecret+"="+secret)
	}
	return env
}

// Starts reads how many times the fake has been started in dir.
func Starts(dir string) int {
	raw, _ := os.ReadFile(filepath.Join(dir, "starts"))
	n, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	return n
}

func run(script []string) {
	n := 0
	if dir := os.Getenv(EnvDir); dir != "" {
		n = Starts(dir)
		os.WriteFile(filepath.Join(dir, "starts"), []byte(strconv.Itoa(n+1)), 0o600)
	}
	mode := script[len(script)-1]
	if n < len(script) {
		mode = script[n]
	}
	fmt.Fprintf(os.Stderr, "INFO fake engine start=%d mode=%s pid=%d\n", n+1, mode, os.Getpid())

	if mode == "crash" {
		fmt.Fprintln(os.Stderr, "FATAL fake engine: crashing as scripted")
		os.Exit(3)
	}

	stop := make(chan os.Signal, 1)
	var deaf atomic.Bool
	deaf.Store(mode == "hang")
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	if mode != "mute" {
		addr, secret := apiFromEnvOrConfig()
		mux := http.NewServeMux()
		mux.HandleFunc("/version", func(w http.ResponseWriter, r *http.Request) {
			if secret != "" && r.Header.Get("Authorization") != "Bearer "+secret {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			fmt.Fprint(w, `{"version":"fake-engine"}`)
		})
		// Test controls: make a healthy engine die or stop listening.
		mux.HandleFunc("/fake/crash", func(http.ResponseWriter, *http.Request) {
			fmt.Fprintln(os.Stderr, "FATAL fake engine: crashing on request")
			os.Exit(3)
		})
		mux.HandleFunc("/fake/hang", func(w http.ResponseWriter, _ *http.Request) {
			deaf.Store(true)
			fmt.Fprint(w, "ok")
		})
		go func() {
			if err := http.ListenAndServe(addr, mux); err != nil {
				fmt.Fprintf(os.Stderr, "FATAL fake engine: listen %s: %v\n", addr, err)
				os.Exit(4)
			}
		}()
	}

	for range stop {
		if deaf.Load() {
			fmt.Fprintln(os.Stderr, "WARN fake engine: ignoring the request to stop")
			continue
		}
		fmt.Fprintln(os.Stderr, "INFO fake engine: stopping")
		// Proof for tests that the stop was a polite one.
		if dir := os.Getenv(EnvDir); dir != "" {
			os.WriteFile(filepath.Join(dir, "graceful"), []byte(time.Now().Format(time.RFC3339Nano)), 0o600)
		}
		return
	}
}

func apiFromEnvOrConfig() (addr, secret string) {
	if addr = os.Getenv(EnvAPI); addr != "" {
		return addr, os.Getenv(EnvSecret)
	}
	for i, a := range os.Args {
		if a != "-c" || i+1 >= len(os.Args) {
			continue
		}
		raw, err := os.ReadFile(os.Args[i+1])
		if err != nil {
			fmt.Fprintf(os.Stderr, "FATAL fake engine: %v\n", err)
			os.Exit(5)
		}
		var cfg struct {
			Experimental struct {
				ClashAPI struct {
					Controller string `json:"external_controller"`
					Secret     string `json:"secret"`
				} `json:"clash_api"`
			} `json:"experimental"`
		}
		if err := json.Unmarshal(raw, &cfg); err != nil {
			fmt.Fprintf(os.Stderr, "FATAL fake engine: decode config: %v\n", err)
			os.Exit(5)
		}
		return cfg.Experimental.ClashAPI.Controller, cfg.Experimental.ClashAPI.Secret
	}
	fmt.Fprintln(os.Stderr, "FATAL fake engine: no API address given")
	os.Exit(5)
	return "", ""
}
