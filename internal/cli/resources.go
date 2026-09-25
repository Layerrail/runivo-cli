package cli

import (
	"errors"
	"net/url"
	"strings"

	"github.com/spf13/cobra"
)

type resource struct {
	name, path, help               string
	scoped, create, update, remove bool
}

func (a *app) resourceCommands() []*cobra.Command {
	specs := []resource{
		{"services", "services", "Application services and databases", false, true, true, true},
		{"projects", "projects", "Projects and their environments", false, true, true, true},
		{"environments", "environments", "Project environments", false, true, true, true},
		{"groups", "env-groups", "Shared environment groups", false, true, true, true},
		{"integrations", "integrations", "Webhooks, log streams, private links, dedicated IPs and registries", false, true, true, true},
		{"blueprints", "blueprints", "Reusable deployment blueprints", false, true, true, true},
		{"members", "members", "Workspace members and roles", false, false, true, true},
		{"invitations", "invitations", "Workspace invitations", false, true, false, true},
		{"domains", "domains", "Service custom domains and HTTPS verification", true, true, false, true},
		{"disks", "disks", "Persistent disks", true, true, true, true},
		{"redirects", "redirects", "Static site redirects", true, true, true, true},
		{"headers", "headers", "Static site response headers", true, true, true, true},
		{"schedules", "jobs", "Saved service job definitions", true, true, true, true},
	}
	result := []*cobra.Command{}
	for _, spec := range specs {
		result = append(result, a.resourceCommand(spec))
	}
	for _, item := range []struct{ name, path, help string }{{"usage", "usage", "Workspace resource consumption"}, {"audit", "events", "Workspace audit events"}, {"notifications", "notifications", "Operational email deliveries"}, {"registries", "registries", "Available private image registries"}} {
		path := item.path
		c := &cobra.Command{Use: item.name, Short: item.help, Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
			if err := a.connect(c.Context()); err != nil {
				return err
			}
			return a.call(c, "GET", a.path(path), nil)
		}}
		result = append(result, c)
	}
	workspace := &cobra.Command{Use: "workspace", Short: "Read or update the active workspace"}
	workspace.AddCommand(&cobra.Command{Use: "show", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if err := a.connect(c.Context()); err != nil {
			return err
		}
		return a.call(c, "GET", strings.TrimSuffix(a.path(""), "/"), nil)
	}})
	var file string
	update := &cobra.Command{Use: "update --file JSON", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if err := a.connect(c.Context()); err != nil {
			return err
		}
		b, e := a.payload(file)
		if e != nil {
			return e
		}
		return a.call(c, "PATCH", strings.TrimSuffix(a.path(""), "/"), b)
	}}
	update.Flags().StringVar(&file, "file", "", "Workspace settings JSON file, or - for stdin")
	_ = update.MarkFlagRequired("file")
	workspace.AddCommand(update)
	result = append(result, workspace)
	return result
}
func (a *app) resourceBase(c *cobra.Command, s resource) (string, error) {
	if err := a.connect(c.Context()); err != nil {
		return "", err
	}
	if !s.scoped {
		return s.path, nil
	}
	base, _, err := a.servicePath(c)
	return base + "/" + s.path, err
}
func (a *app) resourceCommand(s resource) *cobra.Command {
	parent := &cobra.Command{Use: s.name, Short: s.help}
	parent.AddCommand(&cobra.Command{Use: "list", Short: "List resources", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		base, e := a.resourceBase(c, s)
		if e != nil {
			return e
		}
		return a.call(c, "GET", a.path(base), nil)
	}})
	parent.AddCommand(&cobra.Command{Use: "show NAME_OR_ID", Short: "Show one resource", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		base, e := a.resourceBase(c, s)
		if e != nil {
			return e
		}
		value, e := a.resolve(c, base, args[0])
		if e != nil {
			return e
		}
		return a.print(value)
	}})
	for _, action := range []string{"create", "update", "delete"} {
		if action == "create" && !s.create || action == "update" && !s.update || action == "delete" && !s.remove {
			continue
		}
		var file, name, kind, repo, branch, project, environment, plan string
		var deploy bool
		use := action
		args := cobra.NoArgs
		if action != "create" {
			use += " NAME_OR_ID"
			args = cobra.ExactArgs(1)
		}
		command := &cobra.Command{Use: use, Short: strings.ToUpper(action[:1]) + action[1:] + " a resource; server permissions and plan limits apply", Args: args, RunE: func(c *cobra.Command, args []string) error {
			base, err := a.resourceBase(c, s)
			if err != nil {
				return err
			}
			body, err := a.payload(file)
			if err != nil {
				return err
			}
			method := "POST"
			if name != "" {
				body["name"] = name
			}
			if kind != "" {
				body["kind"] = kind
			}
			if project != "" {
				body["projectId"] = project
			}
			if environment != "" {
				body["environmentId"] = environment
			}
			cfg, _ := body["configuration"].(map[string]any)
			if cfg == nil {
				cfg = map[string]any{}
			}
			for k, v := range map[string]string{"repository": repo, "branch": branch, "plan": plan} {
				if v != "" {
					cfg[k] = v
				}
			}
			if len(cfg) > 0 {
				body["configuration"] = cfg
			}
			if deploy {
				body["deploy"] = true
				body["requestId"] = requestID()
			}
			if action != "create" {
				resource, err := a.resolve(c, base, args[0])
				if err != nil {
					return err
				}
				base += "/" + url.PathEscape(str(resource["id"]))
				if action == "delete" {
					if err = a.confirm("Delete " + str(resource["name"]) + "?"); err != nil {
						return err
					}
					method = "DELETE"
					body["confirm"] = resource["name"]
					if s.name == "members" {
						body["confirm"] = resource["email"]
					}
				} else {
					method = "PATCH"
				}
			}
			if action != "delete" && len(body) == 0 {
				return errors.New("provide --file JSON or configuration flags")
			}
			return a.call(c, method, a.path(base), body)
		}}
		command.Flags().StringVar(&file, "file", "", "Complete API JSON payload file, or - for stdin")
		if action != "delete" {
			command.Flags().StringVar(&name, "name", "", "Resource name")
		}
		if s.name == "services" && action != "delete" {
			command.Flags().StringVar(&kind, "type", "", "Service kind from runivo catalog (web, static, private, worker, cron, postgres, redis)")
			command.Flags().StringVar(&repo, "repo", "", "GitHub repository")
			command.Flags().StringVar(&branch, "branch", "", "Git branch")
			command.Flags().StringVar(&plan, "plan", "", "Service plan ID")
			command.Flags().StringVar(&project, "project", "", "Project ID")
			command.Flags().StringVar(&environment, "environment", "", "Environment ID")
			if action == "create" {
				command.Flags().BoolVar(&deploy, "deploy", false, "Enqueue the first deployment after creation")
			}
		}
		parent.AddCommand(command)
	}
	if s.name == "domains" {
		parent.AddCommand(a.resourceAction(s, "verify", "verify"))
	}
	if s.name == "integrations" {
		for _, action := range []string{"test", "refresh", "retry", "deliveries"} {
			parent.AddCommand(a.resourceAction(s, action, action))
		}
	}
	if s.name == "blueprints" {
		parent.AddCommand(a.resourceAction(s, "apply", "apply"))
		var file string
		validate := &cobra.Command{Use: "validate --file YAML", Short: "Validate a blueprint without provisioning services", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
			if err := a.connect(c.Context()); err != nil {
				return err
			}
			raw, e := readLimitedFile(file, 200000)
			if e != nil {
				return e
			}
			return a.call(c, "POST", a.path("blueprints/validate"), map[string]any{"manifest": string(raw)})
		}}
		validate.Flags().StringVar(&file, "file", "", "Blueprint YAML file")
		_ = validate.MarkFlagRequired("file")
		parent.AddCommand(validate)
	}
	if s.name == "services" {
		for _, action := range []string{"archive", "restore", "export"} {
			parent.AddCommand(a.resourceAction(s, action, action))
		}
	}
	return parent
}
func (a *app) resourceAction(s resource, name, suffix string) *cobra.Command {
	var file string
	c := &cobra.Command{Use: name + " NAME_OR_ID", Short: name + " a " + s.name + " resource", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		base, e := a.resourceBase(c, s)
		if e != nil {
			return e
		}
		resource, e := a.resolve(c, base, args[0])
		if e != nil {
			return e
		}
		body, e := a.payload(file)
		if e != nil {
			return e
		}
		method := "POST"
		if name == "export" || name == "deliveries" {
			method = "GET"
		} else if e = a.confirm(name + " " + str(resource["name"]) + "?"); e != nil {
			return e
		}
		return a.call(c, method, a.path(base+"/"+str(resource["id"])+"/"+suffix), body)
	}}
	c.Flags().StringVar(&file, "file", "", "Additional API JSON payload, or - for stdin")
	return c
}
func (a *app) rawCommand() *cobra.Command {
	var method, file string
	var queries []string
	c := &cobra.Command{Use: "api PATH", Short: "Call an API path relative to the current workspace (never the engine)", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		if err := a.connect(c.Context()); err != nil {
			return err
		}
		path := strings.Trim(args[0], "/")
		if strings.Contains(path, "..") || strings.ContainsAny(path, "?#\\%") || strings.HasPrefix(path, "api/") {
			return errors.New("use a plain relative workspace path, e.g. services/ID/scaling")
		}
		method = strings.ToUpper(method)
		if method != "GET" && method != "POST" && method != "PATCH" && method != "DELETE" {
			return errors.New("supported methods: GET, POST, PATCH, DELETE")
		}
		body, e := a.payload(file)
		if e != nil {
			return e
		}
		q := url.Values{}
		for _, v := range queries {
			k, value, ok := strings.Cut(v, "=")
			if !ok {
				return errors.New("query must be KEY=VALUE")
			}
			q.Add(k, value)
		}
		target := a.path(path)
		if len(q) > 0 {
			target += "?" + q.Encode()
		}
		if method != "GET" {
			if e = a.confirm(method + " " + path + "?"); e != nil {
				return e
			}
		}
		return a.call(c, method, target, body)
	}}
	c.Flags().StringVarP(&method, "method", "X", "GET", "HTTP method")
	c.Flags().StringVar(&file, "file", "", "JSON payload file or - for stdin")
	c.Flags().StringArrayVarP(&queries, "query", "q", nil, "Query KEY=VALUE, repeatable")
	return c
}
