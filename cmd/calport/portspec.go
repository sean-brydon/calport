package main

import (
	"fmt"
	"strconv"
	"strings"
)

type portMap struct{ local, remote int }

const maxPortsPerCommand = 100

// parsePorts reads "3000", "8080:3000", "3000-3005" and comma lists of them.
func parsePorts(spec string) ([]portMap, error) {
	var out []portMap
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			return nil, fmt.Errorf("empty port in %q", spec)
		}
		if l, r, ok := strings.Cut(part, ":"); ok {
			local, err := port(l)
			if err != nil {
				return nil, err
			}
			remote, err := port(r)
			if err != nil {
				return nil, err
			}
			out = append(out, portMap{local, remote})
			continue
		}
		if a, b, ok := strings.Cut(part, "-"); ok {
			from, err := port(a)
			if err != nil {
				return nil, err
			}
			to, err := port(b)
			if err != nil {
				return nil, err
			}
			if to < from {
				return nil, fmt.Errorf("port range %q runs backwards", part)
			}
			if to-from >= maxPortsPerCommand {
				return nil, fmt.Errorf("port range %q is larger than %d ports", part, maxPortsPerCommand)
			}
			for p := from; p <= to; p++ {
				out = append(out, portMap{p, p})
			}
			continue
		}
		p, err := port(part)
		if err != nil {
			return nil, err
		}
		out = append(out, portMap{p, p})
	}
	if len(out) > maxPortsPerCommand {
		return nil, fmt.Errorf("at most %d ports at once", maxPortsPerCommand)
	}
	return out, nil
}

func port(s string) (int, error) {
	p, err := strconv.Atoi(s)
	if err != nil || p < 1 || p > 65535 || strconv.Itoa(p) != s {
		return 0, fmt.Errorf("invalid port %q", s)
	}
	return p, nil
}
