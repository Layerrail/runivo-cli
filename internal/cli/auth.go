package cli

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/Layerrail/runivo-cli/internal/api"
	"github.com/Layerrail/runivo-cli/internal/config"
	"github.com/spf13/cobra"
)

func (a *app) authCommands() []*cobra.Command {
	var noBrowser, readOnly, tokenStdin bool
	login := &cobra.Command{Use: "login", Short: "Authorize a workspace in your browser (30-day revocable access)", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if err := a.load(); err != nil {
			return err
		}
		if a.base == "" {
			a.base = api.DefaultURL
		}
		client, err := api.New(a.base, "", a.version)
		if err != nil {
			return err
		}
		var token string
		if tokenStdin {
			raw, e := readLimited(a.in, 4096)
			if e != nil {
				return e
			}
			token = strings.TrimSpace(string(raw))
		} else {
			secret := make([]byte, 32)
			if _, err = rand.Read(secret); err != nil {
				return err
			}
			verifier := base64.RawURLEncoding.EncodeToString(secret)
			sum := sha256.Sum256([]byte(verifier))
			scope := "write"
			if readOnly {
				scope = "read"
			}
			host, _ := os.Hostname()
			name := "Runivo CLI / " + clean(host)
			if len(name) > 80 {
				name = name[:80]
			}
			var device struct {
				DeviceCode string `json:"device_code"`
				UserCode   string `json:"user_code"`
				URL        string `json:"verification_uri_complete"`
				Expires    int    `json:"expires_in"`
				Interval   int    `json:"interval"`
			}
			err = client.Do(c.Context(), "POST", "/api/v1/cli/device", map[string]any{"code_challenge": base64.RawURLEncoding.EncodeToString(sum[:]), "code_challenge_method": "S256", "scope": scope, "device_name": name}, &device, "")
			if err != nil {
				return err
			}
			target, e := url.Parse(device.URL)
			base, _ := url.Parse(client.BaseURL)
			if e != nil || target.Host != base.Host || target.Scheme != base.Scheme || target.Path != "/api/v1/cli/authorize" || target.User != nil {
				return errors.New("Runivo returned an unexpected browser approval URL")
			}
			fmt.Fprintf(a.errout, "Open %s\nConfirm code: %s\n", device.URL, clean(device.UserCode))
			if !noBrowser {
				if e = openBrowser(device.URL); e != nil {
					fmt.Fprintln(a.errout, "Open the URL above manually to continue.")
				}
			}
			if device.Expires < 1 || device.Expires > 600 {
				device.Expires = 600
			}
			interval := time.Duration(device.Interval) * time.Second
			if interval < 5*time.Second {
				interval = 5 * time.Second
			}
			ctx, cancel := context.WithTimeout(c.Context(), time.Duration(device.Expires)*time.Second)
			defer cancel()
			for {
				if err = pause(ctx, interval); err != nil {
					return errors.New("login expired or was cancelled; run runivo login again")
				}
				var response struct {
					Token string `json:"access_token"`
				}
				err = client.Do(ctx, "POST", "/api/v1/cli/token", map[string]any{"device_code": device.DeviceCode, "code_verifier": verifier}, &response, "")
				if err == nil {
					token = response.Token
					break
				}
				var ae *api.Error
				if errors.As(err, &ae) {
					if ae.Code == "authorization_pending" {
						continue
					}
					if ae.Code == "slow_down" {
						interval += 5 * time.Second
						continue
					}
				}
				return err
			}
		}
		if !strings.HasPrefix(token, "rnv_") || strings.ContainsAny(token, "\r\n\t ") {
			return errors.New("invalid Runivo API key")
		}
		client.Token = token
		stored := false
		defer func() {
			if !stored && !tokenStdin {
				cleanup, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_ = client.Do(cleanup, "DELETE", "/api/v1/cli/session", nil, nil, "")
			}
		}()
		var current struct {
			Workspace struct{ ID, Name, Role string }
			Scope     string
			KeyID     string `json:"keyId"`
		}
		if err = client.Do(c.Context(), "GET", "/api/v1/cli/session", nil, &current, ""); err != nil {
			return err
		}
		p := config.Profile{APIURL: client.BaseURL, Workspace: current.Workspace.ID, Name: current.Workspace.Name, Scope: current.Scope, KeyID: current.KeyID}
		name := a.profile
		if name == "" {
			name = p.Workspace
		}
		// Refuse an overwrite so an earlier key is never silently lost.
		if _, ok := a.settings.Profiles[name]; ok {
			if err = a.confirm("Replace saved profile " + name + "? Existing keys remain revocable in Account & security."); err != nil {
				return err
			}
		}
		if a.tokenFile != "" {
			err = config.AtomicWrite(a.tokenFile, []byte(token+"\n"), 0600, true)
		} else {
			err = config.SetToken(p, token)
		}
		if err != nil {
			if !tokenStdin {
				_ = client.Do(c.Context(), "DELETE", "/api/v1/cli/session", nil, nil, "")
			}
			return err
		}
		a.settings.Profiles[name] = p
		a.settings.Active = name
		if err = a.store.Save(a.settings); err != nil {
			return err
		}
		stored = true
		return a.print(map[string]any{"authenticated": true, "profile": name, "workspace": current.Workspace, "scope": p.Scope, "credentialStorage": map[bool]string{true: "explicit token file", false: "OS keychain"}[a.tokenFile != ""]})
	}}
	login.Flags().BoolVar(&noBrowser, "no-browser", false, "Print the approval URL without opening a browser")
	login.Flags().BoolVar(&readOnly, "read-only", false, "Request read-only workspace access")
	login.Flags().BoolVar(&tokenStdin, "token-stdin", false, "Import an existing API key from stdin instead of browser login")
	logout := &cobra.Command{Use: "logout", Short: "Revoke the active API key and remove its local credential", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if err := a.connect(c.Context()); err != nil {
			return err
		}
		if err := a.confirm("Revoke this CLI API key?"); err != nil {
			return err
		}
		var identity struct {
			KeyID string `json:"keyId"`
		}
		if err := a.client.Do(c.Context(), "GET", "/api/v1/cli/session", nil, &identity, ""); err != nil {
			return err
		}
		if err := a.client.Do(c.Context(), "DELETE", "/api/v1/cli/session", nil, nil, ""); err != nil {
			return err
		}
		name := a.profile
		if name == "" {
			name = a.settings.Active
		}
		p, ok := a.settings.Profiles[name]
		if ok && p.Workspace == a.workspace && p.APIURL == a.client.BaseURL && p.KeyID == identity.KeyID {
			_ = config.DeleteToken(p)
			delete(a.settings.Profiles, name)
			if a.settings.Active == name {
				a.settings.Active = ""
			}
		}
		if a.tokenFile != "" {
			if err := os.Remove(a.tokenFile); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
		if err := a.store.Save(a.settings); err != nil {
			return err
		}
		return a.print(map[string]any{"revoked": true})
	}}
	who := &cobra.Command{Use: "whoami", Short: "Show the authenticated user, workspace, scope and expiry", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if err := a.connect(c.Context()); err != nil {
			return err
		}
		return a.call(c, "GET", "/api/v1/cli/session", nil)
	}}
	workspaces := &cobra.Command{Use: "workspaces", Aliases: []string{"context"}, Short: "Manage locally authorized workspace profiles"}
	workspaces.AddCommand(&cobra.Command{Use: "list", Short: "List saved profiles; authorize additional workspaces with login", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if err := a.load(); err != nil {
			return err
		}
		return a.print(a.settings)
	}})
	workspaces.AddCommand(&cobra.Command{Use: "use PROFILE", Short: "Select a saved workspace profile", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if err := a.load(); err != nil {
			return err
		}
		if _, ok := a.settings.Profiles[args[0]]; !ok {
			return errors.New("unknown profile; use runivo workspaces list")
		}
		a.settings.Active = args[0]
		if err := a.store.Save(a.settings); err != nil {
			return err
		}
		return a.print(map[string]any{"active": args[0]})
	}})
	return []*cobra.Command{login, logout, who, workspaces}
}
