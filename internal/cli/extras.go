package cli

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

func (a *app) billingCommand() *cobra.Command {
	parent := &cobra.Command{Use: "billing", Short: "Usage, invoices and browser-authorized checkout; CLI never charges a card"}
	parent.AddCommand(&cobra.Command{Use: "show", Short: "Current plan, unbilled usage, credits and invoices", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if e := a.connect(c.Context()); e != nil {
			return e
		}
		return a.call(c, "GET", a.path("billing"), nil)
	}})
	parent.AddCommand(&cobra.Command{Use: "open", Short: "Open the active workspace billing page to authorize purchases", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if e := a.connect(c.Context()); e != nil {
			return e
		}
		target := a.client.BaseURL + "/email/open?" + url.Values{"workspace": {a.workspace}, "path": {"/dashboard/billing"}}.Encode()
		if e := openBrowser(target); e != nil {
			return e
		}
		return a.print(map[string]any{"url": target})
	}})
	var out, format string
	invoice := &cobra.Command{Use: "invoice ID", Short: "Inspect an invoice or download its PDF/CSV", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if e := a.connect(c.Context()); e != nil {
			return e
		}
		if !validID(args[0]) {
			return errors.New("invalid invoice ID")
		}
		target := a.path("billing/invoices/" + args[0])
		if out == "" {
			return a.call(c, "GET", target, nil)
		}
		if format != "pdf" && format != "csv" {
			return errors.New("format must be pdf or csv")
		}
		response, e := a.client.Request(c.Context(), "GET", target+"/"+format, nil, "")
		if e != nil {
			return e
		}
		defer response.Body.Close()
		if e = downloadTo(response.Body, out, ""); e != nil {
			return e
		}
		return a.print(map[string]any{"file": out})
	}}
	invoice.Flags().StringVar(&out, "output", "", "Save invoice to a new file")
	invoice.Flags().StringVar(&format, "format", "pdf", "pdf or csv")
	parent.AddCommand(invoice)
	return parent
}
func (a *app) githubCommand() *cobra.Command {
	parent := &cobra.Command{Use: "github", Short: "GitHub connections, installation, repositories and framework detection"}
	for _, op := range []string{"connections", "installations", "repositories", "branches"} {
		op := op
		var connection, repository string
		var page int
		c := &cobra.Command{Use: op, Short: "List GitHub " + op, Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
			if e := a.connect(c.Context()); e != nil {
				return e
			}
			target := "github"
			if op != "connections" {
				target += "/" + op
			}
			q := url.Values{}
			if connection != "" {
				q.Set("connection", connection)
			}
			if repository != "" {
				q.Set("repository", repository)
			}
			q.Set("page", fmt.Sprint(page))
			return a.call(c, "GET", a.path(target)+"?"+q.Encode(), nil)
		}}
		c.Flags().StringVar(&connection, "connection", "", "GitHub connection ID")
		c.Flags().StringVar(&repository, "repo", "", "Repository full name")
		c.Flags().IntVar(&page, "page", 1, "Result page; response includes nextPage")
		parent.AddCommand(c)
	}
	var repository, branch, root, connection string
	detect := &cobra.Command{Use: "detect", Short: "Detect a connected repository's runtime and build settings", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if e := a.connect(c.Context()); e != nil {
			return e
		}
		return a.call(c, "POST", a.path("github/detect"), map[string]any{"repository": repository, "branch": branch, "rootDirectory": root, "connectionId": connection})
	}}
	detect.Flags().StringVar(&repository, "repo", "", "Repository full name")
	_ = detect.MarkFlagRequired("repo")
	detect.Flags().StringVar(&branch, "branch", "main", "Branch")
	detect.Flags().StringVar(&root, "root", "", "Root directory")
	detect.Flags().StringVar(&connection, "connection", "", "Connection ID")
	parent.AddCommand(detect)
	for _, action := range []string{"connect", "install"} {
		action := action
		parent.AddCommand(&cobra.Command{Use: action, Short: "Open GitHub to " + action + " the Runivo integration", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
			if e := a.connect(c.Context()); e != nil {
				return e
			}
			var response map[string]any
			if e := a.client.Do(c.Context(), "POST", a.path("github/"+action), map[string]any{}, &response, ""); e != nil {
				return e
			}
			target := str(response["url"])
			u, e := url.Parse(target)
			if e != nil || u.Host != "github.com" || u.Scheme != "https" {
				return errors.New("unexpected GitHub authorization URL")
			}
			if e = openBrowser(target); e != nil {
				return e
			}
			return a.print(response)
		}})
	}
	return parent
}
func (a *app) backupsCommand() *cobra.Command {
	parent := &cobra.Command{Use: "backups", Short: "Managed database backups, exports and isolated restores"}
	parent.AddCommand(a.serviceRead("list", "backups"))
	parent.AddCommand(&cobra.Command{Use: "create", Short: "Queue a database backup", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		base, _, e := a.servicePath(c)
		if e != nil {
			return e
		}
		return a.call(c, "POST", a.path(base+"/backups"), map[string]any{"requestId": requestID()})
	}})
	for _, action := range []string{"delete", "restore", "download"} {
		action := action
		var name, out string
		var timeout time.Duration
		c := &cobra.Command{Use: action + " ID", Short: action + " a database backup", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
			base, s, e := a.servicePath(c)
			if e != nil {
				return e
			}
			if !validID(args[0]) {
				return errors.New("invalid backup ID")
			}
			target := a.path(base + "/backups/" + args[0])
			body := map[string]any{"confirm": s["name"], "name": name, "requestId": requestID()}
			if action == "delete" || action == "restore" {
				message := action + " backup " + args[0] + "?"
				if action == "restore" {
					if name == "" {
						return errors.New("--name is required for the new database service")
					}
					message = "Restore into a new database service? Its plan and storage charges apply."
				}
				if e = a.confirm(message); e != nil {
					return e
				}
				method := "POST"
				if action == "delete" {
					method = "DELETE"
				} else {
					target += "/restore"
				}
				return a.call(c, method, target, body)
			}
			if out == "" {
				return errors.New("--output is required")
			}
			backup, e := a.resolve(c, base+"/backups", args[0])
			if e != nil {
				return e
			}
			var queued struct {
				RequestID string `json:"requestId"`
			}
			if e = a.client.Do(c.Context(), "POST", target+"/download", body, &queued, ""); e != nil {
				return e
			}
			if !validID(queued.RequestID) {
				return errors.New("backup export returned no request ID")
			}
			ctx, cancel := context.WithTimeout(c.Context(), timeout)
			defer cancel()
			for {
				var result struct{ State, Error, URL string }
				if e = a.client.Do(ctx, "GET", target+"/download/"+queued.RequestID, nil, &result, ""); e != nil {
					return e
				}
				if result.State == "failed" {
					return errors.New("backup export failed: " + result.Error)
				}
				if result.State == "complete" {
					u, e := url.Parse(result.URL)
					if e != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
						return errors.New("backup URL expired or invalid; request a new download")
					}
					request, e := http.NewRequestWithContext(ctx, "GET", result.URL, nil)
					if e != nil {
						return errors.New("invalid download URL")
					}
					// Signed storage URLs use a separate client with no Runivo credentials and no redirects.
					client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
					response, e := client.Do(request)
					if e != nil {
						return errors.New("backup download failed; request a new download")
					}
					defer response.Body.Close()
					if response.StatusCode != 200 {
						return errors.New("backup storage refused the download")
					}
					if e = downloadTo(response.Body, out, str(backup["checksum"])); e != nil {
						return e
					}
					return a.print(map[string]any{"file": out, "checksum": backup["checksum"]})
				}
				if e = pause(ctx, time.Second); e != nil {
					return e
				}
			}
		}}
		c.Flags().StringVar(&name, "name", "", "Name of the new database service (restore only)")
		c.Flags().StringVar(&out, "output", "", "New output file (download only)")
		c.Flags().DurationVar(&timeout, "timeout", 30*time.Minute, "Export/download timeout")
		parent.AddCommand(c)
	}
	return parent
}
func downloadTo(r io.Reader, path, checksum string) error {
	if _, e := os.Lstat(path); e == nil {
		return errors.New("output file already exists")
	}
	file, e := os.CreateTemp(filepath.Dir(path), ".runivo-download-*")
	if e != nil {
		return e
	}
	name := file.Name()
	defer os.Remove(name)
	if e = file.Chmod(0600); e != nil {
		file.Close()
		return e
	}
	hash := sha256.New()
	_, e = io.Copy(io.MultiWriter(file, hash), r)
	closeError := file.Close()
	if e != nil {
		return e
	}
	if closeError != nil {
		return closeError
	}
	actual := hex.EncodeToString(hash.Sum(nil))
	checksum = strings.TrimPrefix(checksum, "sha256:")
	if checksum != "" && !strings.EqualFold(checksum, actual) {
		return errors.New("backup checksum mismatch; incomplete file removed")
	}
	// Hard-link publishes atomically and fails if another process created the target.
	if e = os.Link(name, path); e != nil {
		return e
	}
	return nil
}
