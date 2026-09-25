package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"
)

func terminalSize(fd int) (int, int) {
	cols, rows, e := term.GetSize(fd)
	if e != nil {
		return 100, 28
	}
	return max(20, min(300, cols)), max(5, min(100, rows))
}
func (a *app) shellCommand() *cobra.Command {
	var instance string
	c := &cobra.Command{Use: "shell", Short: "Open an interactive terminal on a running instance; Ctrl+] disconnects", Args: cobra.NoArgs, RunE: func(c *cobra.Command, _ []string) error {
		if a.json {
			return errors.New("interactive shell cannot use --json; use jobs run for scripting")
		}
		input, ok := a.in.(*os.File)
		if !ok || !term.IsTerminal(int(input.Fd())) {
			return errors.New("shell requires an interactive terminal; use jobs run --command for automation")
		}
		base, s, e := a.servicePath(c)
		if e != nil {
			return e
		}
		fd := int(input.Fd())
		cols, rows := terminalSize(fd)
		var created struct{ ID string }
		e = a.client.Do(c.Context(), "POST", a.path(base+"/terminal"), map[string]any{"cols": cols, "rows": rows, "instance": instance, "requestId": requestID()}, &created, "")
		if e != nil {
			return e
		}
		if !validID(created.ID) {
			return errors.New("terminal response did not contain a valid session ID")
		}
		target := a.path(base + "/terminal/" + created.ID)
		defer func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			_ = a.client.Do(ctx, "DELETE", target, nil, nil, "")
		}()
		fmt.Fprintf(a.errout, "Connecting to %s. Press Ctrl+] to disconnect.\n", clean(str(s["name"])))
		state, e := term.MakeRaw(fd)
		if e != nil {
			return e
		}
		defer term.Restore(fd, state)
		ctx, cancel := context.WithCancel(c.Context())
		defer cancel()
		inputs := make(chan []byte, 8)
		readErrors := make(chan error, 1)
		go func() {
			buffer := make([]byte, 4096)
			for {
				n, e := input.Read(buffer)
				if n > 0 {
					copyOf := append([]byte(nil), buffer[:n]...)
					select {
					case inputs <- copyOf:
					case <-ctx.Done():
						return
					}
				}
				if e != nil {
					readErrors <- e
					return
				}
			}
		}()
		output := time.NewTicker(300 * time.Millisecond)
		defer output.Stop()
		resize := time.NewTicker(time.Second)
		defer resize.Stop()
		var cursor int64
		for {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case e := <-readErrors:
				if e == io.EOF {
					return nil
				}
				return e
			case data := <-inputs:
				if bytes.Contains(data, []byte{29}) {
					return nil
				}
				if e = a.client.Do(ctx, "POST", target+"/input", map[string]any{"input": base64.StdEncoding.EncodeToString(data), "requestId": requestID()}, nil, ""); e != nil {
					return fmt.Errorf("terminal input could not be confirmed; disconnected to prevent duplicate commands: %w", e)
				}
			case <-resize.C:
				width, height := terminalSize(fd)
				if width != cols || height != rows {
					cols, rows = width, height
					if e = a.client.Do(ctx, "POST", target+"/input", map[string]any{"cols": cols, "rows": rows, "requestId": requestID()}, nil, ""); e != nil {
						return e
					}
				}
			case <-output.C:
				for {
					var response struct {
						Status, Detail string
						Cursor         int64
						HasMore        bool
						Frames         []struct {
							ID   int64
							Data string
						}
					}
					if e = a.client.Do(ctx, "GET", target+"?after="+strconv.FormatInt(cursor, 10), nil, &response, ""); e != nil {
						return e
					}
					for _, frame := range response.Frames {
						if frame.ID <= cursor {
							continue
						}
						decoded, e := base64.StdEncoding.DecodeString(frame.Data)
						if e != nil {
							return errors.New("invalid terminal frame")
						}
						if _, e = a.out.Write(decoded); e != nil {
							return e
						}
						cursor = frame.ID
					}
					if response.HasMore {
						if response.Cursor < cursor {
							return errors.New("invalid terminal cursor")
						}
						continue
					}
					if response.Status == "closed" || response.Status == "failed" {
						fmt.Fprintf(a.errout, "\r\n%s\r\n", clean(response.Detail))
						return nil
					}
					break
				}
			}
		}
	}}
	c.Flags().StringVar(&instance, "instance", "", "Running instance/container ID; default first available")
	return c
}
