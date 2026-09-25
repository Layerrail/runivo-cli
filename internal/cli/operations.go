package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Layerrail/runivo-cli/internal/api"
	"github.com/spf13/cobra"
)

func (a *app) operationCommands() []*cobra.Command {
	var commit string
	var wait, clear bool
	var timeout time.Duration
	deploy := &cobra.Command{Use: "deploy", Short: "Deploy the connected Git repository or configured image", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		base, _, err := a.servicePath(c)
		if err != nil {
			return err
		}
		body := map[string]any{"requestId": requestID()}
		target := base + "/deploys"
		if commit != "" {
			body["commitSha"] = commit
		}
		if clear {
			if commit != "" {
				return errors.New("--commit cannot be combined with --clear-cache")
			}
			target = base + "/actions"
			body["action"] = "clear-cache"
		}
		var result map[string]any
		if err = a.client.Do(c.Context(), "POST", a.path(target), body, &result, str(body["requestId"])); err != nil {
			return err
		}
		if !wait {
			return a.print(result)
		}
		item, _ := result["deployment"].(map[string]any)
		id := str(item["id"])
		if id == "" {
			return errors.New("deployment response did not contain an ID")
		}
		return a.waitDeployment(c, base, id, timeout)
	}}
	deploy.Flags().StringVar(&commit, "commit", "", "Deploy a specific commit SHA")
	deploy.Flags().BoolVar(&wait, "wait", false, "Wait until live, failed, cancelled or superseded")
	deploy.Flags().BoolVar(&clear, "clear-cache", false, "Clear the build cache and deploy again")
	deploy.Flags().DurationVar(&timeout, "timeout", 20*time.Minute, "Maximum time to wait; timeout does not cancel deployment")
	deployments := &cobra.Command{Use: "deploys", Short: "Deployment history, inspection, cancellation and rollback"}
	deployments.AddCommand(a.serviceRead("list", "deploys"), a.recordRead("show ID", "deploys", "deployment"))
	var waitTimeout time.Duration
	wc := &cobra.Command{Use: "wait ID", Short: "Wait for a deployment's actual result", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		base, _, e := a.servicePath(c)
		if e != nil {
			return e
		}
		if !validID(args[0]) {
			return errors.New("invalid deployment ID")
		}
		return a.waitDeployment(c, base, args[0], waitTimeout)
	}}
	wc.Flags().DurationVar(&waitTimeout, "timeout", 20*time.Minute, "Maximum wait")
	deployments.AddCommand(wc)
	for _, action := range []string{"cancel", "rollback"} {
		action := action
		command := &cobra.Command{Use: action + " ID", Short: action + " a deployment", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
			base, s, e := a.servicePath(c)
			if e != nil {
				return e
			}
			if !validID(args[0]) {
				return errors.New("invalid deployment ID")
			}
			if e = a.confirm(action + " deployment " + args[0] + " on " + str(s["name"]) + "?"); e != nil {
				return e
			}
			return a.call(c, "POST", a.path(base+"/deploys/"+args[0]+"/"+action), map[string]any{"confirm": s["name"], "requestId": requestID()})
		}}
		deployments.AddCommand(command)
	}
	result := []*cobra.Command{deploy, deployments, a.logsCommand(), a.statusCommand(), a.jobsCommand(), a.backupsCommand()}
	for _, action := range []string{"restart", "suspend", "resume"} {
		action := action
		result = append(result, &cobra.Command{Use: action, Short: action + " the selected service", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
			base, s, e := a.servicePath(c)
			if e != nil {
				return e
			}
			if e = a.confirm(action + " " + str(s["name"]) + "?"); e != nil {
				return e
			}
			return a.call(c, "POST", a.path(base+"/actions"), map[string]any{"action": action, "requestId": requestID()})
		}})
	}
	for _, name := range []string{"metrics", "connections", "scaling", "routing", "previews"} {
		result = append(result, a.serviceRead(name, name))
	}
	open := &cobra.Command{Use: "open", Short: "Open the service's public URL", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		_, s, e := a.servicePath(c)
		if e != nil {
			return e
		}
		u := str(s["url"])
		if u == "" {
			return errors.New("this service has no public URL")
		}
		if e = openBrowser(u); e != nil {
			return e
		}
		return a.print(map[string]any{"url": u})
	}}
	result = append(result, open)
	return result
}
func (a *app) serviceRead(name, suffix string) *cobra.Command {
	var hours int
	c := &cobra.Command{Use: name, Short: "Read " + suffix + " for the selected service", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		base, _, e := a.servicePath(c)
		if e != nil {
			return e
		}
		target := a.path(base + "/" + suffix)
		if hours > 0 {
			target += "?hours=" + strconv.Itoa(hours)
		}
		return a.call(c, "GET", target, nil)
	}}
	if suffix == "metrics" {
		c.Flags().IntVar(&hours, "hours", 12, "Metrics time window (1–168 hours)")
	}
	return c
}
func (a *app) recordRead(use, collection, key string) *cobra.Command {
	return &cobra.Command{Use: use, Short: "Read one " + key, Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		base, _, e := a.servicePath(c)
		if e != nil {
			return e
		}
		if !validID(args[0]) {
			return errors.New("invalid resource ID")
		}
		return a.call(c, "GET", a.path(base+"/"+collection+"/"+args[0]), nil)
	}}
}
func (a *app) waitDeployment(c *cobra.Command, base, id string, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(c.Context(), timeout)
	defer cancel()
	previous := ""
	for {
		var result map[string]any
		if err := a.client.Do(ctx, "GET", a.path(base+"/deploys/"+id), nil, &result, ""); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return &exitError{8, "wait timed out; deployment continues on Runivo"}
			}
			return err
		}
		d, _ := result["deployment"].(map[string]any)
		status := str(d["status"])
		encoded, _ := json.Marshal(d)
		if string(encoded) != previous {
			if a.json {
				_ = a.print(result)
			} else {
				if previous == "" || status != "" {
					fmt.Fprintf(a.errout, "%s  %s\n", id, clean(status))
				}
			}
			previous = string(encoded)
		}
		switch status {
		case "live":
			if !a.json {
				return a.print(result)
			}
			return nil
		case "failed", "cancelled", "superseded":
			detail := str(d["error"])
			if detail == "" {
				detail = str(d["blockedReason"])
			}
			return &exitError{9, "deployment " + status + ": " + detail}
		}
		if err := pause(ctx, time.Second); err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return &exitError{8, "wait timed out; deployment continues on Runivo"}
			}
			return err
		}
	}
}
func (a *app) logsCommand() *cobra.Command {
	var follow bool
	var source, search, deployment string
	var hours int
	var cursor int64
	c := &cobra.Command{Use: "logs", Short: "Read or follow build, runtime and system logs without duplicate lines", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		base, _, err := a.servicePath(c)
		if err != nil {
			return err
		}
		for {
			q := url.Values{"after": {strconv.FormatInt(cursor, 10)}, "hours": {strconv.Itoa(hours)}}
			if source != "" {
				q.Set("source", source)
			}
			if search != "" {
				q.Set("search", search)
			}
			if deployment != "" {
				q.Set("deployment", deployment)
			}
			var response struct {
				Items   []map[string]any
				Cursor  int64
				HasMore bool
			}
			if err = a.client.Do(c.Context(), "GET", a.path(base+"/logs")+"?"+q.Encode(), nil, &response, ""); err != nil {
				return err
			}
			for _, line := range response.Items {
				if a.json {
					_ = a.print(line)
				} else {
					fmt.Fprintf(a.out, "%s [%s] %s\n", clean(str(line["timestamp"])), clean(str(line["source"])), clean(str(line["message"])))
				}
			}
			if response.Cursor > cursor {
				cursor = response.Cursor
			} else if response.HasMore {
				return errors.New("log cursor did not advance")
			}
			if response.HasMore {
				continue
			}
			if !follow {
				return nil
			}
			if err = pause(c.Context(), time.Second); err != nil {
				return err
			}
		}
	}}
	c.Flags().BoolVarP(&follow, "follow", "f", false, "Keep streaming new log entries")
	c.Flags().StringVar(&source, "source", "", "Filter build, runtime or system logs")
	c.Flags().StringVar(&search, "search", "", "Search log text")
	c.Flags().StringVar(&deployment, "deployment", "", "Deployment ID")
	c.Flags().IntVar(&hours, "hours", 1, "Log time window; workspace retention applies")
	c.Flags().Int64Var(&cursor, "after", 0, "Resume after a log cursor")
	return c
}
func (a *app) statusCommand() *cobra.Command {
	var watch bool
	c := &cobra.Command{Use: "status", Short: "Current services; --watch receives workspace events in real time", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if err := a.connect(c.Context()); err != nil {
			return err
		}
		if !watch {
			if a.service != "" {
				_, s, e := a.servicePath(c)
				if e != nil {
					return e
				}
				return a.print(s)
			}
			return a.call(c, "GET", a.path("services"), nil)
		}
		selected := ""
		if a.service != "" {
			_, s, e := a.servicePath(c)
			if e != nil {
				return e
			}
			selected = str(s["id"])
		}
		last := ""
		backoff := time.Second
		for {
			response, e := a.client.Request(c.Context(), "GET", a.path("live"), nil, "")
			if e != nil {
				var ae *api.Error
				if errors.As(e, &ae) && ae.Status < 500 {
					return e
				}
				if c.Context().Err() != nil {
					return c.Context().Err()
				}
				if !a.json {
					fmt.Fprintln(a.errout, "Reconnecting to workspace events…")
				}
				if e = pause(c.Context(), backoff); e != nil {
					return e
				}
				if backoff < 15*time.Second {
					backoff *= 2
				}
				continue
			}
			if !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") {
				response.Body.Close()
				return errors.New("expected a Runivo event stream")
			}
			backoff = time.Second
			err := readEvents(response.Body, func(event, data string) error {
				if event == "revoked" {
					return &exitError{4, "workspace access was revoked"}
				}
				if event != "services" || data == last {
					return nil
				}
				last = data
				var payload map[string]any
				if json.Unmarshal([]byte(data), &payload) != nil {
					return errors.New("invalid event data")
				}
				if selected != "" {
					rows, _ := payload["services"].([]any)
					for _, row := range rows {
						m, _ := row.(map[string]any)
						if str(m["id"]) == selected {
							return a.print(m)
						}
					}
					return &exitError{5, "service is no longer available"}
				}
				return a.print(payload)
			})
			response.Body.Close()
			if err != nil {
				var ee *exitError
				if errors.As(err, &ee) {
					return err
				}
				if c.Context().Err() != nil {
					return c.Context().Err()
				}
			}
			if e = pause(c.Context(), 500*time.Millisecond); e != nil {
				return e
			}
		}
	}}
	c.Flags().BoolVar(&watch, "watch", false, "Receive live service changes over SSE")
	return c
}
func readEvents(r interface{ Read([]byte) (int, error) }, consume func(string, string) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), api.MaxResponse)
	event := "message"
	data := []string{}
	size := 0
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if len(data) > 0 {
				if e := consume(event, strings.Join(data, "\n")); e != nil {
					return e
				}
			}
			event = "message"
			data = nil
			size = 0
			continue
		}
		if strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
		}
		if strings.HasPrefix(line, "data:") {
			value := strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " ")
			size += len(value)
			if size > api.MaxResponse {
				return errors.New("event exceeds size limit")
			}
			data = append(data, value)
		}
	}
	return scanner.Err()
}
func (a *app) jobsCommand() *cobra.Command {
	parent := &cobra.Command{Use: "jobs", Short: "Run and inspect one-off jobs or cron executions"}
	parent.AddCommand(a.serviceRead("list", "runs"), a.recordRead("show ID", "runs", "run"))
	var command string
	var wait bool
	var timeout time.Duration
	run := &cobra.Command{Use: "run", Short: "Queue a command on the selected service", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		base, s, e := a.servicePath(c)
		if e != nil {
			return e
		}
		if e = a.confirm("Run a job on " + str(s["name"]) + "?"); e != nil {
			return e
		}
		body := map[string]any{"requestId": requestID()}
		if command != "" {
			body["command"] = command
		}
		var response map[string]any
		if e = a.client.Do(c.Context(), "POST", a.path(base+"/runs"), body, &response, ""); e != nil {
			return e
		}
		if !wait {
			return a.print(response)
		}
		r, _ := response["run"].(map[string]any)
		id := str(r["id"])
		if !validID(id) {
			return errors.New("job response has no ID")
		}
		ctx, cancel := context.WithTimeout(c.Context(), timeout)
		defer cancel()
		for {
			var result map[string]any
			if e = a.client.Do(ctx, "GET", a.path(base+"/runs/"+id), nil, &result, ""); e != nil {
				return e
			}
			r, _ = result["run"].(map[string]any)
			switch str(r["status"]) {
			case "complete", "succeeded", "failed", "cancelled":
				_ = a.print(result)
				if str(r["status"]) == "failed" || str(r["status"]) == "cancelled" || r["exitCode"] != nil && str(r["exitCode"]) != "0" {
					return &exitError{9, "job did not succeed"}
				}
				return nil
			}
			if e = pause(ctx, time.Second); e != nil {
				return e
			}
		}
	}}
	run.Flags().StringVar(&command, "command", "", "Command; omit to use the configured cron command")
	run.Flags().BoolVar(&wait, "wait", false, "Wait for the result and propagate failure")
	run.Flags().DurationVar(&timeout, "timeout", 20*time.Minute, "Maximum wait")
	parent.AddCommand(run)
	parent.AddCommand(&cobra.Command{Use: "cancel ID", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		base, _, e := a.servicePath(c)
		if e != nil {
			return e
		}
		if !validID(args[0]) {
			return errors.New("invalid job ID")
		}
		if e = a.confirm("Cancel job " + args[0] + "?"); e != nil {
			return e
		}
		return a.call(c, "POST", a.path(base+"/runs/"+args[0]+"/cancel"), map[string]any{})
	}})
	return parent
}
