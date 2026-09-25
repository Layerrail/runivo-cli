package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const workspaceID = "11111111-1111-4111-8111-111111111111"
const serviceID = "22222222-2222-4222-8222-222222222222"
const deployID = "33333333-3333-4333-8333-333333333333"

func harness(t *testing.T, handler http.HandlerFunc) (func(...string) (int, string, string), *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	t.Setenv("RUNIVO_CONFIG_DIR", t.TempDir())
	t.Setenv("RUNIVO_API_KEY", "rnv_test")
	t.Setenv("RUNIVO_API_URL", server.URL)
	t.Setenv("RUNIVO_WORKSPACE", workspaceID)
	t.Setenv("RUNIVO_SERVICE", "")
	t.Setenv("RUNIVO_PROFILE", "")
	t.Setenv("RUNIVO_TOKEN_FILE", "")
	return func(args ...string) (int, string, string) {
		var out, err bytes.Buffer
		code := Execute(context.Background(), args, strings.NewReader(""), &out, &err, "test", "test")
		return code, out.String(), err.String()
	}, server
}
func services(w http.ResponseWriter) {
	_, _ = io.WriteString(w, `{"services":[{"id":"`+serviceID+`","name":"app","kind":"web","status":"live"}]}`)
}
func TestHelpOfflineAndEveryCommandRegistered(t *testing.T) {
	var out, err bytes.Buffer
	if code := Execute(context.Background(), []string{"--help"}, strings.NewReader(""), &out, &err, "test", "test"); code != 0 {
		t.Fatal(code, err.String())
	}
	for _, name := range []string{"deploy", "shell", "logs", "services", "billing", "backups", "secret-files", "github"} {
		if !strings.Contains(out.String(), name) {
			t.Error("missing", name)
		}
	}
}
func TestServiceCreatePayload(t *testing.T) {
	run, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/api/v1/workspaces/"+workspaceID+"/services" {
			t.Error(r.Method, r.URL.Path)
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		cfg, _ := body["configuration"].(map[string]any)
		if body["name"] != "my-app" || body["kind"] != "web" || cfg["plan"] != "free" || cfg["repository"] != "org/repo" {
			t.Errorf("bad body: %v", body)
		}
		_, _ = io.WriteString(w, `{"service":{"id":"`+serviceID+`"}}`)
	})
	code, _, err := run("services", "create", "--name", "my-app", "--type", "web", "--repo", "org/repo", "--plan", "free", "--json")
	if code != 0 {
		t.Fatal(err)
	}
}
func TestDeployWaitReportsFailureAndIdempotency(t *testing.T) {
	run, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/services"):
			services(w)
		case r.Method == "POST":
			if r.Header.Get("Idempotency-Key") == "" {
				t.Error("missing idempotency key")
			}
			_, _ = io.WriteString(w, `{"deployment":{"id":"`+deployID+`","status":"queued"}}`)
		default:
			_, _ = io.WriteString(w, `{"deployment":{"id":"`+deployID+`","status":"failed","error":"Build failed"}}`)
		}
	})
	code, out, err := run("deploy", "-s", "app", "--wait", "--json")
	if code != 9 || !strings.Contains(out, "failed") || !strings.Contains(err, "Build failed") {
		t.Fatal(code, out, err)
	}
}
func TestDeployWaitReturnsSuccessOnlyForLive(t *testing.T) {
	run, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/services") {
			services(w)
		} else {
			_, _ = io.WriteString(w, `{"deployment":{"id":"`+deployID+`","status":"live"}}`)
		}
	})
	code, out, err := run("deploys", "wait", deployID, "-s", "app", "--json")
	if code != 0 || !strings.Contains(out, "live") {
		t.Fatal(code, out, err)
	}
}
func TestLogsDrainCursorWithoutDuplicates(t *testing.T) {
	calls := 0
	run, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/services") {
			services(w)
			return
		}
		calls++
		if calls == 1 {
			if r.URL.Query().Get("after") != "0" {
				t.Error("initial cursor")
			}
			_, _ = io.WriteString(w, `{"items":[{"id":1,"message":"first"}],"cursor":1,"hasMore":true}`)
		} else {
			if r.URL.Query().Get("after") != "1" {
				t.Error("cursor not resumed")
			}
			_, _ = io.WriteString(w, `{"items":[{"id":2,"message":"second"}],"cursor":2,"hasMore":false}`)
		}
	})
	code, out, err := run("logs", "-s", "app", "--json")
	if code != 0 || calls != 2 || strings.Count(out, "first") != 1 || strings.Count(out, "second") != 1 {
		t.Fatal(code, out, err)
	}
}
func TestSecretSetUpdatesExistingWithoutLeakingValue(t *testing.T) {
	value := "secret-content\n"
	file := filepath.Join(t.TempDir(), "value")
	_ = os.WriteFile(file, []byte(value), 0600)
	run, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/services") {
			services(w)
			return
		}
		if r.Method == "GET" {
			_, _ = io.WriteString(w, `{"variables":[{"id":"`+deployID+`","key":"TOKEN"}]}`)
			return
		}
		if r.Method != "PATCH" || !strings.HasSuffix(r.URL.Path, deployID) {
			t.Error("not upserting")
		}
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["value"] != value {
			t.Error("secret bytes changed")
		}
		_, _ = io.WriteString(w, `{"variable":{"key":"TOKEN","value":null}}`)
	})
	code, out, err := run("env", "set", "TOKEN", "--file", file, "-s", "app", "--json")
	if code != 0 || strings.Contains(out, value) || strings.Contains(err, value) {
		t.Fatal(code, out, err)
	}
}
func TestDeleteRequiresConfirmationAndUsesServiceName(t *testing.T) {
	deleted := false
	run, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			services(w)
			return
		}
		deleted = true
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["confirm"] != "app" {
			t.Error("missing name confirmation")
		}
		_, _ = io.WriteString(w, `{"deleted":true}`)
	})
	if code, _, _ := run("services", "delete", "app", "--json"); code == 0 || deleted {
		t.Fatal("deleted without confirmation")
	}
	if code, _, err := run("services", "delete", "app", "--json", "--yes"); code != 0 || !deleted {
		t.Fatal(code, err)
	}
}
func TestPermissionErrorExitCode(t *testing.T) {
	run, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(403)
		_, _ = io.WriteString(w, `{"errors":[{"message":"Upgrade required"}]}`)
	})
	code, _, err := run("services", "list", "--json")
	if code != 4 || !strings.Contains(err, "Upgrade required") {
		t.Fatal(code, err)
	}
}
func TestRawAPIRejectsTraversal(t *testing.T) {
	called := false
	run, _ := harness(t, func(w http.ResponseWriter, r *http.Request) { called = true })
	for _, path := range []string{"../account/api-keys", "services/%2e%2e/account", "//api/v1/account"} {
		if code, _, _ := run("api", path); code == 0 || called {
			t.Fatal("accepted", path)
		}
	}
}
func TestSSEMultilineAndRevocation(t *testing.T) {
	input := ": heartbeat\n\nevent: services\ndata: {\ndata: \"services\": []}\n\nevent: revoked\ndata: {}\n\n"
	events := []string{}
	e := readEvents(strings.NewReader(input), func(event, data string) error {
		events = append(events, event)
		if event == "services" && !json.Valid([]byte(data)) {
			t.Error("invalid multiline JSON")
		}
		if event == "revoked" {
			return &exitError{4, "revoked"}
		}
		return nil
	})
	if e == nil || len(events) != 2 {
		t.Fatal(events, e)
	}
}
func TestDownloadIntegrityAndNoOverwrite(t *testing.T) {
	path := filepath.Join(t.TempDir(), "backup")
	sum := sha256.Sum256([]byte("correct"))
	checksum := hex.EncodeToString(sum[:])
	if e := downloadTo(strings.NewReader("wrong"), path, checksum); e == nil {
		t.Fatal("accepted corrupt backup")
	}
	if _, e := os.Stat(path); !os.IsNotExist(e) {
		t.Fatal("left corrupt output")
	}
	if e := downloadTo(strings.NewReader("correct"), path, checksum); e != nil {
		t.Fatal(e)
	}
	if e := downloadTo(strings.NewReader("changed"), path, ""); e == nil {
		t.Fatal("overwrote output")
	}
}
