package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestMySQLRestoreForwardsOnlyExplicitTargetPlan(t *testing.T) {
	for _, plan := range []string{"", "mysql-free", "mysql-starter"} {
		t.Run("plan="+plan, func(t *testing.T) {
			posted := false
			run, _ := harness(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" && strings.HasSuffix(r.URL.Path, "/services") {
					io.WriteString(w, `{"services":[{"id":"`+serviceID+`","name":"mysql-db","kind":"mysql"}]}`)
					return
				}
				if r.Method != "POST" || !strings.HasSuffix(r.URL.Path, "/backups/"+deployID+"/restore") {
					t.Fatalf("unexpected request: %s %s", r.Method, r.URL.Path)
				}
				posted = true
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if body["name"] != "recovery" || body["confirm"] != "mysql-db" || body["requestId"] == "" {
					t.Fatalf("invalid restore body: %v", body)
				}
				actual, supplied := body["plan"]
				if (plan == "" && supplied) || (plan != "" && actual != plan) {
					t.Fatalf("unexpected target plan: %v", body)
				}
				io.WriteString(w, `{"serviceId":"`+serviceID+`","checkoutRequired":true}`)
			})
			args := []string{"backups", "restore", deployID, "--service", "mysql-db", "--name", "recovery", "--yes", "--json"}
			if plan != "" {
				args = append(args, "--plan", plan)
			}
			code, _, stderr := run(args...)
			if code != 0 || !posted {
				t.Fatal(code, stderr, posted)
			}
		})
	}
}
