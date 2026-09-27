package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"runtime"
	"text/tabwriter"

	"github.com/sean-brydon/calport/internal/forward"
	"github.com/sean-brydon/calport/internal/wire"
)

// networkDialer reaches addresses through the agent's named network, so the
// CLI can pair with and SSH to boxes on tailnets this laptop has not joined.
func networkDialer(l laptop, name string) (wire.DialFunc, error) {
	if name == "" {
		return nil, nil
	}
	c, err := ensureAgent(l)
	if err != nil {
		return nil, err
	}
	return func(ctx context.Context, _, addr string) (net.Conn, error) {
		return c.Dial(ctx, name, addr)
	}, nil
}

func networkCommand(l laptop, args []string) error {
	if len(args) == 0 {
		return errors.New("usage: calport network login NAME | calport network proxy NAME HOST PORT")
	}
	switch args[0] {
	case "login":
		if len(args) != 2 {
			return errors.New("usage: calport network login NAME")
		}
		c, err := ensureAgent(l)
		if err != nil {
			return err
		}
		info, err := c.Login(context.Background(), args[1], func(url string) {
			fmt.Printf("Sign in to the tailnet for %q:\n  %s\n", args[1], url)
			opener := "xdg-open"
			if runtime.GOOS == "darwin" {
				opener = "open"
			}
			exec.Command(opener, url).Start()
		})
		if err != nil {
			return err
		}
		fmt.Printf("Network %s is connected to %v as %v.\n", args[1], info["tailnet"], info["ips"])
		return nil
	case "proxy":
		// For ssh's ProxyCommand: `-o ProxyCommand="calport network proxy NAME %h %p"`.
		if len(args) != 4 {
			return errors.New("usage: calport network proxy NAME HOST PORT")
		}
		c, err := ensureAgent(l)
		if err != nil {
			return err
		}
		conn, err := c.Dial(context.Background(), args[1], net.JoinHostPort(args[2], args[3]))
		if err != nil {
			return err
		}
		forward.Bridge(context.Background(), stdioConn{}, conn)
		return nil
	}
	return fmt.Errorf("unknown network command %q", args[0])
}

func listNetworks(l laptop, args []string) error {
	_, asJSON, err := flags("networks", args, nil)
	if err != nil {
		return err
	}
	c, err := ensureAgent(l)
	if err != nil {
		return err
	}
	var nets []map[string]any
	if err := c.Call(context.Background(), "GET", "/v1/networks", nil, &nets); err != nil {
		return err
	}
	if asJSON {
		return printJSON(nets)
	}
	if len(nets) == 0 {
		fmt.Println("No networks. Reach a tailnet this laptop is not on with: calport network login NAME")
		return nil
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tSTATE\tTAILNET\tADDRESSES")
	for _, n := range nets {
		fmt.Fprintf(w, "%v\t%v\t%v\t%v\n", n["name"], n["state"], orDash(n["tailnet"]), orDash(n["ips"]))
	}
	return w.Flush()
}

func orDash(v any) any {
	if v == nil {
		return "-"
	}
	return v
}

// stdioConn presents this process's stdin and stdout as one connection, for
// relaying an SSH session.
type stdioConn struct{ net.Conn }

func (stdioConn) Read(b []byte) (int, error)  { return os.Stdin.Read(b) }
func (stdioConn) Write(b []byte) (int, error) { return os.Stdout.Write(b) }
func (stdioConn) CloseWrite() error           { return os.Stdout.Close() }
func (stdioConn) Close() error                { os.Stdin.Close(); return os.Stdout.Close() }
