package cli

import (
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func (a *app) secretCommands(name, section string) *cobra.Command {
	var group string
	parent := &cobra.Command{Use: name, Short: "Manage encrypted " + section + "; values are masked unless explicitly revealed"}
	if section == "variables" {
		parent.PersistentFlags().StringVar(&group, "group", "", "Use an environment group ID or name instead of a service")
	}
	baseFor := func(c *cobra.Command) (string, error) {
		if err := a.connect(c.Context()); err != nil {
			return "", err
		}
		if group != "" {
			g, e := a.resolve(c, "env-groups", group)
			if e != nil {
				return "", e
			}
			return "env-groups/" + str(g["id"]) + "/variables", nil
		}
		base, _, e := a.servicePath(c)
		return base + "/" + section, e
	}
	parent.AddCommand(&cobra.Command{Use: "list", Short: "List names and IDs without exposing values", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		base, e := baseFor(c)
		if e != nil {
			return e
		}
		return a.call(c, "GET", a.path(base), nil)
	}})
	var file string
	set := &cobra.Command{Use: "set KEY", Short: "Read a secret from --file or stdin (never from shell arguments)", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		base, e := baseFor(c)
		if e != nil {
			return e
		}
		var raw []byte
		if file == "" || file == "-" {
			raw, e = readLimited(a.in, 65536)
		} else {
			raw, e = readLimitedFile(file, 65536)
		}
		if e != nil {
			return e
		}
		// Preserve secret bytes, including intentional newlines. Use printf for single-line values.
		method := "POST"
		target := base
		existing, e := a.resolve(c, base, args[0])
		if e == nil {
			method = "PATCH"
			target += "/" + str(existing["id"])
		} else {
			var ee *exitError
			if !errors.As(e, &ee) || ee.code != 5 {
				return e
			}
		}
		return a.call(c, method, a.path(target), map[string]any{"key": args[0], "value": string(raw)})
	}}
	set.Flags().StringVar(&file, "file", "", "Secret value file; default stdin, preserve newlines")
	parent.AddCommand(set)
	parent.AddCommand(&cobra.Command{Use: "unset KEY", Short: "Delete an encrypted secret", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		base, e := baseFor(c)
		if e != nil {
			return e
		}
		item, e := a.resolve(c, base, args[0])
		if e != nil {
			return e
		}
		if e = a.confirm("Delete secret " + str(item["key"]) + "?"); e != nil {
			return e
		}
		return a.call(c, "DELETE", a.path(base+"/"+str(item["id"])), map[string]any{})
	}})
	parent.AddCommand(&cobra.Command{Use: "reveal KEY", Short: "Explicitly reveal one secret; Runivo audits this operation", Args: cobra.ExactArgs(1), RunE: func(c *cobra.Command, args []string) error {
		base, e := baseFor(c)
		if e != nil {
			return e
		}
		item, e := a.resolve(c, base, args[0])
		if e != nil {
			return e
		}
		if e = a.confirm("Reveal secret " + str(item["key"]) + " on stdout?"); e != nil {
			return e
		}
		var response struct{ Value string }
		if e = a.client.Do(c.Context(), "POST", a.path(base+"/"+str(item["id"])+"/reveal"), map[string]any{}, &response, ""); e != nil {
			return e
		}
		if a.json {
			return a.print(map[string]any{"key": args[0], "value": response.Value})
		}
		_, e = fmt.Fprint(a.out, response.Value)
		return e
	}})
	var importFile string
	if section == "variables" {
		imp := &cobra.Command{Use: "import --file JSON", Short: "Import a JSON object of string values; applied one key at a time", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
			base, e := baseFor(c)
			if e != nil {
				return e
			}
			payload, e := a.payload(importFile)
			if e != nil {
				return e
			}
			for key, value := range payload {
				if _, ok := value.(string); !ok || strings.TrimSpace(key) == "" {
					return errors.New("each imported value must be a string with a nonempty key")
				}
			}
			if e = a.confirm("Import environment variables? Existing keys will be replaced."); e != nil {
				return e
			}
			completed := []string{}
			for key, value := range payload {
				method, target := "POST", base
				item, e := a.resolve(c, base, key)
				if e == nil {
					method = "PATCH"
					target += "/" + str(item["id"])
				} else {
					var ee *exitError
					if !errors.As(e, &ee) || ee.code != 5 {
						return e
					}
				}
				if e = a.client.Do(c.Context(), method, a.path(target), map[string]any{"key": key, "value": value}, nil, ""); e != nil {
					return fmt.Errorf("import stopped at %s after %d keys; earlier changes remain: %w", key, len(completed), e)
				}
				completed = append(completed, key)
			}
			return a.print(map[string]any{"updated": completed})
		}}
		imp.Flags().StringVar(&importFile, "file", "", "JSON object file, or - for stdin")
		_ = imp.MarkFlagRequired("file")
		parent.AddCommand(imp)
	}
	return parent
}
