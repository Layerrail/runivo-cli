package cli

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Layerrail/runivo-cli/internal/api"
	"github.com/Layerrail/runivo-cli/internal/config"
	"github.com/spf13/cobra"
)

type app struct {
	in                                                    io.Reader
	out, errout                                           io.Writer
	json, yes                                             bool
	base, workspace, service, profile, tokenFile, version string
	store                                                 config.Store
	settings                                              config.Config
	client                                                *api.Client
}
type exitError struct {
	code    int
	message string
}

func (e *exitError) Error() string { return e.message }
func Execute(ctx context.Context, args []string, in io.Reader, out, errout io.Writer, version, commit string) int {
	a := &app{in: in, out: out, errout: errout, version: version}
	root := a.root(version, commit)
	root.SetArgs(args)
	if err := root.ExecuteContext(ctx); err != nil {
		code := 1
		var ae *api.Error
		var ee *exitError
		if errors.As(err, &ae) {
			switch ae.Status {
			case 401:
				code = 3
			case 403:
				code = 4
			case 404:
				code = 5
			case 409:
				code = 6
			case 429:
				code = 7
			}
		}
		if errors.As(err, &ee) {
			code = ee.code
		}
		if errors.Is(err, context.Canceled) {
			code = 130
		}
		if a.json {
			_ = json.NewEncoder(errout).Encode(map[string]any{"error": clean(err.Error()), "exitCode": code})
		} else {
			fmt.Fprintln(errout, "Error:", clean(err.Error()))
		}
		return code
	}
	return 0
}
func (a *app) root(version, commit string) *cobra.Command {
	root := &cobra.Command{Use: "runivo", Short: "Build and operate your services on Runivo", Version: version + " (" + commit + ")", SilenceUsage: true, SilenceErrors: true}
	root.SetIn(a.in)
	root.SetOut(a.out)
	root.SetErr(a.errout)
	f := root.PersistentFlags()
	f.BoolVar(&a.json, "json", false, "Machine-readable JSON (streams use NDJSON)")
	f.BoolVarP(&a.yes, "yes", "y", false, "Confirm the requested operation without prompting")
	f.StringVar(&a.base, "api-url", os.Getenv("RUNIVO_API_URL"), "Runivo API origin; HTTPS required")
	f.StringVarP(&a.workspace, "workspace", "w", os.Getenv("RUNIVO_WORKSPACE"), "Workspace ID (must match the API key)")
	f.StringVarP(&a.service, "service", "s", os.Getenv("RUNIVO_SERVICE"), "Service ID or exact name")
	f.StringVar(&a.profile, "profile", os.Getenv("RUNIVO_PROFILE"), "Saved workspace profile")
	f.StringVar(&a.tokenFile, "token-file", os.Getenv("RUNIVO_TOKEN_FILE"), "Explicit credential file for headless environments")
	root.AddCommand(a.authCommands()...)
	root.AddCommand(a.resourceCommands()...)
	root.AddCommand(a.operationCommands()...)
	root.AddCommand(a.secretCommands("env", "variables"), a.secretCommands("secret-files", "secret-files"), a.shellCommand(), a.rawCommand(), a.linkCommand(), a.billingCommand(), a.githubCommand())
	root.AddCommand(&cobra.Command{Use: "catalog", Short: "List service types, plans, runtimes and regions", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if err := a.connect(c.Context()); err != nil {
			return err
		}
		return a.call(c, "GET", "/api/v1/catalog", nil)
	}})
	return root
}
func (a *app) load() error {
	var err error
	a.store, err = config.NewStore()
	if err != nil {
		return err
	}
	a.settings, err = a.store.Load()
	return err
}
func (a *app) connect(ctx context.Context) error {
	if a.client != nil {
		return nil
	}
	if err := a.load(); err != nil {
		return err
	}
	project, err := config.LoadProject()
	if err != nil {
		return err
	}
	if a.workspace == "" {
		a.workspace = project.Workspace
	}
	if a.service == "" {
		a.service = project.Service
	}
	name := a.profile
	if name == "" {
		name = a.settings.Active
	}
	p, exists := a.settings.Profiles[name]
	if a.profile != "" && !exists {
		return fmt.Errorf("profile %q does not exist; run runivo login --profile NAME", name)
	}
	if a.workspace != "" && a.profile == "" && p.Workspace != a.workspace {
		for candidateName, candidate := range a.settings.Profiles {
			if candidate.Workspace == a.workspace && (a.base == "" || a.base == candidate.APIURL) {
				p = candidate
				name = candidateName
				exists = true
				break
			}
		}
	}
	a.profile = name
	if a.workspace == "" {
		a.workspace = p.Workspace
	}
	if a.base == "" {
		a.base = p.APIURL
	}
	if a.base == "" {
		a.base = api.DefaultURL
	}
	token := strings.TrimSpace(os.Getenv("RUNIVO_API_KEY"))
	if a.tokenFile != "" {
		raw, e := readLimitedFile(a.tokenFile, 4096)
		if e != nil {
			return e
		}
		token = strings.TrimSpace(string(raw))
	}
	if token == "" && exists {
		canonical, e := api.ValidateURL(a.base)
		if e != nil {
			return e
		}
		if canonical != p.APIURL {
			return errors.New("refusing to send a saved credential to another API origin; log in to that origin first")
		}
		token, err = config.Token(p)
		if err != nil {
			return err
		}
	}
	if token == "" {
		return &exitError{3, "sign in with runivo login, or set RUNIVO_API_KEY for CI"}
	}
	if !strings.HasPrefix(token, "rnv_") || strings.ContainsAny(token, "\r\n\t ") {
		return errors.New("invalid API key format")
	}
	a.client, err = api.New(a.base, token, a.version)
	if err != nil {
		return err
	}
	if a.workspace == "" {
		var session map[string]any
		if err = a.client.Do(ctx, "GET", "/api/v1/cli/session", nil, &session, ""); err != nil {
			return err
		}
		w, _ := session["workspace"].(map[string]any)
		a.workspace = str(w["id"])
	}
	if !validID(a.workspace) {
		return errors.New("a valid workspace ID is required")
	}
	return nil
}
func validID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i, c := range s {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return false
			}
		} else if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}
func (a *app) path(s string) string {
	return "/api/v1/workspaces/" + a.workspace + "/" + strings.TrimLeft(s, "/")
}
func (a *app) call(c *cobra.Command, method, path string, body any) error {
	var result any
	id := idempotencyKey(method, path, body)
	if err := a.client.Do(c.Context(), method, path, body, &result, id); err != nil {
		return err
	}
	return a.print(result)
}

// Only the API's documented core mutations accept idempotency headers. Other
// operations (including terminals and backup jobs) have their own request IDs.
var receiptPath = regexp.MustCompile(`^/api/v1/workspaces/[0-9a-fA-F-]{36}/(?:projects(?:/[0-9a-fA-F-]{36})?|services(?:/[0-9a-fA-F-]{36}(?:/(?:archive|restore|actions))?)?|services/[0-9a-fA-F-]{36}/(?:variables|secret-files)(?:/[0-9a-fA-F-]{36})?|env-groups/[0-9a-fA-F-]{36}/variables(?:/[0-9a-fA-F-]{36})?|services/[0-9a-fA-F-]{36}/deploys(?:/[0-9a-fA-F-]{36}/(?:cancel|rollback))?)$`)

func idempotencyKey(method, path string, body any) string {
	if method != "POST" && method != "PATCH" && method != "DELETE" {
		return ""
	}
	u, err := url.Parse(path)
	if err != nil || !receiptPath.MatchString(u.Path) {
		return ""
	}
	if values, ok := body.(map[string]any); ok {
		if key, ok := values["requestId"].(string); ok && key != "" {
			return key
		}
	}
	return requestID()
}
func (a *app) print(v any) error {
	enc := json.NewEncoder(a.out)
	enc.SetEscapeHTML(false)
	if a.json {
		return enc.Encode(v)
	}
	if m, ok := v.(map[string]any); ok {
		for _, k := range []string{"services", "projects", "environments", "groups", "integrations", "items", "repositories", "branches", "events", "profiles", "members", "invitations", "variables"} {
			if rows, ok := m[k].([]any); ok {
				return a.table(rows)
			}
		}
	}
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
func (a *app) table(rows []any) error {
	if len(rows) == 0 {
		fmt.Fprintln(a.out, "No results.")
		return nil
	}
	keys := []string{"id", "name", "email", "role", "key", "fullName", "kind", "displayStatus", "status", "createdAt", "updatedAt"}
	present := []string{}
	for _, key := range keys {
		for _, r := range rows {
			if m, ok := r.(map[string]any); ok && m[key] != nil {
				present = append(present, key)
				break
			}
		}
	}
	if len(present) == 0 {
		e := json.NewEncoder(a.out)
		e.SetIndent("", "  ")
		return e.Encode(rows)
	}
	w := tabwriter.NewWriter(a.out, 0, 4, 2, ' ', 0)
	head := []string{}
	for _, k := range present {
		head = append(head, strings.ToUpper(k))
	}
	fmt.Fprintln(w, strings.Join(head, "\t"))
	for _, r := range rows {
		m, _ := r.(map[string]any)
		values := []string{}
		for _, k := range present {
			values = append(values, clean(str(m[k])))
		}
		fmt.Fprintln(w, strings.Join(values, "\t"))
	}
	return w.Flush()
}
func clean(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return -1
		}
		return r
	}, s)
}
func str(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprint(v)
}
func requestID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	s := hex.EncodeToString(b[:])
	return s[:8] + "-" + s[8:12] + "-" + s[12:16] + "-" + s[16:20] + "-" + s[20:]
}
func pause(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func (a *app) confirm(message string) error {
	if a.yes {
		return nil
	}
	if a.json {
		return errors.New("this operation requires --yes in JSON mode")
	}
	fmt.Fprint(a.errout, clean(message)+" Type yes to continue: ")
	line, err := bufio.NewReader(a.in).ReadString('\n')
	if err != nil && err != io.EOF {
		return err
	}
	if strings.TrimSpace(line) != "yes" {
		return &exitError{2, "operation cancelled"}
	}
	return nil
}
func readLimitedFile(path string, limit int64) ([]byte, error) {
	f, e := os.Open(path)
	if e != nil {
		return nil, e
	}
	defer f.Close()
	return readLimited(f, limit)
}
func readLimited(r io.Reader, limit int64) ([]byte, error) {
	b, e := io.ReadAll(io.LimitReader(r, limit+1))
	if e != nil {
		return nil, e
	}
	if int64(len(b)) > limit {
		return nil, errors.New("input exceeds size limit")
	}
	return b, nil
}
func (a *app) payload(file string) (map[string]any, error) {
	if file == "" {
		return map[string]any{}, nil
	}
	var raw []byte
	var err error
	if file == "-" {
		raw, err = readLimited(a.in, 262144)
	} else {
		raw, err = readLimitedFile(file, 262144)
	}
	if err != nil {
		return nil, err
	}
	var body map[string]any
	if json.Unmarshal(raw, &body) != nil || body == nil {
		return nil, errors.New("--file must contain a JSON object")
	}
	return body, nil
}
func openBrowser(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.User != nil || u.Host == "" || (u.Scheme != "https" && !(u.Scheme == "http" && (u.Hostname() == "127.0.0.1" || u.Hostname() == "localhost"))) {
		return errors.New("refusing to open an unsafe URL")
	}
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", raw)
	case "darwin":
		cmd = exec.Command("open", raw)
	default:
		cmd = exec.Command("xdg-open", raw)
	}
	if err = cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}
func (a *app) resolve(c *cobra.Command, collection, name string) (map[string]any, error) {
	var v map[string]any
	if err := a.client.Do(c.Context(), "GET", a.path(collection), nil, &v, ""); err != nil {
		return nil, err
	}
	var match map[string]any
	for _, value := range v {
		rows, ok := value.([]any)
		if !ok {
			continue
		}
		for _, row := range rows {
			m, ok := row.(map[string]any)
			if !ok {
				continue
			}
			if str(m["id"]) == name || str(m["name"]) == name || str(m["key"]) == name || str(m["email"]) == name {
				if match != nil {
					return nil, errors.New("name is ambiguous; use the resource ID")
				}
				match = m
			}
		}
	}
	if match == nil {
		return nil, &exitError{5, fmt.Sprintf("%s %q was not found in this workspace", collection, name)}
	}
	return match, nil
}
func (a *app) servicePath(c *cobra.Command) (string, map[string]any, error) {
	if err := a.connect(c.Context()); err != nil {
		return "", nil, err
	}
	if a.service == "" {
		return "", nil, errors.New("select a service with --service NAME_OR_ID or runivo link")
	}
	s, err := a.resolve(c, "services", a.service)
	if err != nil {
		return "", nil, err
	}
	return "services/" + str(s["id"]), s, nil
}
func (a *app) linkCommand() *cobra.Command {
	return &cobra.Command{Use: "link [SERVICE]", Short: "Save workspace/service context in this directory's runivo.toml", Args: cobra.MaximumNArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if len(args) > 0 {
			a.service = args[0]
		}
		path, s, err := a.servicePath(c)
		if err != nil {
			return err
		}
		_ = path
		if _, err = os.Stat("runivo.toml"); err == nil {
			if err = a.confirm("Replace runivo.toml?"); err != nil {
				return err
			}
		}
		data := fmt.Sprintf("workspace = %q\nservice = %q\n", a.workspace, str(s["id"]))
		if err = config.AtomicWrite("runivo.toml", []byte(data), 0600, false); err != nil {
			return err
		}
		absolute, _ := filepath.Abs("runivo.toml")
		return a.print(map[string]any{"linked": str(s["name"]), "file": absolute})
	}}
}
